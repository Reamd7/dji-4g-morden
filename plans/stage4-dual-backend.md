# Stage 4:TUN + netstack 双数据后端实施计划(总览)

> 基于 `docs/01` §六三阶段路线图全部完成后(Windows + macOS AT+短信+QMI拨号+TUN上网)的下一步。
> 方案总览:`docs/performance/02-tun-alternatives.md` + `docs/performance/03-dual-backend-implementation-plan.md`。
> 创建于 2026-07-13。
>
> **目标**:让 `qmidial` 支持 `-tun`(透明上网,需 admin)和 `-socks5`(SOCKS5 代理,无需 admin)两种数据后端,共享同一个 QMI 拨号链路。

## 核心动机

三阶段路线图在 Windows + macOS 上全程跑通,但 **TUN 模式有四个痛点**(详见 02-tun-alternatives):

1. 需要管理员权限(创建 TUN + 改路由表)
2. 平台差异大(Wintun/utun/tun 三套代码 + DLL)
3. 全流量劫持(需处理主网络共存)
4. macOS utun 每次 Close 重建(N 递增)

引入 **gVisor netstack + SOCKS5** 作为替代后端,**无需 admin、纯 Go 零平台代码、天然隔离**。两个后端不互斥,共享同一个 QMI transport + datapath,通过 `PacketSink` 接口切换最后一跳。

## 调研结论(2026-07-13 已确认)

1. **数据面与控制面完全分离**(实测):bulk EP(0x88/0x05)与 QMI 控制面(EP0+intr 0x89)共享 MI_04 claim,但用不同 endpoint、不共享 `ioMu` 锁。netstack 后端直接复用 `transport.OpenBulkEndpoints()`,**QMI transport / manager / 协议栈零改动**。

2. **Bridge 必须重构**:当前 `internal/qmidatapath/relay.go` 的 `Bridge` 直接依赖**私有** `tunDevice` 接口(wireguard/tun 的 batch + offset 语义),netstack channel 不匹配这个接口,必须抽象出 `PacketSink`。

3. **硬约束(已实测,netstack 后端沿用)**:
   - **ZLP**:TX 包长度是 512 整数倍时必须追加 0 字节 Write(QDC507 bulk OUT maxPacketSize 物理约束,subplan 00 D2 实测)
   - **raw-IP**:WDA SetDataFormat 后 bulk EP 上是裸 IP(无 QMAP 头),netstack channel 也是裸 IP,格式完全一致——无需任何头处理
   - **Close 时序**:`sink.Close() → bridge.Stop() → transport.Close()`(反过来死锁)

4. **netstack 后端反而更干净**:gVisor `channel.Endpoint.ReadContext(ctx)` 支持 context 取消(返回 nil),避免 TUN 的 `Read 不响应 ctx` 死锁陷阱——这是新后端的设计优势。

5. **armon/go-socks5 的 `Config.Dial` 是关键钩子**:设为 gVisor netstack dialer,SOCKS5 流量自动走 4G,无需手写协议握手。**参考实现:wireproxy 项目**(`windtf/wireproxy`)用同样的 gVisor netstack + go-socks5 组合暴露 SOCKS5 代理,架构与我们高度相似。

6. **gVisor API 确认**(pkg.go.dev 官方):
   ```go
   func channel.New(size int, mtu uint32, linkAddr tcpip.LinkAddress) *Endpoint
   func (*Endpoint).ReadContext(ctx context.Context) *stack.PacketBuffer  // ctx cancel → nil
   func (*Endpoint).InjectInbound(protocol tcpip.NetworkProtocolNumber, pkt *stack.PacketBuffer)
   ```

## 设计决策(已拍板)

