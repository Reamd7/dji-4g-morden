# 子计划 00 — Phase 0:bulk EP 数据探针

> 隶属 `plans/stage3-tun-internet.md`(总览)。阶段 3 头号风险门控。
> 创建于 2026-07-12。

## 目标

验证 WDA SetDataFormat + WDS StartNetwork 后,MI_04 的 bulk EP 0x88 IN 是否返回
raw IP 数据包。这是整个阶段 3 的前置条件——如果 bulk EP 不承载 IP 数据,
中继方案不成立,需要调整。

同时验证 WDA SetDataFormat 在 QDC507 上是否成功(阶段 2 从未分配 WDA)。

## 依赖 / 前置

- 阶段 2 完成(QMITransport + qmi.Client + manager.NewWithClient 可用)
- 真实 DJI 设备(PID 0x0125)+ WinUSB on MI_04 + SIM(PS attach)
- Wintun.dll **不需要**(此探针不创建 TUN)

## 步骤

### 1. 创建 `cmd/bulkprobe/main.go`

渐进式探针工具,4 个阶段逐步验证:

#### Phase A:WDA 分配 + SetDataFormat

```go
cfg := manager.Config{
    Device: manager.ModemDevice{NetInterface: "dummy"}, // 触发 shouldAllocateWDA
    EnableIPv4: true,
    // 不设 APN,不拨号
}
mgr := manager.NewWithClient(cfg, nil, client)
mgr.StartCoreContext(ctx)
```

验证:
- `shouldAllocateWDA()` → true(WDA 分配日志)
- `enableRawIP()` → `wda.GetDataFormat()` 返回 LinkProtocol=0x02
- 如果 SetDataFormat 失败,打印错误并停止

#### Phase B:WDS StartNetwork(拨号)

```go
cfg.APN = "3gnet"
mgr.Connect()  // WDS StartNetwork → 拿 PDH + IP
```

验证:拿到 IPv4 地址 + PDH(与阶段 2 一致)。

#### Phase C:bulk EP 读取

在 `mgr.Connect()` 之后,打开 MI_04 的 bulk IN EP 0x88,尝试读取数据:

```go
// 从 QMITransport 获取 bulk endpoints
bulkIn, bulkOut, err := transport.OpenBulkEndpoints()

// 读 5 秒
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
buf := make([]byte, 65535)
for {
    n, err := bulkIn.ReadContext(ctx, buf)
    if err != nil { break }
    pkt := buf[:n]
    // 检查是否是 IP 包:第一个 byte 的高 4 bit = 4(IPv4)或 6(IPv6)
    version := pkt[0] >> 4
    fmt.Printf("bulk IN: %d bytes, IP version=%d, first 20 bytes: % x\n", n, version, pkt[:min(20,n)])
}
```

验证:
- 收到数据(IP 包,version=4 或 6)
- 或者无数据(modem 不主动发包,需要先发数据触发响应)

#### Phase D:触发流量(如果 Phase C 无数据)

如果 Phase C 没收到数据,可能是 modem 只在有上行流量时才发下行数据。
尝试:
1. 手动构造一个 ICMP echo request(IP 头 + ICMP),写入 bulk OUT EP 0x05
2. 然后从 bulk IN EP 0x88 读,看是否收到 ICMP echo reply
3. 或者用 `ping` 命令(如果 TUN 已创建,但此探针不创建 TUN,所以用手动构造)

#### Phase D2:ZLP 测试(R5 验证)

手动构造一个 **恰好 512 字节**的 IP 包(padding ICMP payload),写入 bulk OUT EP 0x05,
然后从 bulk IN EP 0x88 读。如果不追加 ZLP 也能收到 reply,说明 modem 不需要 ZLP。
如果卡住,说明需要 ZLP(在 `tunToModem` 里加 0 字节 Write)。

注意:Linux 驱动 `qmi_wwan_q.c` 对 EC25 设了 `FLAG_SEND_ZLP`,说明 modem 期望 ZLP。
但 WinUSB 的行为可能与 Linux 不同。

### 2. `internal/qmitransport/bulkendpoints.go`

QMITransport 新增方法,打开 bulk IN/OUT endpoints:

```go
// OpenBulkEndpoints opens MI_04's bulk IN (EP 0x88) and bulk OUT (EP 0x05)
// for raw IP data transfer. Must be called after Open(). The endpoints are
// on the same claimed interface — no additional USB claim needed.
func (t *QMITransport) OpenBulkEndpoints() (bulkIn *gousb.InEndpoint, bulkOut *gousb.OutEndpoint, err error)
```

实现:
- 加锁 `t.ioMu`(防止与 Close 竞争)
- 检查 `t.closed`
- `t.iface.InEndpoint(0x88)` → bulkIn
- `t.iface.OutEndpoint(0x05)` → bulkOut
- 返回

### 3. 常量定义

在 `qmitransport.go` 或 `bulkendpoints.go` 中:

```go
const (
    DefaultBulkInEP  = 0x88  // MI_04 bulk IN (raw IP from modem)
    DefaultBulkOutEP = 0x05  // MI_04 bulk OUT (raw IP to modem)
)
```

## 交付物 / 完成标志

- [ ] `cmd/bulkprobe/main.go` 可运行
- [ ] WDA SetDataFormat 在 QDC507 上成功(LinkProtocol=0x02)
- [ ] WDS StartNetwork 成功(拿到 IP,与阶段 2 一致)
- [ ] bulk IN EP 0x88 读到数据,且首字节 IP version = 4 或 6(**核心验证**)
- [ ] 或:手动发 IP 包到 bulk OUT 0x05,从 bulk IN 0x88 收到响应(**回环验证**)
- [ ] ZLP 测试:512 字节 IP 包无 ZLP 是否卡住(决定 relay 是否需要手动 ZLP)


## 风险

| 风险 | 缓解 |
|---|---|
| WDA SetDataFormat 失败 | 降级:尝试 QMAP 模式(WDA SetQMAPSettings + WDS BindMuxDataPort) |
| bulk EP 无数据 | 可能 modem 需要先收到上行流量。Phase D 手动构造 IP 包触发 |
| bulk EP 数据不是 raw IP(QMAP 封装) | 检查首字节:QMAP 头通常 0x00-0x7f(mux ID),IP version 不会是 0。降级到 QMAP 适配 |
| gousb bulk Read 阻塞不返回 | 用 context.WithTimeout(5s),超时后检查 |
| `dummy` 接口名导致 enableRawIP 尝试 Linux sysfs | Windows 上 isLinux=false,sysfs 被跳过。不影响 |

## 探针输出示例(预期)

```
[Phase A] WDA allocation + SetDataFormat
  WDA allocated: OK
  GetDataFormat: LinkProtocol=0x02 (raw IP) ✓
[Phase B] WDS StartNetwork
  Connected: IP=10.x.x.x, PDH=0x...
[Phase C] Bulk IN read (5s)
  bulk IN: 74 bytes, IP version=4, first 20 bytes: 45 00 00 4a ...
  bulk IN: 90 bytes, IP version=4, first 20 bytes: 45 00 00 5a ...
  Read 12 packets in 5s
✓ bulk EP carries raw IP data — stage 3 relay is viable
```
