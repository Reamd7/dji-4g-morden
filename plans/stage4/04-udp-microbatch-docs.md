# 子计划 04 — UDP ASSOCIATE + micro-batching + 文档收尾

> 隶属 `plans/stage4-dual-backend.md`(总览)。**Stage 4 收尾**。
> 依赖子计划 00-03 通过。
>
> **目标**:补齐 UDP 支持(SOCKS5 UDP ASSOCIATE)、可选 micro-batching 性能优化、完成文档更新并把 Stage 4 计划归档。

---

## 一、目标

子计划 03 之后的两个遗留项 + 文档收尾:
1. **UDP ASSOCIATE**:SOCKS5 协议的 UDP relay 模式,让 `dig`/Quic/UDP 应用经代理
2. **micro-batching**(可选,来自逆向文档 B2):上行包合并,减少 USB write 次数
3. **文档收尾**:AGENTS.md 更新(PacketSink 措辞统一、Stage 4 记录、03-dual-backend 加入索引),把 plans/stage4* 移入 plans/archive/

---

## 二、依赖

- **子计划 00-03 通过**:PacketSink + netstack + SOCKS5 TCP + DNS/IPv6 已就位
- **armon/go-socks5 已支持 UDP**(子计划 02 导入,本计划只需接入)
- **micro-batching 无新增依赖**(纯 relay 内部优化)

---

## 三、实现

### 3.1 UDP ASSOCIATE

SOCKS5 协议(RFC 1928 §6)的 UDP ASSOCIATE 让客户端通过 UDP 经代理转发。armon/go-socks5 已支持,netstack 也已有 UDP transport。

**改动点**:
- `netstack_dialer.go`:加 UDP dial 分支
- `socks5.go`:确认 armon/go-socks5 的 UDP relay 接到 netstack UDP

```go
// internal/qmidatapath/netstack_dialer.go (扩展)

import (
    "gvisor.dev/gvisor/pkg/tcpip/adapter/gonet"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
    "gvisor.dev/gvisor/pkg/tcpip/transport/udp"  // 已在子计划 01 导入
)

func (s *NetstackPacketSink) NetstackDialer() func(ctx, network, addr string) (net.Conn, error) {
    return func(ctx context.Context, network, addr string) (net.Conn, error) {
        // ... 解析 addr、DNS、构造 fullAddr(同子计划 02)...

        switch network {
        case "tcp", "tcp4":
            return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv4.ProtocolNumber)
        case "tcp6":
            return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv6.ProtocolNumber)
        case "udp", "udp4":
            // gonet.DialUDP 或 DialContextUDP(按锁定版本 API)
            return gonet.DialContextUDP(ctx, s.stk, fullAddr, ipv4.ProtocolNumber)
        case "udp6":
            return gonet.DialContextUDP(ctx, s.stk, fullAddr, ipv6.ProtocolNumber)
        default:
            return nil, fmt.Errorf("netstack dialer: unsupported network %q", network)
        }
    }
}
```

**armon/go-socks5 的 UDP 处理**:库内部已实现 UDP ASSOCIATE 握手 + UDP relay。我们只需确保 `Config.Dial` 能处理 `"udp"` network(上面已加)。库会自动把 UDP relay 包转发到我们的 dialer 建立的 UDP conn。

**验证**:
```bash
# dig 经 SOCKS5 UDP
dig @8.8.8.8 example.com  # 默认走 host,作为对照
# 用 SOCKS5 UDP:需要一个支持 SOCKS5 UDP 的客户端(如 proxychains-ng,或配置浏览器)
# 或用 Python/Go 写最小 SOCKS5 UDP client 测试
```

> **注意**:SOCKS5 UDP ASSOCIATE 的客户端实现较少(很多工具只支持 TCP CONNECT)。如果实测发现 UDP 工具链不成熟,可记录为"UDP 技术上已支持,但客户端工具有限",不阻塞 Stage 4 完成。

### 3.2 micro-batching(可选优化)

**来源**:逆向文档 `docs/performance/01-qcusbwwan-reverse-engineering.md` 的 B2(批量发送 micro-batching)。

**问题**:子计划 00 的重构把 TUN batch read 退化为单包读。每个上行包单独一次 `bulkOut.Write`,高频小包场景 USB write 次数多。

**方案**:`sinkToModem` 累积多个包,合并到一次或少数几次 bulkOut.Write。

