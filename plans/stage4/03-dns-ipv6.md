# 子计划 03 — DNS 经 4G + IPv6 双栈

> 隶属 `plans/stage4-dual-backend.md`(总览)。**Stage 4 打磨(正确性)**。
> 依赖子计划 02 通过(SOCKS5 已能跑通,但 DNS 走 host、仅 IPv4)。
>
> **目标**:DNS 解析完全走 4G(不再依赖 host DNS),IPv6 双栈(netstack 支持 IPv6 路由)。

---

## 一、目标

子计划 02 的两个已知限制:
1. **DNS 走 host 网络**(选项 A):`resolveForNetstack` 用 `net.DefaultResolver`,不消耗 4G 流量,且可能解析到 4G 不可达的 IP
2. **仅 IPv4**:NetstackPacketSink 构造时只加了 ipv4 协议 + ipv4 地址

本计划修复这两个限制,让 netstack 后端达到"DNS + 双栈全部走 4G"的正确性。

---

## 二、依赖

- **子计划 02 通过**:SOCKS5 + cmd/qmidial 已跑通
- **无新增依赖**(gVisor 的 ipv6 + DNS 已在子计划 01 导入)

---

## 三、实现

### 3.1 DNS 升级(选项 B:DNS 走 4G)

netstack 内部需要 DNS 解析。两个实现路径,优先试 (a):

#### (a) gVisor 内置 DNS Resolver(优先)

gVisor netstack 有内置 DNS resolver,可配置上游 DNS:

```go
// internal/qmidatapath/netstack_sink.go (扩展 NewNetstackPacketSink)

import (
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
)

func NewNetstackPacketSinkWithDNS(localIP netip.Addr, mtu int, dnsServers []netip.Addr) (*NetstackPacketSink, error) {
    // ... 同子计划 01 的 stack/channel/NIC 创建 ...

    // 配置 DNS resolver:把 modem DNS 设为上游
    // gVisor 的 DNS 配置方式(按锁定版本 pkg.go.dev 为准):
    //   - stack.SetRouteTable 加 DNS 路由,或
    //   - 用 tcpip.DNSResolver / stack DNS 配置 API
    for _, dns := range dnsServers {
        // 把 DNS 服务器注册到 netstack 的 resolver
        // (具体 API 名按锁定 gVisor 版本,可能是 stk.SetDNSQueryInterval 等)
        _ = dns
    }

    return sink, nil
}
```

**风险**:gVisor 的 DNS 配置 API 在不同版本差异较大。实施时按锁定版本 `pkg.go.dev/gvisor.dev/gvisor/pkg/tcpip` 文档为准。

#### (b) 手动 UDP DNS query 经 netstack(降级方案)

如果 (a) 的 API 不稳定,**手写** DNS 解析:netstack dialer 收到域名时,先发一个 UDP DNS query 到 modem DNS(`settings.IPv4DNS1`),query 经 netstack → channel → USB → modem → 4G → 运营商 DNS,拿到 IP 后再 dial。

```go
// internal/qmidatapath/netstack_dialer.go (扩展 resolveForNetstack)

func (s *NetstackPacketSink) resolveVia4G(ctx context.Context, host string) (netip.Addr, error) {
    // 用 gonet.DialContextUDP 发 DNS query 到 s.dnsServers[0]
    // 构造最小 DNS query (RFC 1035):
    //   Header: ID=0x1234, flags=0x0100 (standard query, recursion desired)
    //   Question: QNAME=host, QTYPE=A, QCLASS=IN
    // 解析 response 的 Answer section,提取 A record
    // ... (约 50 行)
}

// resolveForNetstack 升级为:
func (s *NetstackPacketSink) resolveForNetstack(ctx context.Context, host string) (netip.Addr, error) {
    if s.dnsResolver == "4g" {
        return s.resolveVia4G(ctx, host)
    }
    // fallback: host DNS
    return resolveViaHost(ctx, host)
}
```

