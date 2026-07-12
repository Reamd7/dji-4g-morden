# TUN + netstack 双数据后端实施计划

> 创建日期: 2026-07-13
> 前置调研: `docs/performance/02-tun-alternatives.md`
> 目标: 让 `qmidial` 支持 `-tun`(透明上网,需 admin)和 `-socks5`(SOCKS5 代理,无需 admin)两种数据后端,共享同一个 QMI 拨号链路。

## 现有架构(改动前)

```
cmd/qmidial/main.go
    ├── qmitransport.Open()          → QMITransport (MI_04 USB)
    ├── qmi.NewClientFromTransport() → QMI client
    ├── manager.NewWithClient()      → manager (StartCore + Connect)
    ├── tun.CreateTUN()              → TUN device (需 admin)
    └── qmidatapath.New(tunDev, bulkIn, bulkOut, offset, mtu, zlp)
        └── Bridge { tun tunDevice, bulkIn, bulkOut }
            ├── tunToModem:  tun.Read()  → bulkOut.Write()
            └── modemToTun:  bulkIn.Read() → tun.Write()
```

**问题**: `Bridge` 直接依赖 `tunDevice` 接口(wireguard/tun 的 batch Read/Write + offset 语义)。netstack channel 不匹配这个接口。

## 目标架构(改动后)

```
cmd/qmidial/main.go
    ├── qmitransport.Open()          → QMITransport (共用)
    ├── qmi.NewClientFromTransport() → QMI client (共用)
    ├── manager.NewWithClient()      → manager (共用)
    ├── [-tun]    tun.CreateTUN() + configureNetwork + configureDNS
    ├── [-socks5] netstack.New() + socks5.Listen()  (新增)
    └── qmidatapath.New(sink, bulkIn, bulkOut, mtu, zlp)
        └── Bridge { sink PacketSink, bulkIn, bulkOut }
            ├── sinkToModem: sink.ReadPacket() → bulkOut.Write()
            └── modemToSink: bulkIn.Read()     → sink.WritePacket()
```

**关键**: `Bridge` 不再知道 TUN/netstack 的存在。它只跟 `PacketSink` 接口对话。

## Phase 1:抽象 PacketSink 接口 + 重构现有 TUN(零功能变更)

**目标**: Bridge 从 `tunDevice` 解耦到 `PacketSink` 接口。现有行为不变,所有测试通过。

### 1.1 定义 PacketSink 接口

```go
// internal/qmidatapath/sink.go

// PacketSink is the host-side endpoint of the relay (the non-USB side).
// Implementations: TUN device, gVisor netstack channel.
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
    Close() error
}
```

### 1.2 实现 TUNPacketSink(适配现有 tunDevice)

```go
// internal/qmidatapath/tun_sink.go

// TUNPacketSink wraps a wireguard/tun.Device as a PacketSink.
// It handles the TUN-specific offset (macOS utun 4-byte prefix) internally.
type TUNPacketSink struct {
    dev    tun.Device   // 或保留 tunDevice 接口
    offset int           // macOS=4, others=0
    mtu    int
    // single-buffer reuse (Read is single-threaded in Bridge)
    buf    []byte
}

func NewTUNPacketSink(dev tunDevice, offset, mtu int) *TUNPacketSink {
    return &TUNPacketSink{
        dev:    dev,
        offset: offset,
        mtu:    mtu,
        buf:    make([]byte, mtu+offset),
    }
}

func (s *TUNPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
    bufs := [][]byte{s.buf}
    sizes := make([]int, 1)
    n, err := s.dev.Read(bufs, sizes, s.offset)
    if err != nil { return nil, err }
    if n == 0 { return nil, nil }
    return s.buf[s.offset : s.offset+sizes[0]], nil
}

func (s *TUNPacketSink) WritePacket(pkt []byte) error {
    copy(s.buf[s.offset:], pkt)
    _, err := s.dev.Write([][]byte{s.buf[:s.offset+len(pkt)]}, s.offset)
    return err
}

func (s *TUNPacketSink) Name() string {
    name, _ := s.dev.Name()
    return name
}

func (s *TUNPacketSink) Close() error {
    return s.dev.Close()
}
```

**注意**: 当前 TUN batch read (`BatchSize()`) 优化丢失了——改为单包读。4G 场景下影响可忽略(详见 Phase 3 micro-batching 优化)。

### 1.3 重构 Bridge

