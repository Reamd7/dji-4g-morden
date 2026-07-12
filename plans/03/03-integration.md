# 子计划 03 — qmidial 集成 + 网络配置

> 隶属 `plans/03/00-overview.md`。把 TUN+relay 串进拨号流程,配 IP/路由/DNS,实现端到端上网。
> 依赖子计划 02(Bridge 可用)。创建于 2026-07-12。

---

## 一、目标

把 TUN 创建 + relay 启动串进现有 `cmd/qmidial/`,加 `-tun` 标志。拨号成功后:
1. 创建 TUN 网卡(wireguard/tun)
2. 启动 Bridge(relay 双向中继)
3. 配置 IP/路由/MTU(netcfg,manager.configureNetwork 自动)
4. 配置 DNS(自建,netcfg.UpdateDNS 不足)

最终:`qmidial -dial -tun` 一条命令完成拨号+上网,系统 ping/curl 走 4G。

---

## 二、qmidial 扩展

### 2.1 -tun 标志

```go
var tunMode = flag.Bool("tun", false, "create TUN + start relay for actual internet")
```

拨号成功(`mgr.Connect()`)后,如果 `-tun`:

```go
if *tunMode {
    // 1. 创建 TUN
    const tunName = "qmi0"  // Linux;macOS 被 utun 改名;Windows 用此名
    const mtu = 1500
    offset := 0
    if runtime.GOOS == "darwin" { offset = 4 }
    tunDev, err := tun.CreateTUN(tunName, mtu)

    // 2. 打开 bulk endpoints + 启动 relay
    bulkIn, bulkOut, err := transport.OpenBulkEndpoints()
    zlp := true  // 子计划 01 D2 结果
    bridge := qmidatapath.New(tunDev, bulkIn, bulkOut, offset, mtu, zlp)
    bridge.Start(ctx)

    // 3. 配 IP/路由/MTU —— manager 已经做了(见 2.2)
    // 4. 配 DNS —— 自建(见 §三)

    // 等 Ctrl-C
    <-sigChan
    bridge.Stop()
}
```

### 2.2 manager.configureNetwork 自动配 IP/路由

**关键**:设 `cfg.Device.NetInterface = tunName` 后,manager 的 `configureNetwork`(`manager.go:3414`)会自动:
- `netcfg.BringUp(tunName)`
- `netcfg.SetIPAddress(tunName, settings.IPv4Address, prefix)` —— 阶段 2 拿的 `10.147.0.1/27`
- `netcfg.AddDefaultRoute(tunName, settings.IPv4Gateway)` —— 网关 `10.147.0.2`
- `netcfg.SetMTU(tunName, 1500)`
- `netcfg.SetIPv6Address(...)`(双栈)

netcfg 三平台都能配 TUN 接口名(Linux netlink / Windows netsh / macOS ifconfig 按名找,TUN 创建后即生效)。**无需改 manager / netcfg 的 IP 路由部分**。

**时序问题**:manager.Connect() 调 configureNetwork 时,TUN 必须已存在。所以:
- 先 `tun.CreateTUN(tunName, mtu)` 创建网卡
- 再 `mgr.Connect()`(它内部 configureNetwork 配 TUN)
- Connect 成功后 `OpenBulkEndpoints` + `bridge.Start`

或者:设 `cfg.NoDial`/跳过 configureNetwork,自己拿 `mgr.Settings()` 调 netcfg。倾向前者(复用 manager)。

---

## 三、DNS 配置(自建,netcfg 不足)

**本轮调研发现 netcfg.UpdateDNS 三平台现状**:
- Linux:写 `/etc/resolv.conf`(直写,不整合 systemd-resolved/resolvconf)
- **Windows:`return error` stub**(缺 ifname,设计缺陷)
- **macOS:`return nil` no-op**(注释说 skip system-wide DNS)

阶段 3 必须自建 DNS 配置:

### 3.1 Windows

```go
// netsh interface ip set dns name="<tun>" static <dns1> primary
// netsh interface ip add dns name="<tun>" <dns2> index=2
exec.Command("netsh", "interface", "ip", "set", "dns",
    "name="+tunName, "static", dns1, "primary").Run()
exec.Command("netsh", "interface", "ip", "add", "dns",
    "name="+tunName, dns2, "index=2").Run()
```

dns1/dns2 从阶段 2 拨号结果:`114.114.114.114` / `223.5.5.5`。

### 3.2 macOS

```go
// networksetup -setdnsservers "<networkservice>" <dns1> <dns2>
// network service 名需先探测(通常 "Ethernet" 或 "Wi-Fi",或用 TUN 服务名)
exec.Command("networksetup", "-setdnsservers", service, dns1, dns2).Run()
```

### 3.3 Linux

netcfg 的 `/etc/resolv.conf` 直写可用,但生产环境检测 systemd-resolved:
```go
// 若 /etc/resolv.conf 是 symlink 到 ../run/systemd/resolve/...
// 则用 resolvectl dns <ifname> <dns1> <dns2>
// 否则直写 resolv.conf
```

建议:新建 `internal/dnsconfig/`(或加到 qmidatapath),三平台 exec。本子计划范围。

---

## 四、Wintun.dll 分发(Windows)

### 4.1 下载

从 https://www.wintun.net/ 下载 ZIP,取 `bin/amd64/wintun.dll`(~40KB,已签名)。

### 4.2 分发方式

**方案 A(简单)**:随 exe 放同目录。wintun-go 用 `syscall.NewLazyDLL("wintun.dll")` 按标准搜索路径找(exe 同目录)。
```
dji-modem-research/
├── qmidial.exe
└── wintun.dll    ← 放这里
```

**方案 B(embed)**:`go:embed` 嵌入 DLL,运行时释放临时路径 + `LoadLibraryEx` 显式路径(wireguard-windows 的 RCDATA 方式)。

阶段 3 用**方案 A**(最简)。目录结构加 `wintun/` 或放 cmd/qmidial/ 旁。`.gitignore` 不忽略(入库,~40KB 可接受)。

### 4.3 管理员权限

Windows 创建 Wintun 适配器需管理员权限。qmidial 启动时检查并提示:
```go
// 检测是否管理员,否则提示 "请以管理员身份运行"
```

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
- [ ] DNS 三平台自建(Windows netsh / macOS networksetup / Linux resolvconf)
- [ ] Windows wintun.dll 分发(同目录)
- [ ] 端到端:`qmidial -dial -tun` → 系统 `ping 8.8.8.8` 走 4G 成功

---

## 七、风险

| 风险 | 缓解 |
|---|---|
| TUN 创建时序 vs configureNetwork | 先 CreateTUN 再 Connect;或 NoDial 手动配 |
| macOS utun 改名,configureNetwork 配错名 | `tunDev.Name()` 取实际名,设回 cfg.Device.NetInterface 后再 Connect |
| Windows DNS netsh 需要服务名/接口名匹配 | 用 TUN Name(),netsh 按名找 |
| 默认路由与主网卡冲突 | netcfg Windows 已用 gwmetric=1;Linux 加 metric |

---

## 八、相关文件

- `cmd/qmidial/main.go` —— 现有,扩展 -tun
- `internal/qmidatapath/bridge.go` —— 子计划 02,Bridge
- `third_party/quectel-qmi-go/manager/manager.go` —— configureNetwork(:3414)
- `third_party/quectel-qmi-go/netcfg/` —— SetIPAddress/AddDefaultRoute/SetMTU 可用,UpdateDNS 不足
