# 子计划 01 — gVisor netstack 依赖 + NetstackPacketSink

> 隶属 `plans/stage4-dual-backend.md`(总览)。**Stage 4 netstack 后端核心**。
> 依赖子计划 00 通过(PacketSink 接口已就位,TUN 重构已回归验证)。
> 后续子计划 02(SOCKS5 集成)依赖本计划的 NetstackPacketSink + netstack dialer。

---

## 一、目标

实现 gVisor netstack 的 PacketSink,把 USB bulk EP 上的 raw IP 包接到一个**纯 Go 用户态 TCP/IP 栈**。netstack 的 link layer 用 Go channel(不是 TUN),这样:
- 无需 admin、无需 TUN 设备、无需改路由表
- 纯 Go,零平台特异代码
- 后续子计划 02 在 netstack 上开 SOCKS5 server,App 通过代理上网

同时实现一个 **netstack dialer**(把 gVisor 的 tcpip 连接包装成标准 `net.Conn`),供子计划 02 的 armon/go-socks5 `Config.Dial` 使用。

---

## 二、依赖

- **子计划 00 通过**:PacketSink 接口已定义
- **新增 Go 依赖**:`gvisor.dev/gvisor`(大模块,只需要 `pkg/tcpip` 子树)

```bash
mise exec -- go get gvisor.dev/gvisor@latest
```

### 2.1 版本锁定策略(关键风险缓解)

gVisor 不保证 module 版本稳定,API 可能在 minor 版本变化。实施步骤:

1. **先验证 build**:`go get` 后立即 `mise exec -- go build ./...`,确认无版本冲突
2. **锁定 commit**:`go.mod` 里把 `gvisor.dev/gvisor` 钉到具体 commit(用 `go get gvisor.dev/gvisor@<commit>`),而不是 `@latest`
3. **记录 commit**:在 `internal/qmidatapath/AGENTS.md` 记录锁定的 gVisor 版本/commit,方便后续升级时对照
4. **只用稳定子包**:`pkg/tcpip/link/channel`、`pkg/tcpip/stack`、`pkg/tcpip/network/ipv4`、`pkg/tcpip/transport/tcp`、`pkg/tcpip/transport/udp`、`pkg/tcpip/adapter/gonet`。不用 master 分支未稳定 API
5. **API smoke test(写实现代码前先做)**:写一个 50 行的 `netstack_api_smoke_test.go`,验证锁定版本的关键 API 签名:
   ```go
   //go:build !hardware
   func TestNetstackAPISmoke(t *testing.T) {
       // 验证 channel.New / ReadContext / InjectInbound / Close 签名
       ep := channel.New(64, 1500, "")
       ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
       defer cancel()
       pkt := ep.ReadContext(ctx)  // 超时返回 nil
       // 验证可以 InjectInbound
       // 验证 ep.Close() 不 panic
       // 验证 gonet.DialContextTCP 签名
   }
   ```
   如果任何 API 签名与 §三 的假设不符,**先调整 §三 的代码模板再写实现**。避免写完 200 行才发现 API 变了。

---

## 三、gVisor netstack API 关键点(已确认 pkg.go.dev)

```go
import (
    "gvisor.dev/gvisor/pkg/tcpip"
    "gvisor.dev/gvisor/pkg/tcpip/link/channel"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/stack"
    "gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
    "gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

// Link layer = Go channel(不是 TUN!这是 netstack 方案的基石)
ep := channel.New(size int, mtu uint32, linkAddr tcpip.LinkAddress) *channel.Endpoint

// netstack TCP/IP 栈
s := stack.New(stack.Options{
    NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
    TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
})

// 把 channel endpoint 注册为 NIC
s.CreateNIC(nicID tcpip.NICID, ep stack.LinkEndpoint) tcpip.Error
s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})
s.AddAddress(nicID, ipv4.ProtocolNumber, addr tcpip.Address) tcpip.Error

// 下行(USB → netstack):把 raw IP 包注入 netstack 的入站
pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
    Payload: bufferv2.MakeData(append([]byte{}, pkt...)),
})
ep.InjectInbound(ipv4.ProtocolNumber, pkb)

// 上行(netstack → USB):阻塞等 netstack 吐包(ctx cancel → nil)
pkt := ep.ReadContext(ctx context.Context) *stack.PacketBuffer
if pkt == nil { return nil, ctx.Err() }
rawIP := pkt.AsSlice()

// 关闭(channel close 让 ReadContext 立即返回 nil,goroutine 自然退出)
ep.Close()
```

