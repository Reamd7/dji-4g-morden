# 子计划 02 — SOCKS5 server + cmd/qmidial 集成

> 隶属 `plans/stage4-dual-backend.md`(总览)。**Stage 4 核心新功能落地**。
> 依赖子计划 00(PacketSink)+ 子计划 01(NetstackPacketSink + dialer)通过。
>
> **目标**:`qmidial -dial -socks5` 跑通,浏览器/curl 通过 SOCKS5 代理经 4G 上网。**无需 admin、无需 wintun.dll/utun、macOS 无需 sudo**。

---

## 一、目标

在子计划 01 的 NetstackPacketSink 之上,挂一个 SOCKS5 server。App(curl/浏览器)把 SOCKS5 代理指向 `127.0.0.1:1080`,流量即经 gVisor netstack → channel → USB → modem → 4G 上网。

**关键设计**:SOCKS5 listener 跑在 **host 网络栈**(localhost:1080),但 `Config.Dial` 注入 gVisor netstack dialer,所以实际连接走 4G。两条网络栈通过 SOCKS5 proxy 桥接。

---

## 二、依赖

- **子计划 00 通过**:PacketSink 接口、Bridge 已解耦
- **子计划 01 通过**:NetstackPacketSink、NetstackDialer() 已就位
- **新增 Go 依赖**:`github.com/armon/go-socks5`

```bash
mise exec -- go get github.com/armon/go-socks5
```

### 2.1 armon/go-socks5 的关键钩子(已确认)

`Config.Dial` 字段是注入自定义网络栈的入口:

```go
// armon/go-socks5 源码
type Config struct {
    AuthMethods []Authenticator
    Rules       RuleSet
    Logger      *log.Logger
    // Optional function for dialing out.
    // nil 时 fallback 到 net.Dial(走 host 网络)。
    // 设为我们的 NetstackDialer() → 所有 CONNECT 请求走 4G。
    Dial        func(ctx context.Context, network, addr string) (net.Conn, error)
}
```