**好处**:完全不依赖 gVisor DNS API,可控性高。DNS query 走真实 4G(可观测、可统计)。

### 3.2 cmd/qmidial 传入 DNS

```go
// cmd/qmidial/main.go (socks5 分支)
if *socks5Mode {
    s := mgr.Settings()
    dnsServers := []netip.Addr{}
    if len(s.IPv4DNS1) > 0 {
        dnsServers = append(dnsServers, netip.Addr(s.IPv4DNS1))
    }
    if len(s.IPv4DNS2) > 0 {
        dnsServers = append(dnsServers, netip.Addr(s.IPv4DNS2))
    }
    sink, err = qmidatapath.NewNetstackPacketSinkWithDNS(
        netip.Addr(s.IPv4Address), s.MTU, dnsServers)
}
```

### 3.3 IPv6 双栈

```go
// internal/qmidatapath/netstack_sink.go (扩展)

import (
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
    "gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
    "gvisor.dev/gvisor/pkg/tcpip/header"
)

func NewNetstackPacketSinkDualStack(
    localIP4, localIP6 netip.Addr, mtu int, dnsServers []netip.Addr,
) (*NetstackPacketSink, error) {
    ep := channel.New(channelSize, uint32(mtu), "")

    // 双栈:加 ipv4 + ipv6
    s := stack.New(stack.Options{
        NetworkProtocols: []stack.NetworkProtocolFactory{
            ipv4.NewProtocol,
            ipv6.NewProtocol,  // ← 新增
        },
        TransportProtocols: []stack.TransportProtocolFactory{
            tcp.NewProtocol, udp.NewProtocol,
        },
    })

    nicID := tcpip.NICID(1)
    s.CreateNIC(nicID, ep)

    // 默认路由:ipv4 + ipv6 都走我们的 channel
    s.SetRouteTable([]tcpip.Route{
        {Destination: header.IPv4EmptySubnet, NIC: nicID},
        {Destination: header.IPv6EmptySubnet, NIC: nicID},  // ← 新增
    })

    // IPv4 地址(modem 分配)
    if localIP4.IsValid() {
        addr4 := tcpip.AddrFromSlice(localIP4.AsSlice())
        s.AddAddress(nicID, ipv4.ProtocolNumber, addr4)
    }

    // IPv6 地址(modem 分配,如果运营商给了)
    if localIP6.IsValid() {
        addr6 := tcpip.AddrFromSlice(localIP6.AsSlice())
        s.AddAddress(nicID, ipv6.ProtocolNumber, addr6)
    }

    return &NetstackPacketSink{stk: s, ep: ep, /* ... */}, nil
}
```

**netstack_dialer.go 也要支持 IPv6**:

```go
func (s *NetstackPacketSink) NetstackDialer() func(ctx, network, addr string) (net.Conn, error) {
    return func(ctx context.Context, network, addr string) (net.Conn, error) {
        host, port, _ := net.SplitHostPort(addr)
        ip, _ := resolveForNetstack(ctx, host)  // 可能返回 v4 或 v6
        fullAddr := tcpip.FullAddress{
            NIC:  s.nicID,
            Addr: tcpip.AddrFromSlice(ip.AsSlice()),
            Port: uint16(portNum),
        }
        switch network {
        case "tcp", "tcp4":
            return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv4.ProtocolNumber)
        case "tcp6":
            return gonet.DialContextTCP(ctx, s.stk, fullAddr, ipv6.ProtocolNumber)
        }
    }
}
```

### 3.4 WritePacket 支持 IPv6(无需改动!)

raw-IP 直传:`WritePacket` 的 `InjectInbound` 已经按 IP version 分发。只需把硬编码的 `ipv4.ProtocolNumber` 改为按首字节判断:

```go
func (s *NetstackPacketSink) WritePacket(pkt []byte) error {
    if len(pkt) == 0 { return nil }
    // 按首字节判断 IP 版本:0x4x = IPv4, 0x6x = IPv6
    protoNum := ipv4.ProtocolNumber
    if pkt[0]>>4 == 6 {
        protoNum = ipv6.ProtocolNumber
    }
    pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
        Payload: bufferv2.MakeData(append([]byte(nil), pkt...)),
    })
    defer pkb.DecRef()
    s.ep.InjectInbound(protoNum, pkb)
    return nil
}
```

