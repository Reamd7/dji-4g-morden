# 子计划 01 — bulk EP 数据探针(阶段 3 门控)

> 隶属 `plans/03/00-overview.md`。**阶段 3 头号风险门控**(R1 + R2)。
> 创建于 2026-07-12。

---

## 一、目标

验证两件事(决定整个阶段 3 方案是否成立):

1. **WDA SetDataFormat 在 QDC507 上成功**(R2):设 `NetInterface="dummy"` 触发 WDA 分配 → `SetDataFormat(LinkProtocolIP)` → `GetDataFormat` 确认 raw-IP
2. **bulk IN EP 0x88 承载 raw IP 数据**(R1):WDA+拨号后从 bulk IN 读,检查首字节 IP version = 4 或 6

如果两个都通过 → 阶段 3 relay 方案成立,子计划 02 解除阻塞。
如果 WDA 失败 → 降级 QMAP(需剥 4 字节头,子计划 02 加适配层)。
如果 bulk 不承载 IP → 方案需大改(另一接口?802.3?)。

---

## 二、依赖 / 前置

- 阶段 2 完成(`QMITransport` + `qmi.NewClientFromTransport` + `manager.NewWithClient` 可用)
- 真实 DJI 设备(QDC507, PID 0x0125)+ WinUSB on MI_04 + SIM(PS attach)
- **Wintun.dll 不需要**(此探针不创建 TUN)

---

## 三、交付物

### 1. `internal/qmitransport/bulkendpoints.go`(~30 行)

QMITransport 新增方法,打开 MI_04 的 bulk IN/OUT endpoints:

```go
package qmitransport

import "github.com/google/gousb"

// MI_04 bulk endpoints for raw IP data (see AGENTS.md endpoint table).
const (
    DefaultBulkInEP  = 0x88 // bulk IN: raw IP from modem
    DefaultBulkOutEP = 0x05 // bulk OUT: raw IP to modem
)

// OpenBulkEndpoints opens MI_04's bulk IN (EP 0x88) and bulk OUT (EP 0x05)
// for raw IP data transfer. Must be called after Open(). The endpoints are
// on the same claimed interface as the control path (EP0 + interrupt 0x89) —
// no additional USB claim needed; bulk and control use different endpoints
// with no contention.
//
// The returned endpoints operate independently of QMITransport's ioMu (which
// only serializes EP0 control transfers). Callers MUST stop using these
// endpoints before Close() — Close releases the underlying interface.
func (t *QMITransport) OpenBulkEndpoints() (bulkIn *gousb.InEndpoint, bulkOut *gousb.OutEndpoint, err error)
```

实现要点:
- 加锁 `t.ioMu`(防止与 Close 竞争,与 EP0 control transfer 同锁)
- 检查 `t.closed`
- `t.iface.InEndpoint(DefaultBulkInEP)` / `t.iface.OutEndpoint(DefaultBulkOutEP)`
- 返回原始 `*gousb.InEndpoint` / `*gousb.OutEndpoint`(子计划 02 的 relay 直接用 ReadContext/Write)

### 2. `cmd/bulkprobe/main.go`(~120 行)

渐进式探针,4 个阶段逐步验证:

#### Phase A:WDA 分配 + SetDataFormat

```go
cfg := manager.Config{
    Device:      manager.ModemDevice{NetInterface: "dummy"}, // 触发 shouldAllocateWDA
    EnableIPv4:  true,
    // 不设 APN,不拨号
}
mgr := manager.NewWithClient(cfg, nil, client)
mgr.StartCoreContext(ctx)
```

验证:
- `shouldAllocateWDA()` → true(manager 日志显示 WDA 分配)
- `enableRawIP()` 路径:`wda.GetDataFormat()` 应返回 `LinkProtocol=0x02`
- 如果 SetDataFormat 失败,打印错误并停止(R2 失败,需降级)

**注意(本轮调研发现)**:`enableRawIP` 在 Linux 路径会查 sysfs raw_ip 节点(2535 行),Windows 上 `runtime.GOOS == "linux"` 为 false 跳过。但 modem 侧 `wda.SetDataFormat(LinkProtocolIP)` 在非 Linux 也执行(2587 行)。所以 Windows 上能协商 modem 侧 raw-IP,只是跳过内核 sysfs(我们没内核驱动,正合适)。

#### Phase B:WDS StartNetwork(拨号)

```go
cfg.APN = "3gnet"  // 或从环境变量
mgr.Connect()  // WDS StartNetwork → 拿 PDH + IP(复用阶段 2 流程)
```

验证:拿到 IPv4 地址 + PDH(与阶段 2 实测一致:IP `10.147.x.x`)。

#### Phase C:bulk EP 读取(核心 R1 验证)

```go
bulkIn, bulkOut, err := transport.OpenBulkEndpoints()

ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
defer cancel()
buf := make([]byte, 65535)  // R4 缓解:大 buffer + short packet 检测
count := 0
for {
    n, err := bulkIn.ReadContext(ctx, buf)
    if err != nil { break }
    if n == 0 { continue }
    pkt := buf[:n]
    version := pkt[0] >> 4
    fmt.Printf("bulk IN: %d bytes, IP version=%d, first 20 bytes: % x\n",
        n, version, pkt[:min(20,n)])
    count++
}
fmt.Printf("Read %d packets in 8s\n", count)
```