| 决策点 | 选择 | 理由 |
|---|---|---|
| **接口命名** | `PacketSink`(03 文档版) | 带 ctx + Name + 清晰的 ReadPacket=上行/WritePacket=下行语义。AGENTS.md/02 的 `DataSink` 措辞需同步统一 |
| **SOCKS5** | `armon/go-socks5` 库 | 通过 `Config.Dial` 注入 gVisor dialer,无需手写协议握手 ~100 行;库本身支持 UDP(Phase 3 启用) |
| **多设备** | 暂不展开(Stage 5 future) | AGENTS.md 已标多设备为 netstack 杀手级用例,但本阶段聚焦单设备跑通,枚举/负载均衡留作后续 |
| **DNS** | Phase 2 host DNS(选项 A),Phase 3 升级 4G DNS(选项 B) | 先验证核心通路,再优化 DNS 正确性 |

## 目标架构

```
cmd/qmidial/main.go
    ├── qmitransport.Open()          → QMITransport (共用,零改动)
    ├── qmi.NewClientFromTransport() → QMI client  (共用,零改动)
    ├── manager.NewWithClient()      → manager     (共用,零改动)
    ├── [-tun]    tun.CreateTUN() + TUNPacketSink + configureNetwork + configureDNS
    ├── [-socks5] NetstackPacketSink + armon/go-socks5 ListenAndServe  (新增)
    └── qmidatapath.New(sink, bulkIn, bulkOut, mtu, zlp)
        └── Bridge { sink PacketSink, bulkIn, bulkOut }
            ├── sinkToModem: sink.ReadPacket(ctx) → bulkOut.Write() [+ZLP]
            └── modemToSink: bulkIn.ReadContext() → sink.WritePacket()
```

**关键**:Bridge 不再知道 TUN/netstack 的存在,只跟 `PacketSink` 接口对话。数据路径从 QMI transport 到 bulk endpoints 完全共享,只有最后一跳(TUN device vs netstack channel)不同。

## 子计划索引

| 子计划 | 内容 | 验证 | 风险 |
|---|---|---|---|
| [00](stage4/00-packetsink-refactor.md) | PacketSink 接口 + TUN 重构 | `-race` 通过 + 硬件 TUN 不变 + `qmidial -tun` 上网不变 | 低(纯重构) |
| [01](stage4/01-netstack-sink.md) | go get gvisor + NetstackPacketSink | `go build ./...` 通过 + mock 测试通过 | **中(gVisor 版本锁定)** |
| [02](stage4/02-socks5-integration.md) | SOCKS5 + cmd/qmidial 集成 | **硬件:curl --socks5 HTTP 200,无需 admin** | **中(netstack dialer 包装)** |
| [03](stage4/03-dns-ipv6.md) | DNS 经 4G + IPv6 双栈 | curl ipv6 站 + DNS 经 4G 解析 | 低 |
| [04](stage4/04-udp-microbatch-docs.md) | UDP + micro-batching + 文档收尾 | dig 经 SOCKS5 + AGENTS.md 更新 | 低 |

## PacketSink 接口(核心抽象)

```go
// internal/qmidatapath/sink.go

// PacketSink is the host-side endpoint of the relay (the non-USB side).
// Implementations: TUN device (TUNPacketSink), gVisor netstack channel (NetstackPacketSink).
type PacketSink interface {
    // ReadPacket reads one raw IP packet (host → modem / uplink).
    // pkt is valid until the next ReadPacket call.
    // Returns io.EOF when the sink is closed.
    ReadPacket(ctx context.Context) (pkt []byte, err error)

    // WritePacket writes one raw IP packet (modem → host / downlink).
    // pkt is a bare IP packet (no TUN prefix, no QMAP header).
    WritePacket(pkt []byte) error

    // Name returns the sink's identifier for logging (e.g. "qmi0", "netstack").
    Name() string

    // Close releases the sink's resources.
    // Must unblock any pending ReadPacket (otherwise bridge deadlocks on Stop).
    Close() error
}
```

**两个实现**:
- `TUNPacketSink`(子计划 00):包装 wireguard/tun.Device,内部处理 offset headroom(macOS utun 4 字节前缀)
- `NetstackPacketSink`(子计划 01):gVisor channel link endpoint,raw IP 直传

## 涉及文件汇总