**对比 TUN 的关键优势**:`channel.Endpoint.ReadContext(ctx)` 支持 context 取消,channel close 立即返回 nil。TUN 的 `Read` 不响应 context,必须靠 Close TUN 解除阻塞。**netstack sink 不存在 TUN 的死锁陷阱**。

---

## 四、实现

### 4.1 新增 `internal/qmidatapath/netstack_sink.go` — NetstackPacketSink

```go
package qmidatapath

import (
    "context"
    "fmt"
    "net/netip"

    "gvisor.dev/gvisor/pkg/bufferv2"
    "gvisor.dev/gvisor/pkg/tcpip"
    "gvisor.dev/gvisor/pkg/tcpip/header"
    "gvisor.dev/gvisor/pkg/tcpip/link/channel"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/stack"
    "gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
    "gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

// NetstackPacketSink runs a gVisor userspace TCP/IP stack.
// USB bulk packets ↔ Go channel ↔ netstack ↔ (SOCKS5 in 子计划 02).
//
// The link layer is a Go channel (channel.Endpoint), NOT a TUN device.
// This means: no admin privileges, no platform-specific code, no routing
// changes — pure Go userspace networking.
type NetstackPacketSink struct {
    stk    *stack.Stack
    ep     *channel.Endpoint
    nicID  tcpip.NICID
    addr   tcpip.Address
    mtu    uint32
}

// NewNetstackPacketSink creates a netstack-backed PacketSink.
// localIP: modem-assigned IPv4 (from mgr.Settings().IPv4Address)
// mtu: typically 1500 (from mgr.Settings().MTU)
func NewNetstackPacketSink(localIP netip.Addr, mtu int) (*NetstackPacketSink, error) {
    if !localIP.IsValid() {
        return nil, fmt.Errorf("netstack: invalid local IP")
    }

    // 1. Channel link endpoint (this IS our "TUN replacement")
    //    size=256: packet buffer depth (enough for burst, bounded memory)
    const channelSize = 256
    ep := channel.New(channelSize, uint32(mtu), "")

    // 2. Create stack with our channel as link layer
    s := stack.New(stack.Options{
        NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
        TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
    })

    // 3. Create NIC on our channel endpoint
    nicID := tcpip.NICID(1)
    if err := s.CreateNIC(nicID, ep); err != nil {
        return nil, fmt.Errorf("netstack: CreateNIC: %v", err)
    }

    // 4. Default route → our channel (all outbound traffic goes to USB)
    s.SetRouteTable([]tcpip.Route{
        {Destination: header.IPv4EmptySubnet, NIC: nicID},
    })

    // 5. Set the modem-assigned IP address
    addr := tcpip.AddrFromSlice(localIP.AsSlice())
    if err := s.AddAddress(nicID, ipv4.ProtocolNumber, addr); err != nil {
        return nil, fmt.Errorf("netstack: AddAddress: %v", err)
    }

    return &NetstackPacketSink{
        stk:   s,
        ep:    ep,
        nicID: nicID,
        addr:  addr,
        mtu:   uint32(mtu),
    }, nil
}

// Stack returns the underlying gVisor stack (供子计划 02 的 dialer / SOCKS5 使用).
func (s *NetstackPacketSink) Stack() *stack.Stack {
    return s.stk
}

// ReadPacket: netstack → USB (uplink).
// Blocks until netstack emits a packet on the channel, or ctx is canceled.
// netstack emits a packet when an app (e.g. SOCKS5 → netstack.Dial) sends data.
func (s *NetstackPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
    pkt := s.ep.ReadContext(ctx)
    if pkt == nil {
        // ctx canceled or channel closed
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        return nil, fmt.Errorf("netstack: channel closed")
    }
    defer pkt.DecRef()
    // Extract raw IP bytes from PacketBuffer
    return pkt.AsSlice(), nil
}

// WritePacket: USB → netstack (downlink).
// Injects a raw IP packet (from modem) into netstack's inbound path.
// netstack's TCP/UDP stack will then deliver payload to the app
// (e.g. SOCKS5 handler that dialed the destination).
func (s *NetstackPacketSink) WritePacket(pkt []byte) error {
    if len(pkt) == 0 {
        return nil
    }
    // Copy into a fresh buffer — gVisor PacketBuffer owns its payload
    buf := bufferv2.MakeData(append([]byte(nil), pkt...))
    pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
        Payload: buf,
    })
    defer pkb.DecRef()
    s.ep.InjectInbound(ipv4.ProtocolNumber, pkb)
    return nil
}

func (s *NetstackPacketSink) Name() string {
    return "netstack"
}

// Close releases netstack resources.
// ep.Close() makes pending ReadContext return nil → ReadPacket returns error
// → Bridge's sinkToModem goroutine exits naturally. No deadlock (unlike TUN).
func (s *NetstackPacketSink) Close() error {
    s.ep.Close()
    s.stk.Close()
    return nil
}
```