判断标准:
- `version == 4`(IPv4,首字节 `0x45`)或 `version == 6`(IPv6,首字节 `0x60`)→ **raw-IP 确认** ✅
- `version` 在 `0x00-0x07`(像 mux_id)→ **QMAP 模式**,数据有 4 字节头需剥
- 无数据 → 走 Phase D(modem 可能不主动发包,需先触发上行)

#### Phase D:触发流量(如果 Phase C 无数据)

modem 可能只在有上行流量时才发下行。手动构造 ICMP echo request:

```go
// 构造最小 ICMP echo request(IP 头 20 + ICMP 8 + 可选 payload)
// src = 拨号拿的 IP(10.147.0.1),dst = 8.8.8.8 或网关
icmpPkt := buildICMPEcho(srcIP, "8.8.8.8")
bulkOut.Write(icmpPkt)
// 然后从 bulkIn 读,看是否收到 ICMP echo reply 或其他响应
```

如果手动发包后 bulk IN 收到响应 → relay 可行。
如果仍无响应 → 可能需要 ZLP(R5),进 Phase D2。

#### Phase D2:ZLP 测试(R5 验证)

构造恰好 512 字节的 IP 包(padding ICMP payload 到对齐):

```go
pkt512 := buildICMPEchoPadded(srcIP, "8.8.8.8", 512)  // 恰好 512 字节
bulkOut.Write(pkt512)
// 不追加 ZLP,读 bulk IN
// 如果卡住 → modem 期望 ZLP(len%512==0 时需追加 0 字节 Write)
// 如果收到 reply → modem 不需要 ZLP,relay 简化
```

---

## 四、完成标志

- [ ] `internal/qmitransport/bulkendpoints.go` 编译通过 + 单测(锁/closed 检查)
- [ ] `cmd/bulkprobe/main.go` 可运行
- [ ] WDA SetDataFormat 在 QDC507 上成功(GetDataFormat 返回 LinkProtocol=0x02)
- [ ] WDS StartNetwork 成功(拿到 IP,与阶段 2 一致)
- [ ] **bulk IN EP 0x88 读到数据,首字节 IP version = 4 或 6(核心 R1 验证)**
- [ ] 或:手动发 IP 包到 bulk OUT 0x05,从 bulk IN 0x88 收到响应(回环验证)
- [ ] ZLP 测试:512 字节包无 ZLP 是否卡住(决定 relay 是否需手动 ZLP)

---

## 五、风险与降级

| 风险 | 缓解 / 降级 |
|---|---|
| WDA SetDataFormat 失败(R2) | 降级 QMAP:`wda.SetDataFormat` 带 UL/DL aggregation=0x05,数据带 4 字节头,子计划 02 加 strip/add 层 |
| bulk EP 无数据 | Phase D 手动 ICMP 触发;仍无则查是否需 ZLP |
| bulk EP 数据不是 raw IP(QMAP) | 首字节 0x00-0x7f(mux_id)→ QMAP。子计划 02 加 4 字节头处理:`[mux_id(1)][flags(1)][pkt_len_be16(2)][IP payload]` |
| gousb bulk Read 阻塞不返回 | `context.WithTimeout(8s)` 兜底 |
| `dummy` 接口名导致 enableRawIP 尝试 sysfs | Windows 上 `GOOS != linux` 跳过 sysfs;`configureNetwork` 对 dummy 名配 IP 会失败但只 Warn,不影响探针 |

---

## 六、探针预期输出

```
[Phase A] WDA allocation + SetDataFormat
  WDA allocated: OK
  GetDataFormat: LinkProtocol=0x02 (raw IP) ✓
[Phase B] WDS StartNetwork
  Connected: IP=10.147.0.1, PDH=0x...
[Phase C] Bulk IN read (8s)
  bulk IN: 74 bytes, IP version=4, first 20 bytes: 45 00 00 4a ...
  bulk IN: 90 bytes, IP version=4, first 20 bytes: 45 00 00 5a ...
  Read 12 packets in 8s
✓ bulk EP carries raw IP data — stage 3 relay is viable
[Phase D2] ZLP test (512B packet)
  reply received without ZLP → modem does not require ZLP
```

---

## 七、相关文件

- `internal/qmitransport/qmitransport.go` —— 现有,OpenBulkEndpoints 加在此包(iface 字段同包可访问)
- `third_party/quectel-qmi-go/manager/manager.go` —— shouldAllocateWDA(:1170)、enableRawIP(:2524)、configureNetwork(:3414)
- `third_party/quectel-qmi-go/qmi/wda.go` —— SetDataFormat(:93)、GetDataFormat(:144)、LinkProtocolIP=0x02(:265)
- `cmd/qmidial/main.go` —— 阶段 2 拨号工具(参考 WDA/Connect 流程)
- `AGENTS.md` —— MI_04 endpoint 表(0x05/0x88/0x89)+ 拨号实测结果