```go
// internal/qmidatapath/relay.go (改动)

type Bridge struct {
    sink    PacketSink    // ← 替换 tun tunDevice
    bulkIn  BulkReader
    bulkOut BulkWriter
    mtu     int
    zlp     bool
    // ... 其余不变
}

func New(sink PacketSink, bulkIn BulkReader, bulkOut BulkWriter, mtu int, zlp bool) *Bridge {
    return &Bridge{ sink: sink, bulkIn: bulkIn, bulkOut: bulkOut, mtu: mtu, zlp: zlp }
}
```

relay goroutine 改动:
```go
func (b *Bridge) sinkToModem(ctx context.Context) {
    for {
        pkt, err := b.sink.ReadPacket(ctx)   // ← 替换 b.tun.Read
        // ... ZLP 逻辑不变 ...
        b.bulkOut.Write(pkt)
    }
}

func (b *Bridge) modemToSink(ctx context.Context) {
    for {
        n, _ := b.bulkIn.ReadContext(ctx, buf)
        b.sink.WritePacket(buf[:n])          // ← 替换 b.tun.Write
    }
}
```

### 1.4 改 cmd/qmidial

```go
// Before:
bridge = qmidatapath.New(tunDev, bulkIn, bulkOut, offset, 1500, true)

// After:
sink := qmidatapath.NewTUNPacketSink(tunDev, offset, 1500)
bridge = qmidatapath.New(sink, bulkIn, bulkOut, 1500, true)
```

### 1.5 验证

- `go test -race ./internal/qmidatapath/` — 全部 mock 测试通过
- `go test -tags=hardware ./internal/qmidatapath/` — 硬件 TUN relay 不变
- `qmidial.exe -dial -tun` — Windows TUN 上网不变
- 验证 Phase 1 **零功能变更**,只是重构

### 涉及文件

| 文件 | 改动 |
|---|---|
| `internal/qmidatapath/sink.go` | **新增** — `PacketSink` 接口 |
| `internal/qmidatapath/tun_sink.go` | **新增** — `TUNPacketSink` 实现 |
| `internal/qmidatapath/relay.go` | 改 — `tunDevice` → `PacketSink`, 去掉 `offset` |
| `internal/qmidatapath/relay_test.go` | 改 — mock 改为 `PacketSink` |
| `cmd/qmidial/main.go` | 改 — 1 行 `New()` 参数 |

---

## Phase 2:netstack + SOCKS5 后端(核心新功能)

**目标**: `qmidial -dial -socks5` 跑通,浏览器/curl 通过 SOCKS5 代理经 4G 上网。无需 admin。

### 2.1 导入 gVisor netstack

```bash
mise exec -- go get gvisor.dev/gvisor@latest
```

gVisor 是大模块,但只需要 `pkg/tcpip` + `pkg/tcpip/network/ipv4` + `pkg/tcpip/transport/tcp` + `pkg/tcpip/transport/udp` + `pkg/tcpip/link/channel` + `pkg/tcpip/stack`。

### 2.2 实现 NetstackPacketSink

```go
// internal/qmidatapath/netstack_sink.go

import (
    "gvisor.dev/gvisor/pkg/tcpip"
    "gvisor.dev/gvisor/pkg/tcpip/link/channel"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/stack"
    "gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
    "gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

// NetstackPacketSink runs a gVisor userspace TCP/IP stack.
// USB bulk packets ↔ Go channel ↔ netstack ↔ SOCKS5.
type NetstackPacketSink struct {
    stk     *stack.Stack
    ep      *channel.Endpoint    // link layer = Go channel
    addr    tcpip.Address        // modem-assigned IP
    dns     tcpip.Address        // carrier DNS
    listen  string               // SOCKS5 listen addr (e.g. ":1080")
    ln      net.Listener          // SOCKS5 listener
}

func NewNetstackPacketSink(localIP netip.Addr, dns netip.Addr, listenAddr string) (*NetstackPacketSink, error) {
    // 1. Create channel link endpoint (this IS our "TUN replacement")
    ep := channel.New(256, uint32(mtu), "")
    
    // 2. Create stack with our channel as link layer
    s := stack.New(stack.Options{
        NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
        TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
    })
    
    // 3. Create NIC on our channel endpoint
    nicID := tcpip.NICID(1)
    s.CreateNIC(nicID, ep)
    s.SetRouteTable([]tcpip.Route{
        {Destination: header.IPv4EmptySubnet, NIC: nicID},  // default route → our channel
    })
    
    // 4. Set the modem-assigned IP address
    addr := tcpip.AddrFromSlice(localIP.AsSlice())
    s.AddAddress(nicID, ipv4.ProtocolNumber, addr)
    
    return &NetstackPacketSink{stk: s, ep: ep, addr: addr, listen: listenAddr}, nil
}

// ReadPacket: netstack → USB (uplink).
// Blocks until netstack emits a packet on the channel.
func (s *NetstackPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
    pkt := s.ep.ReadContext(ctx)  // channel.Endpoint.ReadContext blocks
    if pkt == nil { return nil, ctx.Err() }
    // Extract raw IP bytes from PacketBuffer
    return pkt.AsSlice(), nil
}

// WritePacket: USB → netstack (downlink).
func (s *NetstackPacketSink) WritePacket(pkt []byte) error {
    // InjectPacket delivers raw IP packet into netstack's link layer
    pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
        Payload: bufferv2.MakeData(append([]byte{}, pkt...)),
    })
    s.ep.InjectInbound(ipv4.ProtocolNumber, pkb)
    return nil
}
```

