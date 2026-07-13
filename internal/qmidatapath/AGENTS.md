# AGENTS.md — internal/qmidatapath

## 包职责

USB bulk EP 与主机侧网络栈之间的双向 raw IP 中继。
- **后端 1(阶段 3)**:`TUNPacketSink` → wireguard/tun → 内核 TCP/IP(需 admin)
- **后端 2(阶段 4)**:`NetstackPacketSink` → gVisor netstack → SOCKS5(无需 admin)

两个后端共享同一条 USB bulk 数据通路,通过 `PacketSink` 接口切换最后一跳。Bridge 不知道 TUN/netstack 的存在。

## 核心文件

- `sink.go` — `PacketSink` 接口(`ReadPacket(ctx)` 上行 / `WritePacket(pkt)` 下行 / `Name` / `Close`)
- `tun_sink.go` — `TUNPacketSink`:包装 wireguard/tun.Device,内部处理 macOS utun 4 字节 offset headroom
- `netstack_sink.go` — `NetstackPacketSink`:gVisor channel link endpoint + `NetstackDialer()`(gonet 包装,供 SOCKS5 `Config.Dial`)
- `socks5.go` — `RunSOCKS5`:armon/go-socks5 包装,listener 跑 host 网络栈,`Dial` 注入 netstack dialer(所有 CONNECT 走 4G)
- `relay.go` — `Bridge`:`PacketSink ↔ bulk EP`,ZLP + Stats
- `*_test.go` — mock + 硬件测试(见下)

## 关键设计决策

### PacketSink 抽象(阶段 4)

Bridge 只跟 `PacketSink` 接口对话,不依赖 TUN/netstack 具体实现:
- `ReadPacket(ctx)`:host → modem(上行;netstack `channel.ReadContext` / TUN batch read)
- `WritePacket(pkt)`:modem → host(下行;`InjectInbound` / TUN batch write)
- `Close` 必须解除 `ReadPacket` 阻塞(否则 `bridge.Stop` 的 `wg.Wait` 死锁)

### ZLP(探针驱动,两后端共用)

`zlp=true`:TX 包长度是 bulk OUT maxPacketSize(512)整数倍时追加 0 字节 Write(QDC507 物理约束,子计划 00 D2 实测;Linux `qmi_wwan_q.c` 的 `FLAG_SEND_ZLP` 印证)。ZLP 逻辑在 `sinkToModem` 内,与后端无关。

### raw-IP 直传(两后端共用)

WDA SetDataFormat(LinkProtocolIP)后 bulk EP = 裸 IP 包(无以太网头、无 QMAP 头)。TUN 与 netstack channel 都是 layer-3,relay 直传无需任何头处理。

### Close 时序(TUN vs netstack 差异,均已实测)

- **TUNPacketSink**:`tun.Read` 不响应 ctx(阻塞直到 TUN 关闭)。必须 `tun.Close() → bridge.Stop() → transport.Close()`。反过来死锁(Stop 的 `wg.Wait` 等 tunToModem,tunToModem 阻塞在 Read,Read 等 TUN 关闭)。
- **NetstackPacketSink**:`channel.ReadContext` 支持 ctx 取消(Close 关 channel → 返回 nil → goroutine 自然退出)。`sink.Close() → bridge.Stop() → transport.Close()` 即可,更干净。
- **`Bridge.Stop()` 带 5s 超时**(`wg.Wait` 包在 `select` 超时里):防 WinUSB 上 `ReadContext` 不响应 ctx 取消导致挂起(2026-07-13 硬件测试 fix,commit `34341de`)。macOS 正常路径不触发超时(SOCKS5 SIGINT 关闭实测 5s 内退出)。

### DNS(Phase 3:经 4G)

`SetDNSServers(modemDNS)` 后,`NetstackDialer()` 用 `net.Resolver{PreferGo:true, Dial: gonet}` 解析域名——DNS query 经 netstack → USB → modem → 4G 运营商 DNS,而非 host 网络。DNS 协议(压缩指针/CNAME/TCP fallback)委托标准库,只把 transport 换成 gonet,零新依赖。无 DNS 服务器时回退 host resolver(向后兼容)。实测:host reach 不到运营商 DNS(`dig @运营商DNS` 超时),SOCKS5 用该 DNS 解析成功(HTTP 200)——证明 query 必经 4G。

## 测试

- `relay_test.go`:PacketSink mock 测试(Bridge 收发/ZLP/错误路径/Stop)
- `netstack_sink_test.go`:netstack 收发 + ctx cancel + Close
- `netstack_api_smoke_test.go`:gVisor API smoke(编译期 + 基本收发)
- `socks5_test.go`:SOCKS5 CONNECT 握手(本地 echo server + mock dialer)
- `relay_hardware_test.go`:硬件 TUN relay(build tag: hardware)
- `hwenv_test.go`:硬件环境探测 helper

## 不做的事

- 不做内核 TCP/IP(TUN 模式交给内核 / netstack 模式交给 gVisor)
- 不做路由配置(manager.configureNetwork / netstack 内部路由表)
- 不做 DNS 配置(netstack 内部 resolver / cmd 层 dns.go)
- 不做 QMAP 头处理(raw-IP 已确认;QMAP 是降级分支)