### 4.2 新增 `internal/qmidatapath/netstack_dialer.go` — gVisor → net.Conn dialer

把 gVisor netstack 包装成标准 `func(ctx, network, addr) (net.Conn, error)`,供子计划 02 的 `armon/go-socks5.Config.Dial` 使用。

**优先用 gVisor 自带的 `gonet` 包**(避免手写包装):

```go
package qmidatapath

import (
    "context"
    "fmt"
    "net"

    "gvisor.dev/gvisor/pkg/tcpip"
    "gvisor.dev/gvisor/pkg/tcpip/adapter/gonet"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

// NetstackDialer returns a Dial function suitable for armon/go-socks5's
// Config.Dial. Connections dialed through this function go through the
// gVisor netstack → channel → USB → modem (NOT the host network).
func (s *NetstackPacketSink) NetstackDialer() func(ctx context.Context, network, addr string) (net.Conn, error) {
    return func(ctx context.Context, network, addr string) (net.Conn, error) {
        host, port, err := net.SplitHostPort(addr)
        if err != nil {
            return nil, fmt.Errorf("netstack dialer: bad addr %q: %w", addr, err)
        }
        // Note: DNS resolution happens here. Phase 2 (子计划 02) uses host DNS;
        // 子计划 03 升级 to 4G DNS (netstack internal resolver or UDP query).
        ip, err := resolveForNetstack(ctx, host)  // helper, see below
        if err != nil {
            return nil, fmt.Errorf("netstack dialer: resolve %q: %w", host, err)
        }
        portNum, err := net.LookupPort(ctx, network, port)
        if err != nil {
            return nil, fmt.Errorf("netstack dialer: bad port %q: %w", port, err)
        }

        fullAddr := tcpip.FullAddress{
            NIC:  s.nicID,
            Addr: tcpip.AddrFromSlice(ip.AsSlice()),
            Port: uint16(portNum),
        }

        // gonet.DialContext returns a standard net.Conn backed by gVisor netstack
        // — traffic flows: app → gonet → netstack TCP → channel → USB → modem
        switch network {
        case "tcp", "tcp4":
            return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv4.ProtocolNumber)
        // UDP 在子计划 04 启用(SOCKS5 UDP ASSOCIATE)
        default:
            return nil, fmt.Errorf("netstack dialer: unsupported network %q", network)
        }
    }
}

// resolveForNetstack resolves a hostname to an IP.
// Phase 2: use host DNS (simple, works for most public sites).
// 子计划 03: upgrade to 4G DNS via netstack internal resolver.
func resolveForNetstack(ctx context.Context, host string) (netip.Addr, error) {
    // For Phase 2, use Go's standard resolver (host network).
    // This is intentional — see 子计划 02 §DNS for rationale.
    ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
    if err != nil {
        return netip.Addr{}, err
    }
    if len(ips) == 0 {
        return netip.Addr{}, fmt.Errorf("no IP for %s", host)
    }
    return netip.AddrFromSlice(ips[0])
}
```