### 2.3 实现 SOCKS5 server

SOCKS5 协议极简(RFC 1928),核心 ~100 行。或用 `armon/go-socks5`(~2K 行库)。

**选择:先手写最小 SOCKS5**,只支持 CONNECT(TCP),避免外部依赖:

```go
// internal/qmidatapath/socks5.go

// SOCKS5Server accepts TCP connections and proxies them through the
// gVisor netstack (which routes them through the 4G modem).
type SOCKS5Server struct {
    stk    *stack.Stack
    ln     net.Listener
}

func (srv *SOCKS5Server) Serve(ctx context.Context, addr string) error {
    ln, err := net.Listen("tcp", addr)  // listen on localhost (host network)
    // ...
}

func (srv *SOCKS5Server) handleConn(conn net.Conn) {
    // 1. SOCKS5 handshake (no-auth, CONNECT)
    // 2. Parse target host:port
    // 3. Open TCP via gVisor netstack: s.stk.Dial(ctx, tcp, target)
    // 4. Bidirectional copy: conn ↔ netstack-tcp-conn
}
```

**关键**: SOCKS5 listener 跑在 host 网络栈(localhost:1080),但 Dial 通过 gVisor netstack → channel → USB → modem。两条网络栈通过这个 proxy 桥接。

### 2.4 集成到 cmd/qmidial

```go
socks5Mode := flag.Bool("socks5", false, "start SOCKS5 proxy via netstack (no admin needed)")
socks5Addr := flag.String("socks5-addr", ":1080", "SOCKS5 listen address")

if *socks5Mode {
    *dial = true
}

// After dial success (mgr.Settings() has IP/DNS):
if *socks5Mode {
    settings := mgr.Settings()
    sink, err := qmidatapath.NewNetstackPacketSink(
        settings.IPv4Addr, settings.DNS1, *socks5Addr,
    )
    // ... start relay + SOCKS5 server ...
}
```

### 2.5 DNS 处理

netstack 内部需要 DNS 解析。两个选项:

**选项 A(简单)**:SOCKS5 handler 里用标准 `net.LookupHost` — 走 host 网络 DNS。
- 问题:host 网络 DNS 可能解析到 host 网络可达的 IP,而非 4G 网络可达的
- 但实际影响小:大多数公网 IP 两个网络都能到达

**选项 B(正确)**:SOCKS5 handler 里用 modem DNS 手动解析。
- `AT+QIDNSGPIP=example.com` 或直接发 UDP DNS query 到 `114.114.114.114`(经 netstack → modem)
- netstack 有 `tcpip.DNSResolver`,可以配置上游 DNS

**Phase 2 选 A,Phase 3 升级 B。**

### 2.6 验证

```
# 终端 1:启动 SOCKS5 代理
qmidial.exe -dial -socks5

# 终端 2:通过 4G 代理上网
curl --socks5-hostname 127.0.0.1:1080 http://www.baidu.com
# 期望: HTTP 200

# 浏览器:设置 → SOCKS5 代理 → 127.0.0.1:1080 → 访问百度
```

- **不需要 admin**(SOCKS5 监听 localhost,USB 操作 gousb 不需要 admin)
- **不需要 wintun.dll / utun / tun**
- macOS 上也跑(`qmidial -dial -socks5`,无需 sudo)

### 涉及文件

