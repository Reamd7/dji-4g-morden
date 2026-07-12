# 子计划 02 — qmidial 集成 + 网络配置

> 隶属 `plans/stage3-tun-internet.md`(总览)。依赖子计划 01 完成。
> 创建于 2026-07-12。

## 目标

把 TUN + relay 集成到 `cmd/qmidial`,实现完整的"USB → QMI → WDS → TUN → 上网"链路。
端到端验证:通过 TUN 能 ping 通外网。

## 依赖 / 前置

- **子计划 01 完成**:tunbridge 包可构建
- `wintun.dll` 已放置(Windows)
- 管理员权限运行

## 步骤

### 1. 修改 `cmd/qmidial/main.go`

新增 `-tun` 标志,启用 TUN + relay:

```
mise exec -- go run ./cmd/qmidial -dial -tun
```

流程变更:

```
[1/8] Open QMITransport (MI_04)           ← 现有
[2/8] Create QMI client                    ← 现有
[3/8] Create TUN device                    ← 新增
[4/8] Create manager (cfg.NetInterface=tun)← 修改
[5/8] Start core (WDA + WDS allocation)    ← 现有,但现在会分配 WDA
[6/8] Connect (WDS StartNetwork + configureNetwork)
      → netcfg 在 TUN 上设 IP/路由/DNS     ← 现有 configureNetwork,目标变成 TUN
[7/8] Open bulk endpoints + start relay    ← 新增
[8/8] Verify: ping through TUN             ← 新增
```

### 2. TUN 创建时机

TUN 必须在 `mgr.StartCoreContext()` 之前创建,因为:
- `cfg.Device.NetInterface` 必须指向已存在的接口
- `shouldAllocateWDA()` 检查 `NetInterface != ""`
- `configureNetwork()` 在 `Connect()` 中设 IP 到该接口

```go
// Step 3: Create TUN
tunName := "dji0"
if runtime.GOOS == "darwin" {
    tunName = "utun"
} else if runtime.GOOS == "windows" {
    tunName = "DJI Modem"
}
bridge, err := tunbridge.New(tunName, 1500, nil, nil) // bulk EPs opened later
// ...
actualName, _ := bridge.Name()

// Step 4: Create manager with TUN interface name
cfg := manager.Config{
    APN: *apn,
    Device: manager.ModemDevice{NetInterface: actualName},
    EnableIPv4: true,
    EnableIPv6: true,
}
```

### 3. Bulk EP 打开 + relay 启动时机

在 `mgr.Connect()` 成功之后(modem 已开始发数据):

```go
// Step 7: Open bulk endpoints and start relay
bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
if err != nil { /* ... */ }
bridge.SetBulkEndpoints(bulkIn, bulkOut) // 注入实际 endpoints
bridge.Start(ctx)
fmt.Printf("Relay active: bulk EP ↔ TUN %s\n", actualName)
```

Bridge 的构造函数先创建 TUN(不需要 bulk EP),然后通过 setter 注入 bulk EP。

### 4. 网络配置(manager.configureNetwork)

manager 的 `configureNetwork()` 会自动:
1. `netcfg.BringUp(ifname)` — 启动 TUN 接口
2. `netcfg.SetIPAddress(ifname, ip, prefix)` — 设 IP
3. `netcfg.AddDefaultRoute(ifname, gateway)` — 设默认路由
4. `netcfg.SetMTU(ifname, mtu)` — 设 MTU
5. `netcfg.UpdateDNS(dns1, dns2)` — 设 DNS

**Windows 验证**:
```bash
# 验证 TUN 接口存在且有 IP
netsh interface ip show config name="DJI Modem"
ipconfig | findstr "DJI Modem" -A 5

# 验证路由
route print -4 | findstr "0.0.0.0"

# 验证 DNS
netsh interface ip show dns
```

**Linux 验证**:
```bash
ip addr show dji0
ip route show
cat /etc/resolv.conf
```

### 5. ping 验证

通过 TUN 接口 ping 外网:

```go
// Step 8: Verify connectivity
// Windows: ping -n 3 8.8.8.8
// Linux/macOS: ping -c 3 -I <tun> 8.8.8.8
```

注意:ping 可能需要几秒建立 ARP/NDP(虽然是点对点 TUN,不需要 ARP)。
默认路由指向 TUN 后,所有流量走 TUN → relay → modem → 互联网。

**⚠️ 风险**:设默认路由后,如果 relay 不通,主机的网络会中断(所有流量走 TUN 但 TUN 不通)。
**缓解**:
- 初始测试用 `ping -S <tun-ip>` 或 `ping -I <tun>` 指定接口,不设默认路由
- 或者用 `cfg.NoRoute = true` 跳过默认路由设置,手动指定路由
- 确认 relay 正常后再设默认路由

### 6. 清理

```go
defer func() {
    bridge.Stop()       // 停 relay + 关 TUN
    transport.Close()   // 释放 USB
}()
```

## 交付物 / 完成标志

- [ ] `-tun` 标志工作:创建 TUN + WDA 分配 + 拨号 + relay 启动
- [ ] TUN 接口有运营商 IP(`ipconfig` / `ip addr` 可见)
- [ ] 通过 TUN ping 通外网(`ping 8.8.8.8` 通过)
- [ ] DNS 解析工作(`nslookup baidu.com` 通过)
- [ ] Clean shutdown(Stop relay → close TUN → close USB,无崩溃)

## 风险

| 风险 | 缓解 |
|---|---|
| 默认路由抢走主机网络 | 初始测试用 `-noroute`,确认 relay 后再设默认路由 |
| netcfg 的 netsh 在 Wintun 适配器上失败 | 降级:用 winipcfg(LUID API)直接配置 |
| WDA SetDataFormat 在 configureNetwork 前未完成 | manager.StartCore 中 enableRawIP 在 StartNetwork 之前,顺序正确 |
| DNS 不通 | 用运营商 DNS 或公共 DNS(114.114.114.114 / 8.8.8.8) |
| ping 超时 | LTE 延迟较高(50-200ms),设 5s 超时 |