| 文件 | 子计划 | 改动类型 |
|---|---|---|
| `internal/qmidatapath/sink.go` | 00 | 新增 — PacketSink 接口 |
| `internal/qmidatapath/tun_sink.go` | 00 | 新增 — TUNPacketSink |
| `internal/qmidatapath/relay.go` | 00 | 改 — tunDevice→PacketSink,去 offset,ZLP 保留 |
| `internal/qmidatapath/relay_test.go` | 00 | 改 — fakeTUN→fakePacketSink |
| `cmd/qmidial/main.go` | 00/02 | 改 — New() 参数 + -socks5 flag |
| `internal/qmidatapath/netstack_sink.go` | 01 | 新增 — NetstackPacketSink |
| `internal/qmidatapath/netstack_dialer.go` | 01 | 新增 — gVisor→net.Conn dialer |
| `internal/qmidatapath/netstack_sink_test.go` | 01 | 新增 — mock 测试 |
| `internal/qmidatapath/socks5.go` | 02 | 新增 — RunSOCKS5(armon/go-socks5) |
| `internal/qmidatapath/socks5_test.go` | 02 | 新增 — SOCKS5 协议测试 |
| `go.mod` / `go.sum` | 01/02 | 加 gvisor + armon/go-socks5 |
| `AGENTS.md` | 04 | 更新 — PacketSink 措辞 + Stage 4 记录 |
| `internal/qmidatapath/AGENTS.md` | 04 | 更新 — 双后端 + Close 时序 |

## 不改动的部分(零改动)

- `internal/qmitransport/`(QMI USB transport,含 `bulkendpoints.go` 的 `OpenBulkEndpoints`)
- `third_party/quectel-qmi-go/`(QMI 协议栈 + manager + netcfg)
- `internal/usbtransport/`(AT USB transport)
- `third_party/sms-gateway/`(SMS AT 协议层)
- `-tun` flag 的外部行为(子计划 00 后功能完全等价)

## 关键风险 & 缓解

1. **gVisor 模块大、API 不保证稳定**(中等):子计划 01 第一步先 `go get` + `go build ./...` 验证无版本冲突,锁定 commit 写入 go.mod。netstack_sink.go 按 pkg.go.dev 官方 API 写,不依赖 master 分支未稳定 API。

2. **netstack dialer 包装 gVisor tcpip 连接为 net.Conn**(中等):优先用 **gVisor 自带的 `gonet` 包**(`gvisor.dev/gvisor/pkg/tcpip/adapter/gonet`)的 `DialContext` 直接返回标准 `net.Conn`,避免手写包装。子计划 01 实施时确认 gonet 可用。

3. **Phase 1 batch read 退化**(低):Windows 本就 BatchSize=1,Linux/macOS 在 4G 带宽下影响可忽略。子计划 04 micro-batching 补偿。

4. **Close 时序**(低):netstack sink 的 Close 比 TUN 更干净(channel close → ReadContext 返回 nil → goroutine 自然退出),不存在 TUN 的死锁陷阱。子计划 02 实施时验证 `bridge.Stop()` 不会挂起。

## 参考实现 & 文档

- **wireproxy**(`windtf/wireproxy`):gVisor netstack + go-socks5 的成熟项目,架构与我们高度相似,实施时可作为参考
- **gVisor netstack 官方文档**:https://gvisor.dev/docs/architecture_guide/networking/
- **gVisor channel 包**(`gvisor.dev/gvisor/pkg/tcpip/link/channel`):https://pkg.go.dev/gvisor.dev/gvisor/pkg/tcpip/link/channel
- **armon/go-socks5**:`Config.Dial` 字段是注入 gVisor dialer 的钩子(https://github.com/armon/go-socks5)
- **方案调研文档**:
  - `docs/performance/01-qcusbwwan-reverse-engineering.md` — 官方驱动逆向(韧性/异步/micro-batching 思想)
  - `docs/performance/02-tun-alternatives.md` — TUN 替代方案对比 + 多设备场景
  - `docs/performance/03-dual-backend-implementation-plan.md` — 双后端方案总览(本计划的方案来源,保留为方案文档)