| 文件 | 改动 |
|---|---|
| `go.mod` | 加 `gvisor.dev/gvisor` 依赖 |
| `internal/qmidatapath/netstack_sink.go` | **新增** — gVisor netstack PacketSink |
| `internal/qmidatapath/socks5.go` | **新增** — 最小 SOCKS5 server |
| `internal/qmidatapath/netstack_sink_test.go` | **新增** — mock 测试 |
| `internal/qmidatapath/socks5_test.go` | **新增** — SOCKS5 协议测试 |
| `cmd/qmidial/main.go` | 改 — 加 `-socks5` flag + netstack 分支 |

---

## Phase 3:打磨 + 多设备

### 3.1 IPv6 双栈

netstack 支持 IPv6。只需:
```go
// Stack options 加 IPv6
NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol}

// 如果 mgr.Settings() 有 IPv6 地址,加到 NIC
if settings.IPv6Addr.IsValid() {
    s.AddAddress(nicID, ipv6.ProtocolNumber, v6Addr)
}
```

### 3.2 UDP 支持

SOCKS5 支持 UDP ASSOCIATE。netstack 已有 UDP 实现。Phase 2 先只做 TCP CONNECT,Phase 3 加 UDP。

### 3.3 micro-batching(来自逆向文档 B2)

`PacketSink.ReadPacket()` 单包读可以加 micro-batching:
```go
func (b *Bridge) sinkToModem(ctx context.Context) {
    batch := make([][]byte, 0, 16)
    timer := time.NewTimer(time.Millisecond)
    for {
        select {
        case <-ctx.Done(): return
        default:
            pkt, _ := b.sink.ReadPacket(ctx)
            batch = append(batch, pkt)
            if len(batch) >= 16 { flush(batch); batch = batch[:0] }
        case <-timer.C:
            if len(batch) > 0 { flush(batch); batch = batch[:0] }
            timer.Reset(time.Millisecond)
        }
    }
}
```
减少 USB write 次数。

### 3.4 多设备支持

```go
// cmd/qmidial/main.go
serial := flag.String("serial", "", "USB serial number (empty = first device)")
// ... 或 ...
multi := flag.Bool("multi", false, "enumerate all modems, one SOCKS5 port each")

if *multi {
    devices := qmitransport.EnumerateAll()  // 按 serial 枚举
    for i, dev := range devices {
        // 独立 transport → manager → dial → netstack → SOCKS5 :1081+i
        go runModem(ctx, dev, fmt.Sprintf(":%d", 1081+i))
    }
}
```

### 3.5 负载均衡

单个 SOCKS5 端口,round-robin 分发到 N 个 netstack:
```go
type LoadBalancer struct {
    sinks []*NetstackPacketSink
    idx   atomic.Int64
}
func (lb *LoadBalancer) next() *NetstackPacketSink {
    i := lb.idx.Add(1) % int64(len(lb.sinks))
    return lb.sinks[i]
}
```

---

## 实施顺序 & 验证检查点

| 步骤 | 内容 | 验证 | 风险 |
|---|---|---|---|
| **Phase 1.1-1.5** | PacketSink 接口 + TUN 重构 | `-race` 测试通过 + 硬件 TUN 不变 | 低(纯重构) |
| **Phase 2.1** | go get gvisor | `go build ./...` 通过 | 中(gVisor 模块大,可能有版本冲突) |
| **Phase 2.2** | NetstackPacketSink | mock 测试:channel.inject → ReadPacket | 低 |
| **Phase 2.3** | SOCKS5 server | 单元测试:handshake + CONNECT | 低 |
| **Phase 2.4** | cmd/qmidial 集成 | **硬件测试:curl --socks5** | **中(netstack API 细节)** |
| **Phase 2.5** | DNS | curl 域名解析成功 | 低(先用 host DNS) |
| **Phase 3.x** | IPv6 / UDP / multi | 按需 | 低 |

**关键风险**: gVisor netstack API 不保证稳定。Phase 2.1 需要确定一个具体的 gVisor commit/version 并锁定。如果 API 变化,netstack_sink.go 需要相应调整。

## 不改动的部分

以下代码**完全不变**:
- `internal/qmitransport/` — QMI USB transport(MI_04 claim + DTR + SYNC + bulk endpoints)
- `third_party/quectel-qmi-go/` — QMI 协议栈 + manager + netcfg
- `internal/usbtransport/` — AT USB transport(MI_02)
- `third_party/sms-gateway/` — SMS AT 协议层
- Phase 1 的 TUN 模式(`-tun` flag 行为不变)

**数据路径从 QMI transport 到 bulk endpoints 完全共享**,只有最后一跳(TUN vs netstack channel)不同。
