# sub-agent 调研报告 3:原生网卡方案(系统层出现网卡)

> Agent: NativeNIC_Research(scout)
> 耗时:11m37s
> 调研日期:2026-07-14

## 核心结论

要让应用像系统 VPN 一样被 macOS 网络策略、路由和 DNS resolver 正式管理,正确方案是 **NetworkExtension 的 Packet Tunnel Provider**;若应用通过 Developer ID 在 App Store 外分发,则应把 Provider 打包成 **Network Extension System Extension**。

必须澄清"原生网卡"有两种含义:

1. **像 VPN 一样的系统级网络连接**:系统设置能显示/开关,连接时有 utun,系统接管路由和 resolver。用 NEPacketTunnelProvider。
2. **像 Wi-Fi/USB Ethernet 一样的硬件网络端口**:出现在普通网络服务/硬件端口列表,可像 en0 那样设置 TCP/IP、DNS、服务顺序。需要 NetworkingDriverKit 的 Ethernet 驱动。

## 1. raw utun(wireguard/tun)vs NE utun 的区别

**内核数据面没有根本区别。** XNU 的 `utun_control` 会为连接它的客户端注册一个 BSD utun 接口;wireguard/tun 直接打开 `AF_SYSTEM/SYSPROTO_CONTROL` 并连接 `com.apple.net.utun_control`。两者活动时通常都表现为 `utunN`,都能承载三层 IP 包。

真正区别是**谁拥有控制面,以及系统收到什么会话元数据**:

| | raw wireguard/tun utun | NEPacketTunnelProvider utun |
|---|---|---|
| 接口创建 | Go 进程 open utun_control | 系统的 Network Extension session 创建 |
| 路由 | 手动 ifconfig/route | setTunnelNetworkSettings 原子提交 |
| DNS | 手动 networksetup/scutil(失败) | NEDNSSettings 随 session 绑定 |
| configd 识别 | ❌ 仅内核接口事件 | ✅ VPN session + DNS 声明 |
| 系统设置可见 | ❌ 不可见 | ✅ VPN 条目(可连接/断开) |

macOS 不是根据接口名 `utunN` 判断是否为系统 VPN,而是根据 Network Extension/VPN 会话及其设置声明来管理网络策略。raw utun 只触发内核接口事件;没有对应的 NE VPN 配置/会话,也没有可信的 DNS/路由声明。

## 2. 系统设置中能否看到

- **能作为 VPN 配置出现和管理**。`NETunnelProviderManager.localizedDescription` 是显示名称;用户首次保存配置时需授权,之后可在系统 VPN 设置中连接、断开或移除。
- **不能作为 Wi-Fi 式普通网络硬件服务管理**。用户不能在该 utun 项目里手动选择 DHCP、编辑接口地址、DNS、MTU、硬件介质。这些运行时参数由 Provider 的 `NEPacketTunnelNetworkSettings` 声明。

Tailscale 官方明确说明其 Network/System Extension GUI 版本可从 macOS VPN settings 管理,而 raw `tailscaled` utun 版本不可以。

## 3. 各工具中的表现

| 位置 | 表现 |
|---|---|
| 系统设置 > VPN | ✅ 出现为 VPN 条目(可连接/断开/状态),有 localizedDescription |
| 系统设置 > 网络(硬件端口) | ❌ 不出现(NE utun 不是硬件端口) |
| `ifconfig` | ✅ 出现 utunN(active 时) |
| `scutil --dns` | ✅ 出现 NE DNS resolver |
| `networksetup` | ❌ 不管理 NE utun(VPN service,非 hardware port) |

## 4. DNS resolver 独立性

`NEPacketTunnelNetworkSettings.dnsSettings` 接受 `NEDNSSettings`:

- `matchDomains = [""]`:声明为默认域,使所有 DNS 优先交给 VPN DNS
- 当 VPN 已成为网络 default route 时,DNS server 自动成为 default resolver
- **这正是当前 raw utun 缺失的能力**:不是简单把 DNS IP 写进文件,而是把 resolver 与活动 tunnel session、路由可达性及生命周期绑定
- Wi-Fi 关闭时,VPN session 仍存在,系统不会因 Wi-Fi inactive 丢掉隧道声明的 resolver

## 5. NetworkExtension 之外的方案

### NetworkingDriverKit + USBDriverKit(不推荐)

唯一接近"真正原生硬件网卡"的现代方案。但:
- Apple 明确 NetworkingDriverKit 当前**只支持 Ethernet**
- DJI 模组数据面是 QMI raw IP,非标准 Ethernet framing
- 没有公开的 DriverKit WWAN/QMI family
- 需 DriverKit/Networking/USB entitlements + C++ DriverKit + 复杂签名
- 为解决 DNS 而伪装 Ethernet 成本远高于 Packet Tunnel

### 内置 IPsec/IKEv2/L2TP/PPP

- IKEv2/IPsec 是系统管理 VPN,能获得系统 UI/路由/DNS,但要求 VPN server/protocol
- Personal VPN API 只支持 IKEv2/IPsec,不支持 PPTP/L2TP
- PPP/PPPoE 要求设备实际实现 PPP;QMI WDS + raw IP 不是 PPP

### raw utun + 更多 ioctl / SystemConfiguration 写入

**不可行。** wireguard/tun 已使用创建 utun 的标准底层机制;增加地址/flags/MTU/route ioctl 仍只是配置 BSD 接口,不能创建 NE VPN configuration/session。手工创建 SCNetworkService 对任意 utun 不是 Apple 支持的虚拟 VPN 注册路径,接口关闭重建时不稳定。

### 旧 KEXT

旧式虚拟网卡 KEXT 理论上能做到,但现代 macOS 已转向 System Extension/DriverKit;新产品不应选择 KEXT。

## 最终建议

采用 **NEPacketTunnelProvider + Network Extension System Extension**,不要把 raw utun 修补成 configd service,也不要为 DNS 问题上 DriverKit。

最小闭环:

1. Apple Developer 后台为主 App 与扩展 App ID 开启 Network Extension
2. Wails App 嵌入 `.systemextension`;启动时请求激活
3. `NETunnelProviderManager` 保存 "DJI 4G" VPN 配置
4. Provider 内调用 Go 核心,获得 IP/GW/DNS/MTU
5. 创建 `NEPacketTunnelNetworkSettings`,配置 IP/route/MTU/DNS,调 `setTunnelNetworkSettings`
6. 用 `packetFlow.readPackets` / `writePackets` 与 USB bulk relay 对接
7. 系统负责接口/路由/DNS 的安装和清理

用户看到的是系统认可的"DJI 4G VPN"(不是 Wi-Fi 式物理网卡),但满足断 Wi-Fi 后全局 IP 和 DNS 工作的目标。

## 参考资料

- [NEPacketTunnelProvider](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider)
- [setTunnelNetworkSettings](https://developer.apple.com/documentation/networkextension/netunnelprovider/settunnelnetworksettings(_:completionhandler:))
- [NETunnelProviderManager](https://developer.apple.com/documentation/networkextension/netunnelprovidermanager)
- [NEDNSSettings](https://developer.apple.com/documentation/networkextension/nednssettings)
- [TN3134](https://developer.apple.com/documentation/technotes/tn3134-network-extension-provider-deployment)
- [SystemExtensions](https://developer.apple.com/documentation/systemextensions)
- [Tailscale macOS variants](https://tailscale.com/docs/concepts/macos-variants)
- [wireguard-go tun_darwin.go](https://github.com/WireGuard/wireguard-go/blob/master/tun/tun_darwin.go)
- [XNU if_utun.c](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/if_utun.c)
- [NetworkingDriverKit](https://developer.apple.com/documentation/networkingdriverkit)