### 3.5 cmd/qmidial 传入 IPv6

```go
// cmd/qmidial/main.go
if *socks5Mode {
    s := mgr.Settings()
    ip4 := netip.Addr(s.IPv4Address)
    var ip6 netip.Addr
    if h6 := mgr.HandleV6(); h6 != 0 {
        if s6 := mgr.SettingsV6(); s6 != nil && len(s6.IPv6Address) > 0 {
            ip6 = netip.Addr(s6.IPv6Address)
        }
    }
    sink, err = qmidatapath.NewNetstackPacketSinkDualStack(ip4, ip6, s.MTU, dnsServers)
}
```

---

## 四、验证

```bash
# 1. DNS 走 4G 验证
# 终端 1:启动
./qmidial.exe -dial -socks5
# 终端 2:用 4G DNS 解析
curl --socks5-hostname 127.0.0.1:1080 -s "http://[dns-server-ip]/dns-query?name=baidu.com"
# 或更简单:观察 cmd/qmidial 日志,确认 DNS query 经过 relay(看 relay stats 的 TX/RX 增量)
nslookup baidu.com 114.114.114.114  # 经 host 直连,作为对照

# 2. IPv6 双栈验证
curl --socks5-hostname 127.0.0.1:1080 -6 -s https://ipv6.google.com -o /dev/null -w "%{http_code}\n"
# 或用 IPv6-only 测试站
curl --socks5-hostname 127.0.0.1:1080 -s https://test-ipv6.com -o /dev/null -w "%{http_code}\n"

# 3. mock 测试
mise exec -- go test -race -v ./internal/qmidatapath/ -run "TestNetstackSink.*IPv6|TestDNS"
```

**子计划 03 通过判据**:
- DNS query 经过 relay(观察 relay stats,或抓包确认 DNS 流量走 4G)
- IPv6 站点可达(curl -6 经 SOCKS5)
- IPv4 回归不破坏(curl 默认仍能用)

---

## 五、涉及文件

| 文件 | 改动 |
|---|---|
| `internal/qmidatapath/netstack_sink.go` | 改 — ipv6 双栈 + DNS 配置 + WritePacket 按版本分发 |
| `internal/qmidatapath/netstack_dialer.go` | 改 — DNS 升级(选项 a 或 b)+ IPv6 dial |
| `internal/qmidatapath/netstack_sink_test.go` | 改 — 加 IPv6 + DNS 测试 |
| `cmd/qmidial/main.go` | 改 — 传入 IPv6 地址 + DNS 服务器 |

---

## 六、风险 & 缓解

### R1:gVisor DNS API 不稳定(中等)

gVisor 的 DNS resolver 配置 API 在不同版本差异大。

**缓解**:优先用降级方案 (b) 手写 UDP DNS query —— 不依赖 gVisor DNS API,可控性高,且 DNS query 可观测(经 relay)。

### R2:运营商 IPv6 可能不稳定(低)

4G 运营商的 IPv6 分配/PDH 在 QDC507 上虽有(子计划 02 已验证 `HandleV6()` 非零),但实际 IPv6 路由可能不通。

**缓解**:IPv6 失败不影响 IPv4(双栈独立)。实施时先验证 IPv4 不破坏,再验证 IPv6;IPv6 不通则记录为已知限制,不阻塞 Stage 4 完成。

### R3:DNS 手写 query 的兼容性(低)

DNS 协议(RFC 1035)成熟,最小 query ~30 行可构造。但解析 response 需要处理压缩指针、多 record 等。

**缓解**:用现成 DNS 库(如 `miekg/dns`)构造 query + 解析 response,只把 transport(gonet UDP)换成经 netstack。避免手搓 DNS 协议。
