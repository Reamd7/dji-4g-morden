# 子计划 02 — qmidial 集成 + 网络配置 + DNS

> 隶属 `plans/stage3-tun-internet.md`(总览)。把 TUN+relay 串进拨号流程,配 IP/路由/DNS,实现端到端上网。
> 依赖子计划 01(Bridge 可用)。

---

## 一、目标

把 TUN 创建 + relay 启动串进现有 `cmd/qmidial/`,加 `-tun` 标志。拨号成功后:
1. 创建 TUN 网卡(wireguard/tun)
2. 启动 Bridge(relay 双向中继)
3. 配置 IP/路由/MTU(netcfg,manager.configureNetwork 自动)
4. **配置 DNS(自建——netcfg.UpdateDNS 在 Windows/macOS 不可用)**

最终:`qmidial -dial -tun` 一条命令完成拨号+上网,系统 ping/curl 走 4G。

---

## 二、qmidial 扩展

### 2.1 -tun 标志

```go
var tunMode = flag.Bool("tun", false, "create TUN + start relay for actual internet")
```

### 2.2 时序(关键)

TUN 必须在 `mgr.Connect()` 之前创建,因为 `configureNetwork` 会向 `cfg.Device.NetInterface` 配 IP:

```
1. transport = qmitransport.Open()
2. client = qmi.NewClientFromTransport(...)
3. tunDev = tun.CreateTUN("qmi0", 1500)           ← 先创建 TUN
4. tunName, _ = tunDev.Name()                       ← 取实际名(macOS 可能是 utunN)
5. cfg.Device.NetInterface = tunName                ← 设到 manager config
6. mgr = manager.NewWithClient(cfg, nil, client)
7. mgr.StartCoreContext(ctx)                        ← WDA 分配 + enableRawIP(modem 切 raw-IP)
8. mgr.Connect()                                    ← WDS StartNetwork + configureNetwork(配 TUN 的 IP/路由/MTU)
9. bulkIn, bulkOut = transport.OpenBulkEndpoints()
10. zlp := true                                     ← 子计划 00 D2 结果
11. offset := 0; if runtime.GOOS == "darwin" { offset = 4 }
12. bridge = qmidatapath.New(tunDev, bulkIn, bulkOut, offset, 1500, zlp)
13. bridge.Start(ctx)
14. configureDNS(tunName, dns1, dns2)               ← 自建(§三)
15. 等 Ctrl-C
16. bridge.Stop() → tunDev.Close() → transport.Close()
```

### 2.3 manager.configureNetwork 自动配 IP/路由

设 `cfg.Device.NetInterface = tunName` 后,manager 的 `configureNetwork`(`manager.go:3414`)自动:
- `netcfg.BringUp(tunName)` — 启动接口
- `netcfg.SetIPAddress(tunName, settings.IPv4Address, prefix)` — 设 IP
- `netcfg.AddDefaultRoute(tunName, settings.IPv4Gateway)` — 设默认路由
- `netcfg.SetMTU(tunName, 1500)` — 设 MTU
- `netcfg.SetIPv6Address(...)` — 双栈

**netcfg 的 IP/路由/MTU/BringUp 三平台可用**(见总览 netcfg 评估表)。无需改 manager/netcfg 的这部分。

---

## 三、DNS 配置(自建,netcfg 不足)

### 3.1 问题

**本轮调研发现** `netcfg.UpdateDNS` 三平台现状:
- Linux:写 `/etc/resolv.conf`(直写,不整合 systemd-resolved)
- **Windows:`return error` stub**(缺 ifname,设计缺陷)
- **macOS:`return nil` no-op**(注释说 skip system-wide DNS)

manager.configureNetwork 调 `netcfg.UpdateDNS(dns1, dns2)` 时 Windows 会 Warn 但不致命。DNS 必须自己配。

### 3.2 实现(`cmd/qmidial/dns.go` 或内联)

dns1/dns2 从 `mgr.Settings().IPv4DNS1/DNS2` 取(阶段 2 拨号拿到)。