```go
// internal/qmidatapath/relay.go (可选优化,加 flag 控制)

type Bridge struct {
    // ... 现有字段 ...
    microBatch bool       // 新增:是否启用 micro-batching(默认 false)
    batchSize  int        // 累积多少包后 flush(默认 16)
    batchDelay time.Duration  // 超时 flush(默认 1ms)
}

func (b *Bridge) sinkToModem(ctx context.Context) {
    defer b.wg.Done()

    if !b.microBatch {
        b.sinkToModemSingle(ctx)  // 子计划 00 的单包逻辑,默认路径
        return
    }

    // micro-batching 路径(默认关闭,实验性)
    batch := make([][]byte, 0, b.batchSize)
    timer := time.NewTimer(b.batchDelay)
    defer timer.Stop()

    flush := func() {
        if len(batch) == 0 { return }
        // 合并多个包为一次 write
        // 注意:USB bulk 是流式的,合并写需要 modem 能区分包边界
        // —— raw-IP 模式下,每个 IP 包自带长度字段(IP header total length),
        //    modem 按 IP 包解析,所以合并写是安全的
        var buf []byte
        for _, pkt := range batch {
            buf = append(buf, pkt...)
            // ZLP 检查(每个包独立)
            if b.zlp && len(pkt)%bulkMaxPacketSize == 0 {
                buf = append(buf, make([]byte, 0)...)  // ZLP 标记
            }
            b.txPackets.Add(1)
            b.txBytes.Add(int64(len(pkt)))
        }
        b.bulkOut.Write(buf)
        batch = batch[:0]
    }

    for {
        select {
        case <-ctx.Done():
            flush()
            return
        case pkt := <-b.sinkPackets:  // 需要把 ReadPacket 改为 channel 模式
            batch = append(batch, pkt)
            if len(batch) >= b.batchSize {
                flush()
            }
        case <-timer.C:
            flush()
            timer.Reset(b.batchDelay)
        }
    }
}
```

**风险**:
- 合并写需要 modem 的 raw-IP 解析正确(IP header total length 字段)。bulkprobe 已确认 raw-IP 模式,理论上安全,但需实测
- ZLP 在合并写中的处理变复杂(每个包独立检查 512 倍数)
- 可能增加延迟(1ms 累积窗口)

**决策**:默认 `microBatch=false`(不启用)。实施后用一个性能基准测试对比开关前后,确认收益再决定是否默认开。

### 3.3 文档收尾

#### AGENTS.md 更新(项目根目录)

1. **措辞统一**:把 "DataSink" 改为 "PacketSink"(子计划 00-04 用的是 PacketSink)
2. **Stage 4 完成记录**:在 "实测验证结果" 章节加 Stage 4 记录段
   - netstack + SOCKS5 后端验证结果(curl HTTP 200,无需 admin)
   - macOS 验证(无需 sudo)
   - PacketSink 接口说明
3. **下一步更新**:把"TUN + netstack 双数据后端"从"下一步"移到"已完成",标注 Stage 5 候选(多设备负载均衡、QMAP 聚合等性能优化)
4. **索引补充**:`docs/performance/03-dual-backend-implementation-plan.md` 加入相关调研文档索引

#### `internal/qmidatapath/AGENTS.md` 更新

```markdown
# AGENTS.md — internal/qmidatapath

## 包职责(更新)

USB bulk EP 与主机侧网络栈之间的双向 raw IP 中继。
- 后端 1(Stage 3):TUNPacketSink → wireguard/tun → 内核 TCP/IP(需 admin)
- 后端 2(Stage 4):NetstackPacketSink → gVisor netstack → SOCKS5(无需 admin)

## 核心结构(更新)

- `sink.go`:PacketSink 接口(导出,可测性注入)
- `tun_sink.go`:TUNPacketSink(包装 wireguard/tun.Device,内部处理 offset)
- `netstack_sink.go`:NetstackPacketSink(gVisor channel link endpoint)
- `netstack_dialer.go`:gVisor → net.Conn dialer(供 SOCKS5 Config.Dial)
- `socks5.go`:RunSOCKS5(armon/go-socks5 包装)
- `relay.go`:Bridge(PacketSink ↔ bulk EP,ZLP 保留)

## 关键设计决策(更新)

### PacketSink 抽象(Stage 4)

Bridge 只跟 PacketSink 接口对话,不知道 TUN/netstack 的存在。
- ReadPacket(ctx):host → modem(上行,netstack channel.ReadContext / TUN batch read)
- WritePacket(pkt):modem → host(下行,InjectInbound / TUN batch write)
- Close 必须解除 ReadPacket 阻塞(防 bridge.Stop 死锁)

### Close 时序(更新,TUN vs netstack 差异)

- **TUNPacketSink**:`tun.Read` 不响应 ctx,必须 `tun.Close() → bridge.Stop()`
- **NetstackPacketSink**:`channel.ReadContext` 支持 ctx,`sink.Close() → bridge.Stop()`
  即可,更干净

### ZLP(不变)

zlp=true 保留(QDC507 bulk OUT 512 倍数追加 0 字节,USB 物理约束)。
ZLP 逻辑在 sinkToModem 内,与后端无关。

### raw-IP 直传(不变)

WDA SetDataFormat 后 bulk EP 是裸 IP,netstack channel 也是裸 IP,直传。

### gVisor 版本锁定

go.mod 锁定 gVisor commit:<填入实际 commit>。
netstack_sink.go 用 pkg/tcpip 稳定子包,不用 master 未稳定 API。

## 测试(更新)

- `relay_test.go`:PacketSink mock 测试(子计划 00)
- `tun_sink_test.go`:TUN offset 包装测试(子计划 00)
- `netstack_sink_test.go`:netstack 收发 + ctx cancel + Close 测试(子计划 01)
- `socks5_test.go`:SOCKS5 CONNECT 握手测试(子计划 02)
- `relay_hardware_test.go`:硬件 TUN relay(build tag: hardware)
- 待加:netstack hardware 测试(curl --socks5 端到端)

## 不做的事(不变)

- 不做内核 TCP/IP(交给 TUN/内核)
- 不做路由配置(manager.configureNetwork / netstack 内部)
- 不做 DNS 配置(netstack 内部 resolver / cmd 层)
```