**参考实现**:`wireproxy` 项目用同样的 `gVisor netstack + go-socks5.Config.Dial` 组合暴露 WireGuard SOCKS5 代理。我们的架构与它高度相似,实施时可参考其 [socks5 集成代码](https://github.com/windtf/wireproxy)。

---

## 三、实现

### 3.1 新增 `internal/qmidatapath/socks5.go` — RunSOCKS5

```go
package qmidatapath

import (
    "context"
    "fmt"
    "net"

    "github.com/armon/go-socks5"
)

// RunSOCKS5 starts a SOCKS5 server on listenAddr (host network, e.g. "127.0.0.1:1080").
// Outbound connections are dialed through the NetstackPacketSink's dialer
// (gVisor netstack → channel → USB → modem), NOT the host network.
//
// Blocks until ctx is canceled or the listener errors.
func RunSOCKS5(ctx context.Context, sink *NetstackPacketSink, listenAddr string) error {
    conf := &socks5.Config{
        Dial: sink.NetstackDialer(),  // ← 关键:所有 CONNECT 走 4G
        // Logger: ... (可选,接 zerolog/logrus)
    }
    server, err := socks5.New(conf)
    if err != nil {
        return fmt.Errorf("socks5: New: %w", err)
    }

    // Custom listener so we can honor ctx cancellation
    lc := net.ListenConfig{}
    ln, err := lc.Listen(ctx, "tcp", listenAddr)
    if err != nil {
        return fmt.Errorf("socks5: listen %s: %w", listenAddr, err)
    }
    defer ln.Close()

    errCh := make(chan error, 1)
    go func() {
        errCh <- server.Serve(ln)
    }()

    select {
    case <-ctx.Done():
        ln.Close()  // unblock Serve
        <-errCh
        return ctx.Err()
    case err := <-errCh:
        return err
    }
}
```

### 3.2 新增 `internal/qmidatapath/socks5_test.go` — 协议测试

用一个 **mock dialer** + 本地 TCP echo server 验证 SOCKS5 握手 + 双向转发(不需要真实 USB):

```go
//go:build !hardware

package qmidatapath

import (
    "context"
    "io"
    "net"
    "testing"
    "time"
)

// TestSOCKS5ConnectHandshake verifies the SOCKS5 CONNECT flow:
//   1. Client connects to SOCKS5 listener (host network)
//   2. Handshake (no-auth)
//   3. CONNECT request to echo server
//   4. Bidirectional copy via the mock dialer
func TestSOCKS5ConnectHandshake(t *testing.T) {
    // 1. Start an echo server on host network (simulates the "destination")
    echoLn, _ := net.Listen("tcp", "127.0.0.1:0")
    defer echoLn.Close()
    echoAddr := echoLn.Addr().String()
    go func() {
        c, _ := echoLn.Accept()
        if c != nil { io.Copy(c, c); c.Close() }  // echo
    }()

    // 2. Mock dialer: routes to echo server (in real usage, this goes to gVisor)
    mockDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
        return net.Dial(network, echoAddr)  // always dial echo (ignore target)
    }

    // 3. Start SOCKS5 server with mock dialer
    conf := &socks5.Config{Dial: mockDial}
    server, _ := socks5.New(conf)
    socksLn, _ := net.Listen("tcp", "127.0.0.1:0")
    defer socksLn.Close()
    go server.Serve(socksLn)

    // 4. Client: SOCKS5 handshake + CONNECT + echo round-trip
    client, _ := net.Dial("tcp", socksLn.Addr().String())
    defer client.Close()
    // ... send no-auth method selection, CONNECT request ...
    // ... write "hello", read back, assert "hello" ...
}

// TestSOCKS5UDPRelay — UDP ASSOCIATE,放子计划 04
```

> **测试要点**:SOCKS5 协议(RFC 1928)握手字节流:
> 1. 客户端发版本+方法:`05 01 00`(SOCKS5, 1 method, no-auth)
> 2. 服务端选方法:`05 00`(no-auth)
> 3. 客户端发 CONNECT:`05 01 00 01 <4-byte IP> <2-byte port>`(TCP connect, IPv4)
> 4. 服务端回复成功:`05 00 00 01 <bind-IP> <bind-port>`
> 5. 之后双向透明转发

### 3.3 改 `cmd/qmidial/main.go` — 加 -socks5 flag

**新增 flag**:

```go
func main() {
    dial := flag.Bool("dial", false, "perform WDS dialup ...")
    tunMode := flag.Bool("tun", false, "create TUN + start relay ... (needs admin)")
    socks5Mode := flag.Bool("socks5", false, "start SOCKS5 proxy via netstack (no admin needed)")
    socks5Addr := flag.String("socks5-addr", "127.0.0.1:1080", "SOCKS5 listen address")
    apn := flag.String("apn", "3gnet", "APN for dialup")
    flag.Parse()

    if *tunMode {
        *dial = true
    }
    if *socks5Mode {
        *dial = true
    }

    // ... (步骤 1-2 transport + client 不变) ...

    // 步骤 3:根据模式创建 sink
    var sink qmidatapath.PacketSink
    var tunDev tun.Device  // TUN 模式才需要(生命周期归 main)
    if *tunMode {
        // ... 现有 TUN 创建逻辑 ...
        sink = qmidatapath.NewTUNPacketSink(tunDev, offset, 1500)
    } else if *socks5Mode {
        // netstack 模式:不需要 TUN,但需要先拿到 IP
        // IP 在 mgr.Connect() 之后才有,所以 sink 创建延迟到步骤 7 之后
    }

    // 步骤 4-5:manager + StartCore(共用)
    // 注意:socks5 模式下 NetInterface 也要设非空触发 WDA
    cfg := manager.Config{
        APN:        *apn,
        EnableIPv4: true,
        EnableIPv6: true,
        Device:     manager.ModemDevice{NetInterface: tunName},  // tunName or "dummy"
        // ...
    }

    // 步骤 6:device info(共用)
    // 步骤 7:Connect(共用)
    if !*dial { /* ... */ }
    if err := mgr.Connect(); err != nil { /* ... */ }
    printConnectionInfo(mgr)

    // 步骤 8a:socks5 模式创建 netstack sink
    if *socks5Mode {
        s := mgr.Settings()
        localIP := netip.Addr(s.IPv4Address)  // 转换 net.IP → netip.Addr
        sink, err = qmidatapath.NewNetstackPacketSink(localIP, s.MTU)
        if err != nil { /* cleanup */ }
    }

    // 步骤 8b:bulk endpoints + bridge(共用,TUN 和 netstack 都走这里)
    bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
    if err != nil { /* ... */ }

    bridge = qmidatapath.New(sink, bulkIn, bulkOut, 1500, true)  // zlp=true
    if err := bridge.Start(ctx); err != nil { /* ... */ }

    // 步骤 8c:TUN 模式的额外配置(DNS/路由/诊断)
    if *tunMode {
        // ... 现有 configureDNS / setInterfaceMetric / 诊断逻辑 ...
    }

    // 步骤 8d:socks5 模式启动 SOCKS5 server
    if *socks5Mode {
        socksCtx, socksCancel := context.WithCancel(ctx)
        defer socksCancel()
        go func() {
            fmt.Printf("SOCKS5 listening on %s (no admin needed)\n", *socks5Addr)
            if err := qmidatapath.RunSOCKS5(socksCtx,
                sink.(*qmidatapath.NetstackPacketSink), *socks5Addr); err != nil {
                fmt.Fprintf(os.Stderr, "SOCKS5 server error: %v\n", err)
            }
        }()
        fmt.Printf("\n  curl --socks5-hostname %s http://www.baidu.com\n", *socks5Addr)
        // hold until Ctrl+C or timeout
        waitForSignal()  // 新增 helper
    }
}
```

### 3.4 manager.Config 的 NetInterface 处理(netstack 模式)

**问题**:manager 的 `shouldAllocateWDA()` 要求 `Config.Device.NetInterface` 非空才分配 WDA(触发 raw-IP)。netstack 模式没有 TUN,但必须触发 WDA。

**方案:复用已有字段,零 third_party 改动**:

manager.Config **已有** `NoRoute bool` 和 `NoDNS bool` 字段(manager.go:75-76)。`configureNetwork()` 对 netcfg 调用失败只 log warning/error,**不 return error**(最终 return nil,manager.go:3536)。且 `m.settings` 在 netcfg 调用**之前**已存入(manager.go:3445-3447)。

```go
// netstack 模式下的 Config
cfg := manager.Config{
    Device:     manager.ModemDevice{NetInterface: "dummy"},  // 触发 WDA 分配
    NoRoute:    true,   // 已有字段:跳过 netcfg.AddDefaultRoute
    NoDNS:      true,   // 已有字段:跳过 netcfg.UpdateResolvConf
    // ... 其余同 TUN 模式 ...
}
```

**执行流程**(netstack 模式):
1. `enableRawIP()` — WDA SetDataFormat(modem 端),非 Linux 跳过 sysfs。**成功**,raw-IP 已设。
2. `StartNetwork()` — QMI 拨号,获取 PDH。**成功**。
3. `configureNetwork()` — `m.settings` 存入(manager.go:3445),然后:
   - `netcfg.BringUp("dummy")` → log Warn,继续
   - `netcfg.SetIPAddress("dummy", ...)` → log Error,继续
   - `!m.cfg.NoRoute` = false → 跳过路由
   - `!m.cfg.NoDNS` = false → 跳过 DNS
   - **return nil**(manager.go:3536)
4. `doConnect()` 检查 `configureNetwork()` 返回值 → nil → **成功**
5. `mgr.Settings()` 返回完整 IP/GW/DNS/MTU(m.settings 在 step 3 已存入)

**验证**:`mgr.Connect()` 返回 nil,`mgr.Settings().IPv4Address` 非空,PDH 非零。

> **Linux 注意**:Linux 上 `enableRawIP()` 会写 `/sys/class/net/<ifname>/qmi/raw_ip`(manager.go:2570-2615)。netstack 模式下 ifname="dummy" 导致 sysfs 写失败。但非 Linux(Windows/macOS)只做 modem 端 WDA,不碰 sysfs。**netstack 模式主要面向 Windows/macOS**,Linux 有原生 TUN 支持无需 netstack。如果将来需要 Linux netstack,设 `enableRawIPHook` 跳过 sysfs 即可。

### 3.5 DNS 处理(Phase 2:选项 A,host DNS)

netstack 内部需要 DNS 解析。子计划 01 的 `resolveForNetstack` 用 Go 标准 `net.DefaultResolver`(走 host 网络 DNS)。

**为什么 Phase 2 用 host DNS**:
- 简单,先验证核心通路(SOCKS5 → netstack → USB → 4G)能跑
- 大多数公网 IP 两个网络都能到达(host DNS 解析的 IP,4G 也能连)
- curl 的 `--socks5-hostname` 会把域名透传给 SOCKS5 server,由我们的 dialer 解析 —— 用 host DNS 解析即可

**Phase 2 的限制**(子计划 03 升级):
- 如果某域名解析到 host 网络可达但 4G 不可达的 IP(如内网 CDN),会失败
- DNS 查询本身走 host 网络,不消耗 4G 流量

### 3.6 清理时序(netstack 模式)

```go
// Cleanup for socks5 mode:
// 1. Cancel socks ctx → SOCKS5 listener close, in-flight CONNECT dialed connections close
// 2. sink.Close() → ep.Close() (channel) → ReadPacket returns error → sinkToModem exits
//                   stk.Close() → in-flight gonet connections drop
// 3. bridge.Stop() → cancel ctx + wg.Wait() (both goroutines exit)
// 4. mgr.Stop() / client.Close() / transport.Close()
//
// 关键:netstack sink 的 Close 比 TUN 干净 ——
// channel close 立即让 ReadContext 返回 nil,goroutine 自然退出,无死锁。
socksCancel()
sink.Close()
bridge.Stop()
mgr.Stop()
client.Close()
transport.Close()
```

### 3.7 硬件测试 `netstack_hardware_test.go`(`//go:build hardware`)

自动化验证 netstack + SOCKS5 端到端通路(不需要手动 curl):

```go
//go:build hardware

package qmidatapath

import (
    "context"
    "io"
    "net"
    "testing"
    "time"
)

// TestHardwareNetstackSOCKS5 验证 netstack + SOCKS5 端到端:
//   QMI 拨号 → NetstackPacketSink → SOCKS5 server → gonet.Dial → baidu.com:80
//
// 前置:需要真实模块(WinUSB on MI_04)、.env 里有 DJI_TEST_APN
func TestHardwareNetstackSOCKS5(t *testing.T) {
    // 1. 打开 QMI transport + 拨号(复用现有 qmitransport.Open + manager)
    //    NetInterface="dummy", NoRoute=true, NoDNS=true
    // 2. 创建 NetstackPacketSink(localIP from mgr.Settings())
    // 3. 启动 relay Bridge(sink, bulkIn, bulkOut)
    // 4. 启动 SOCKS5 server on 127.0.0.1:0 (random port)
    // 5. 用 gonet.DialContextTCP(netstack, baidu.com:80) 建立连接
    // 6. 发送 "GET / HTTP/1.0\r\nHost: www.baidu.com\r\n\r\n"
    // 7. 读取响应,断言包含 "HTTP/1." (HTTP 200 或 302)
    // 8. Cleanup: socks cancel → sink.Close → bridge.Stop → mgr.Stop → transport.Close
}

// TestHardwareNetstackPacketRoundTrip 验证 PacketSink 接口:
//   InjectPacket(构造的 ICMP/TCP 包) → netstack 处理 → ReadPacket 返回响应包
func TestHardwareNetstackPacketRoundTrip(t *testing.T) {
    // 更底层:不经过 SOCKS5,直接验证 channel.InjectInbound / ReadContext 通路
    // 用 raw IP 包验证 netstack → USB → modem → internet → modem → USB → netstack 往返
}
```

**验证判据**:
- `TestHardwareNetstackSOCKS5`:HTTP 响应包含 `"HTTP/1."`(200 或 302)
- `TestHardwareNetstackPacketRoundTrip`:TX/RX 计数器都有增长
- 无 goroutine 泄漏(cleanup 后 `runtime.NumGoroutine()` 归零或接近)

---

## 四、验证(硬件)

```bash
# 终端 1:启动 SOCKS5 代理(Windows,无需 admin)
mise exec -- go build -o qmidial.exe ./cmd/qmidial
./qmidial.exe -dial -socks5
# 期望输出:
#   [1/8] Opening QMITransport... OK
#   [7/8] Dialing... OK — IPv4 10.x.x.x, PDH 0x...
#   [8/8] SOCKS5 listening on 127.0.0.1:1080 (no admin needed)
#     curl --socks5-hostname 127.0.0.1:1080 http://www.baidu.com

# 终端 2:通过 4G 代理上网
curl --socks5-hostname 127.0.0.1:1080 -s -o /dev/null \
  -w "%{http_code} %{time_total}s\n" http://www.baidu.com
# 期望:200 0.xxs

# DNS(Phase 2 走 host DNS)
curl --socks5-hostname 127.0.0.1:1080 -s https://www.google.com -o /dev/null -w "%{http_code}\n"

# macOS(无需 sudo)
mise exec -- go run ./cmd/qmidial -dial -socks5
# 另一个终端:curl --socks5-hostname 127.0.0.1:1080 http://www.baidu.com
```

**关键验证点**:
- ✅ **不需要 admin**(SOCKS5 监听 127.0.0.1,USB 操作 gousb 不需要 admin)
- ✅ **不需要 wintun.dll / utun / tun**(netstack 用 channel,不创建 TUN)
- ✅ macOS 上无需 sudo(brew libusb + go 默认 clang)
- ✅ curl HTTP 200(经 4G)
- ✅ relay stats 有 TX/RX(双向数据)

**Stage 4 核心价值验证**:无需 admin 的 4G 上网。

---

## 五、涉及文件

| 文件 | 改动 |
|---|---|
| `go.mod` / `go.sum` | 加 `github.com/armon/go-socks5` |
| `internal/qmidatapath/socks5.go` | **新增** — RunSOCKS5(armon/go-socks5 包装) |
| `internal/qmidatapath/socks5_test.go` | **新增** — SOCKS5 CONNECT 握手测试(mock dialer) |
| `internal/qmidatapath/netstack_hardware_test.go` | **新增** — 硬件测试(`//go:build hardware`) |
| `cmd/qmidial/main.go` | 改 — `-socks5`/`-socks5-addr` flag + netstack 分支 + 清理时序 |
| ~~`third_party/quectel-qmi-go/manager/`~~ | ~~加 SkipNetworkConfig~~ **不需要**(用已有 NoRoute+NoDNS) |

---

## 六、风险 & 缓解

### R1:manager.ConfigureNetwork 在 "dummy" 网卡上失败(低 — 已解决)

`mgr.Connect()` 在 netstack 模式下会给 "dummy" 网卡配 IP,Windows/macOS 会失败。

**已解决**(§3.4):manager.Config 已有 `NoRoute`+`NoDNS` 字段。`configureNetwork()` 对 netcfg 失败只 log warning、不 return error(manager.go:3536 return nil)。`m.settings` 在 netcfg 之前已存入(manager.go:3445)。**不需要改 third_party,不需要 catch error**。

**残留**:`netcfg.BringUp("dummy")` 和 `netcfg.SetIPAddress("dummy", ...)` 会 log Error/Warning,但不影响功能。实施时可接受(或设 `enableRawIPHook` 绕过 Linux sysfs 问题)。

### R2:netstack dialer 的 DNS 解析走 host 网络(低,Phase 3 修)

Phase 2 用 host DNS,某些场景可能解析到 4G 不可达的 IP。

**缓解**:Phase 2 接受这个限制(子计划 03 升级)。用 `curl --socks5-hostname` 让域名透传到我们的 dialer,dialer 内部解析。

### R3:armon/go-socks5 的 CONNECT 默认行为(低)

库默认 `Config.Dial == nil` 时走 `net.Dial`(host 网络)。确认设了 `Config.Dial` 后所有 CONNECT 都走我们的 dialer(实测验证:用一个只接受 4G IP 的目标,确认 host 网络连不上但 SOCKS5 能连上)。

**缓解**:实施时用一个 4G-only 的测试目标(如运营商内网 IP)验证流量确实走 4G 而非 host 网络。

### R4:Close 时序(goroutine 泄漏)(低)

netstack sink Close → channel close → ReadPacket 返回 error。但 SOCKS5 in-flight 连接(已 Accept 的 client conn + 对端 gonet conn)需要单独关闭。

**缓解**:`RunSOCKS5` 的 ctx cancel 触发 listener close,armon/go-socks5 的 Serve 退出。in-flight 连接靠 ctx 链或 netstack `stk.Close()` 让 gonet conn drop。实施时验证 Close 后 goroutine 数归零(`runtime.NumGoroutine()`)。