**Windows**:
```go
exec.Command("netsh", "interface", "ip", "set", "dns",
    "name="+tunName, "static", dns1, "primary").Run()
exec.Command("netsh", "interface", "ip", "add", "dns",
    "name="+tunName, dns2, "index=2").Run()
```

**macOS**:
```go
// network service 名需先探测(通常 "Ethernet" 或 "Wi-Fi")
exec.Command("networksetup", "-setdnsservers", service, dns1, dns2).Run()
```

**Linux**:
```go
// 检测 systemd-resolved:若 /etc/resolv.conf 是 symlink
if isSymlink("/etc/resolv.conf") {
    exec.Command("resolvectl", "dns", tunName, dns1, dns2).Run()
} else {
    // 直写 resolv.conf(netcfg 已做,但可覆盖)
}
```

### 3.3 DNS 清理

断开时恢复 DNS:
- Windows:`netsh interface ip set dns name="<tun>" source=dhcp`(TUN 删除后自动)
- macOS:`networksetup -setdnsservers <service> empty`
- Linux:恢复原 resolv.conf

---

## 四、Wintun.dll 分发(Windows)

### 4.1 下载

从 https://www.wintun.net/ 下载 ZIP,取 `bin/amd64/wintun.dll`(~40KB,已签名)。

### 4.2 方案

**方案 A(简单,首选)**:随 exe 放同目录。wintun-go 用 `LoadLibraryEx("wintun.dll", ...)` 按标准搜索路径找(exe 同目录优先)。

**方案 B(embed,可选)**:`go:embed` 嵌入 DLL,运行时释放临时路径 + 显式 LoadLibraryEx。更自包含但更复杂。

阶段 3 用**方案 A**。

### 4.3 管理员权限

Windows 创建 Wintun 适配器需管理员权限。qmidial 启动时检查并提示"请以管理员身份运行"。

---

## 五、权限要求

| 平台 | 权限 | 原因 |
|---|---|---|
| Windows | 管理员 | 创建 Wintun 适配器 + netsh 配 IP/DNS |
| macOS | root(sudo) | 创建 utun + ifconfig + networksetup |
| Linux | root 或 CAP_NET_ADMIN | 创建 TUN + ip addr/route |

---

## 六、完成标志

- [ ] `cmd/qmidial/main.go` 加 `-tun` 标志
- [ ] TUN 创建 + Bridge 启动 + IP/路由自动配(manager.configureNetwork)
- [ ] DNS 三平台自建(Windows netsh / macOS networksetup / Linux resolvectl)
- [ ] Windows wintun.dll 分发(同目录)
- [ ] 端到端:`qmidial -dial -tun` → 系统 `ping 114.114.114.114` 走 4G 成功
- [ ] DNS 解析:`nslookup baidu.com` 通过

---

## 七、风险

| 风险 | 缓解 |
|---|---|
| TUN 创建时序 vs configureNetwork | 先 CreateTUN 再 Connect;macOS 用 `tunDev.Name()` 取实际名设回 cfg |
| 默认路由与主网卡冲突 | 初始测试用 `cfg.NoRoute=true` + 手动 ping 指定接口,确认 relay 后再设默认路由 |
| macOS utun 改名,configureNetwork 配错名 | `tunDev.Name()` 取实际名,设回 `cfg.Device.NetInterface` 后再 Connect |
| Windows DNS netsh 需要接口名匹配 | 用 TUN `Name()` 返回值;netsh 按名找 |
| 默认路由设后 relay 不通 → 主机断网 | 先不设默认路由,手动 `ping -S <tun-ip>` 确认 relay;再放开 |

---

## 八、相关文件

- `cmd/qmidial/main.go` — 现有,扩展 -tun
- `internal/qmidatapath/bridge.go` — 子计划 01,Bridge
- `third_party/quectel-qmi-go/manager/manager.go` — configureNetwork(:3414)
- `third_party/quectel-qmi-go/netcfg/` — SetIPAddress/AddDefaultRoute/SetMTU 可用,UpdateDNS 不足