#### 归档(项目惯例)

已完成阶段的计划移入 `plans/archive/`:

```bash
# Stage 4 全部通过后
mkdir -p plans/archive/stage4
mv plans/stage4-dual-backend.md plans/archive/stage4-dual-backend.md
mv plans/stage4/00-packetsink-refactor.md plans/archive/stage4/00-packetsink-refactor.md
# ... 01-04 同理
rmdir plans/stage4  # 如果空了
```

参考:`plans/archive/stage2*` 和 `plans/archive/stage3*` 的归档模式。

---

## 四、验证

```bash
# 1. UDP ASSOCIATE 验证(工具成熟度依赖客户端)
# 若有支持 SOCKS5 UDP 的客户端:
dig @8.8.8.8 example.com via SOCKS5 UDP
# 否则:记录为"技术上已支持,客户端工具有限"

# 2. micro-batching 基准(可选)
mise exec -- go test -bench=. ./internal/qmidatapath/ -benchmem
# 对比 microBatch 开关前后的 bulkOut.Write 次数

# 3. 全套测试回归
mise exec -- go test -race ./internal/qmidatapath/
mise exec -- go test -tags=hardware ./internal/qmidatapath/

# 4. 端到端回归(TUN + SOCKS5 都跑)
./qmidial.exe -dial -tun           # TUN 模式不变
./qmidial.exe -dial -socks5        # SOCKS5 模式
curl --socks5-hostname 127.0.0.1:1080 http://www.baidu.com

# 5. 文档检查
grep -r "DataSink" AGENTS.md docs/  # 确认全部改为 PacketSink
ls plans/archive/stage4/            # 确认归档
```

**子计划 04 通过判据**:
- UDP dialer 支持连通(或记录为已知限制)
- micro-batching 实现但默认关闭(或确认收益后默认开)
- AGENTS.md PacketSink 措辞统一,grep 无残留 DataSink
- `internal/qmidatapath/AGENTS.md` 更新双后端 + Close 时序 + gVisor 版本
- plans/stage4* 移入 plans/archive/

---

## 五、涉及文件

| 文件 | 改动 |
|---|---|
| `internal/qmidatapath/netstack_dialer.go` | 改 — UDP dial 分支 |
| `internal/qmidatapath/socks5.go` | (可能改)UDP relay 接入 |
| `internal/qmidatapath/relay.go` | (可选)micro-batching 路径 |
| `internal/qmidatapath/relay_test.go` | (可选)micro-batch 测试 |
| `AGENTS.md` | 改 — PacketSink 措辞 + Stage 4 记录 + 索引 |
| `internal/qmidatapath/AGENTS.md` | 改 — 双后端 + Close 时序 + gVisor 版本 |
| `plans/stage4*` | 移入 `plans/archive/stage4/` |

---

## 六、风险 & 缓解

### R1:SOCKS5 UDP 客户端工具链不成熟(低-中)

很多工具(curl、浏览器)只支持 SOCKS5 TCP CONNECT,UDP ASSOCIATE 客户端少。

**缓解**:技术上实现到 dialer 层(armon/go-socks5 + netstack UDP)。验证用最小手写 SOCKS5 UDP client(Python/Go ~50 行)。如果实测困难,记录为"已实现,客户端工具有限",不阻塞 Stage 4。

### R2:micro-batching 的 modem 兼容性(低)

合并写多个 IP 包,QDC507 是否正确按 IP header 解析包边界?

**缓解**:
- raw-IP 模式下 IP 包自带长度字段,理论安全
- 但实测前不确定。默认关闭 micro-batch,先验证再决定
- 如果实测发现包边界问题,放弃 micro-batch(单包写在 4G 场景已够用)

### R3:归档时机(低)

过早归档(Stage 4 未完全稳定)会让后续修复找不到计划。

**缓解**:归档前确认子计划 00-04 全部通过判据,且 macOS 回归验证完成。归档后如需重大修改,在 plans/ 新开 hotfix 文档。