> **注意**:`gonet.DialContextTCP` 的确切签名可能因 gVisor 版本略有差异(有时是 `gonet.DialTCP` 返回 `*gonet.TCPConn` 或 `net.Conn`)。实施时按锁定的 gVisor 版本 `pkg.go.dev` 文档为准。如果 gonet 包 API 不稳定,**降级方案**:手写一个最小 `gonetConn` 包装 gVisor `tcp.Endpoint`,实现 `net.Conn` 接口(Read/Write/Close/SetDeadline)。参考 wireproxy 项目的实现。

### 4.3 新增 `internal/qmidatapath/netstack_sink_test.go` — mock 测试

测试不依赖真实 USB,验证 NetstackPacketSink 的 IP 包收发逻辑:

```go
//go:build !hardware

package qmidatapath

import (
    "context"
    "net/netip"
    "testing"
    "time"
)

func TestNetstackSinkUpRoundTrip(t *testing.T) {
    // 上行(netstack → channel):在 netstack 内部创建一个 TCP listener,
    // 通过 listener 发包,ReadPacket 应能读出 IP 包
    sink, err := NewNetstackPacketSink(netip.MustParseAddr("10.0.0.1"), 1500)
    if err != nil { t.Fatal(err) }
    defer sink.Close()

    // ... 用 gonet.ListenTCP 起 listener,客户端 dial,验证 ReadPacket 读到包 ...
}

func TestNetstackSinkDownInject(t *testing.T) {
    // 下行(channel → netstack):WritePacket 注入一个 IP 包,
    // 验证 netstack 内部的 listener/socket 能收到
    sink, err := NewNetstackPacketSink(netip.MustParseAddr("10.0.0.1"), 1500)
    if err != nil { t.Fatal(err) }
    defer sink.Close()

    // ... 构造一个发往 10.0.0.1 的 TCP SYN,WritePacket,验证 listener Accept ...
}

func TestNetstackSinkReadPacketContextCancel(t *testing.T) {
    // ctx 取消 → ReadPacket 立即返回(不死锁,这是 netstack 优于 TUN 的点)
    sink, _ := NewNetstackPacketSink(netip.MustParseAddr("10.0.0.1"), 1500)
    defer sink.Close()

    ctx, cancel := context.WithCancel(context.Background())
    go func() { time.Sleep(50 * time.Millisecond); cancel() }()

    _, err := sink.ReadPacket(ctx)
    if err == nil { t.Fatal("expected error on canceled ctx") }
}

func TestNetstackSinkCloseUnblocksRead(t *testing.T) {
    // Close → pending ReadPacket 立即返回(防死锁)
    sink, _ := NewNetstackPacketSink(netip.MustParseAddr("10.0.0.1"), 1500)

    done := make(chan struct{})
    go func() {
        defer close(done)
        sink.ReadPacket(context.Background())
    }()

    time.Sleep(50 * time.Millisecond)
    sink.Close()

    select {
    case <-done:
        // OK
    case <-time.After(time.Second):
        t.Fatal("ReadPacket did not unblock after Close")
    }
}
```

> **测试细节**:netstack 内部 TCP/UDP 收发需要构造完整的 IP/TCP 包,或用 gonet 走真实握手。优先用 gonet 走真实握手(更接近真实场景,不需要手搓 TCP 状态机)。参考 wireproxy 的测试。

---

## 五、验证

