# 子计划 03 — 测试(mock + 硬件)

> 隶属 `plans/stage3-tun-internet.md`(总览)。依赖子计划 02 完成。
> 创建于 2026-07-12。

## 目标

为 tunbridge relay 层编写 mock 单测(离线,CI 友好)和硬件集成测试(需设备)。

## 依赖 / 前置

- **子计划 02 完成**:qmidial -tun 端到端工作
- 遵循项目测试约定(AGENTS.md):标准库 testing,手写 mock,table-driven,`-race` 硬性要求

## 步骤

### 1. Mock 工具(`internal/tunbridge/mock_test.go`)

#### MockBulkReader

```go
type mockBulkReader struct {
    packets [][]byte  // 预设的下行包队列
    idx     int
    mu      sync.Mutex
}

func (m *mockBulkReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
    m.mu.Lock()
    if m.idx >= len(m.packets) {
        m.mu.Unlock()
        <-ctx.Done()  // 队列空,阻塞到 cancel
        return 0, ctx.Err()
    }
    pkt := m.packets[m.idx]
    m.idx++
    m.mu.Unlock()
    n := copy(buf, pkt)
    return n, nil
}
```

#### MockBulkWriter

```go
type mockBulkWriter struct {
    written [][]byte  // 记录所有写入的包
    mu      sync.Mutex
}

func (m *mockBulkWriter) WriteContext(ctx context.Context, buf []byte) (int, error) {
    m.mu.Lock()
    cp := make([]byte, len(buf))
    copy(cp, buf)
    m.written = append(m.written, cp)
    m.mu.Unlock()
    return len(buf), nil
}
```

#### MockTUNDevice

WireGuard 的 `tun.Device` 接口 mock:
- `Read`:从预设队列返回包
- `Write`:记录写入的包
- `Name`/`MTU`/`Close`/`Events`/`BatchSize`:返回固定值

### 2. Mock 单测(`internal/tunbridge/relay_test.go`)

#### TestRelayModemToTun

验证 modem → TUN 方向:
1. 注入 mockBulkReader(预设 3 个 IPv4 包)
2. 注入 mockBulkWriter(不期望写入)
3. 注入 mockTUNDevice
4. Start relay,等包传完,cancel
5. 断言 mockTUN 收到 3 个包,内容与预设一致

#### TestRelayTunToModem

验证 TUN → modem 方向:
1. 注入 mockTUNDevice(预设 2 个 IPv4 包)
2. 注入 mockBulkWriter(记录写入)
3. Start relay,等包传完,cancel
4. 断言 mockBulkWriter 收到 2 个包

#### TestRelayNonIPPacketDropped

验证非 IP 包被丢弃:
1. mockBulkReader 预设一个首字节 version=0 的包
2. relay 运行,cancel
3. 断言 mockTUN 没收到任何包(丢弃了)

#### TestRelayConcurrentClose

验证 cancel 不 panic、不 deadlock:
1. Start relay
2. 立即 cancel context
3. Relay 应在 ≤1s 内退出

#### TestRelayIPv6Passthrough

验证 IPv6 包正常中继:
1. mockBulkReader 预设 IPv6 包(首字节 version=6)
2. 断言 mockTUN 收到

所有测试用 `-race` 跑。

### 3. 硬件集成测试(`internal/tunbridge/relay_hardware_test.go`)

`//go:build hardware`

#### TestHardwareTUNCreateAndConfig

验证 TUN 创建 + 网络配置:
1. `tunbridge.New("DJI Modem", 1500, nil, nil)`
2. 检查 `Name()` 返回非空
3. 用 netcfg 设 IP: `netcfg.SetIPAddress(name, "10.0.0.1", 24)`
4. 检查 `netsh interface ip show config` 包含该 IP
5. `bridge.Stop()`

#### TestHardwareFullDialupWithTUN

完整端到端:
1. QMITransport.Open
2. qmi.NewClientFromTransport
3. tunbridge.New (创建 TUN)
4. manager.NewWithClient (cfg.NetInterface = tun name)
5. mgr.StartCoreContext (WDA 分配 + enableRawIP)
6. mgr.Connect (WDS StartNetwork + configureNetwork on TUN)
7. transport.OpenBulkEndpoints
8. bridge.SetBulkEndpoints + bridge.Start
9. ping 8.8.8.8 通过 TUN
10. nslookup baidu.com 通过 TUN
11. Clean shutdown

#### TestHardwareRelayBidirectional

验证 relay 双向工作:
1. 同上 1-8
2. 发送 ICMP echo(手动构造 IP+ICMP 到 bulk OUT)
3. 从 TUN 读,检查是否收到 ICMP reply
4. 反向:从 TUN 写 ICMP echo,从 bulk IN 读 reply

### 4. 测试命令

```bash
# 离线 mock 测试(CI)
mise exec -- go test -race -v ./internal/tunbridge/

# 硬件测试(需设备 + wintun.dll + 管理员)
mise exec -- go test -tags=hardware -v -timeout 120s ./internal/tunbridge/

# 全量
mise exec -- go test -race ./...
mise exec -- go test -tags=hardware -race ./...
```

## 交付物 / 完成标志

- [ ] `relay_test.go`:5+ mock 测试,`-race` 通过
- [ ] `relay_hardware_test.go`:2+ 硬件测试,`-tags=hardware` 通过
- [ ] TUN 创建 + 网络配置验证通过
- [ ] 端到端 ping + DNS 验证通过
- [ ] 并发 Close 0 segfault(延续 issue/001 防护)

## 风险

| 风险 | 缓解 |
|---|---|
| Mock TUN Device 复杂(Device 接口方法多) | 只 mock 需要的方法,其余 panic("not implemented") |
| 硬件测试设默认路由影响主机网络 | 用 `cfg.NoRoute=true`,手动 ping 指定接口 |
| ping 不通但 relay 正常 | 可能 DNS 问题,先 ping IP 再 ping 域名 |
| wintun.dll 架构不匹配 | 确认下载 amd64 版本 |