```bash
# 1. 先验证 gVisor 依赖能 build(关键风险点)
mise exec -- go get gvisor.dev/gvisor@latest
mise exec -- go build ./...
# 失败则尝试锁定较旧 commit:
#   mise exec -- go get gvisor.dev/gvisor@<older-commit>

# 2. 锁定版本后,记录到 go.mod
mise exec -- go mod tidy

# 3. mock 测试(netstack_sink_test.go,无需硬件)
mise exec -- go test -race -v ./internal/qmidatapath/ -run "TestNetstackSink"

# 4. race 检测(并发代码硬性要求)
mise exec -- go test -race ./internal/qmidatapath/
```

**子计划 01 通过判据**:
- `go build ./...` 通过(gVisor 依赖无冲突)
- `TestNetstackSinkUpRoundTrip` 通过(netstack 能发包,ReadPacket 能读出)
- `TestNetstackSinkDownInject` 通过(WritePacket 注入,netstack 能收到)
- `TestNetstackSinkReadPacketContextCancel` 通过(ctx 取消不死锁)
- `TestNetstackSinkCloseUnblocksRead` 通过(Close 解除 Read 阻塞)
- go.mod 锁定的 gVisor commit 记录在 `internal/qmidatapath/AGENTS.md`

---

## 六、涉及文件

| 文件 | 改动 |
|---|---|
| `go.mod` / `go.sum` | 加 `gvisor.dev/gvisor`(锁定 commit) |
| `internal/qmidatapath/netstack_sink.go` | **新增** — NetstackPacketSink |
| `internal/qmidatapath/netstack_dialer.go` | **新增** — gVisor→net.Conn dialer(gonet 包装) |
| `internal/qmidatapath/netstack_sink_test.go` | **新增** — 4 个 mock 测试 |
| `internal/qmidatapath/AGENTS.md` | 改 — 记录 gVisor 锁定版本 |

---

## 七、风险 & 缓解

### R1:gVisor 模块大、版本冲突(中等)

`gVisor` 是大型 monorepo,可能与现有依赖(`golang.org/x/net`、`golang.org/x/sys` 等)有间接版本冲突。

**缓解**:
- 先 `go get` + `go build ./...` 验证
- 冲突时尝试较旧 commit(避免 master 最新)
- go.mod 必须锁定具体 commit,不用 `@latest`

### R2:gonet API 不稳定(低-中)

`gonet.DialContextTCP` / `gonet.DialTCP` 签名在不同 gVisor 版本有差异。

**缓解**:
- 按锁定的 gVisor 版本 `pkg.go.dev` 文档为准
- 降级方案:手写 `gonetConn` 包装 gVisor `tcp.Endpoint`(参考 wireproxy)

### R3:netstack 内部测试构造复杂(低)

netstack 内部 TCP 收发需要走真实握手,不能像 fakeBulkReader 那样简单塞包。

**缓解**:用 gonet.ListenTCP + gonet.DialContextTCP 走真实握手,参考 wireproxy 测试。测试代码会比 relay_test.go 长,但更接近真实场景。

---

## 八、与 TUN 的对比(验证后记入 AGENTS.md)

| 维度 | TUNPacketSink | NetstackPacketSink |
|---|---|---|
| 需要 admin | ✅(创建 TUN) | ❌ |
| 平台特异代码 | ✅(offset/Wintun/utun) | ❌(纯 Go) |
| Read 响应 ctx | ❌(死锁陷阱) | ✅(channel.ReadContext) |
| Close 解除 Read | 需 tunDev.Close() | ep.Close() 自动 |
| TCP/IP 栈 | 内核 | 用户态(netstack) |
| 性能 | 内核 TCP | 用户态 TCP(重传/拥塞控制开销) |
| 透明度 | ✅(系统级) | ❌(需 SOCKS5/子计划 02) |

**子计划 01 完成后**:netstack sink 可用,但还无法让 App 上网(没有入口)。子计划 02 加 SOCKS5 server 提供入口。
