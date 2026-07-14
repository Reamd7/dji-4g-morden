# sub-agent 调研报告 1:NetworkExtension DNS 管理机制

> Agent: NE_DNS_Research(scout)
> 耗时:25m15s
> 调研日期:2026-07-14

## 核心结论

应采用 `NEPacketTunnelProvider`,并在 macOS 直接分发版本中把它打包为 Network System Extension。关键不是"创建一个名字同样叫 utun 的接口",而是让虚拟接口由 macOS 的 NetworkExtension VPN 会话创建,并通过 `setTunnelNetworkSettings` 把 IP、路由和 DNS 作为同一组会话设置提交给系统。

当前 `wireguard/tun` 直接打开 `com.apple.net.utun_control` 只完成了数据面:内核中有 utun、IP 路由也可以工作,但没有 `NETunnelProviderManager` 配置、活动 VPN session 和 `setTunnelNetworkSettings` 控制面。因此 SystemConfiguration/configd 与 mDNSResponder 没有一个受支持的、生命周期绑定到该 utun 的 DNS 配置来源;Wi-Fi service inactive 后,它们会撤销由 Wi-Fi 提供的默认 resolver,而不会自动把裸 utun 当成新的 DNS service。

严格来说,NetworkExtension 并不是把 utun 转换为一个普通、用户可编辑的 `SCNetworkService`。它把它注册为一个由系统管理的 VPN 配置与活动 tunnel session;NetworkExtension 守护进程负责创建 utun、应用设置并把 DNS resolver 状态发布给系统解析栈。[INFERENCE] Apple 没有公开 configd/nesessionmanager/mDNSResponder 的内部动态存储协议,因此不应依赖具体 `State:/Network/Service/...` 路径或 resolver 编号,应该依赖公开的 `setTunnelNetworkSettings` 行为。

## NEPacketTunnelProvider 如何设置 DNS

Provider 启动流程:

1. 主应用通过 `NETunnelProviderManager` 保存并启用 VPN 配置。
2. 系统启动 Packet Tunnel Provider,并创建其 utun。
3. Provider 构造 `NEPacketTunnelNetworkSettings`。
4. 设置 `ipv4Settings` / `ipv6Settings`、included/excluded routes、MTU 和 `dnsSettings`。
5. 调用 `setTunnelNetworkSettings(_:completionHandler:)`;成功后再完成 `startTunnel`。

明文 DNS 示例:

```swift
let settings = NEPacketTunnelNetworkSettings(
    tunnelRemoteAddress: "127.0.0.1"
)

let dns = NEDNSSettings(servers: ["<4G DNS IPv4>"])
dns.matchDomains = [""]       // 空字符串代表 default domain
settings.dnsSettings = dns

let ipv4 = NEIPv4Settings(
    addresses: ["<utun local IP>"],
    subnetMasks: ["255.255.255.255"]
)
ipv4.includedRoutes = [NEIPv4Route.default()]
settings.ipv4Settings = ipv4

setTunnelNetworkSettings(settings) { error in
    completionHandler(error)
}
```

`NEDNSSettings` 默认协议是 cleartext DNS。4G 模组/QMI 下发的运营商 DNS 通常是 UDP/TCP 53,因此应优先使用它,而不是无条件换成 DoT。

### matchDomains 的关键语义

- 指定域名列表:split DNS,仅匹配域走 tunnel DNS。
- 包含空字符串 `""`:该 resolver 成为 default domain,所有 DNS 查询优先使用 VPN DNS。
- 如果 VPN tunnel 已成为网络 default route,`NEDNSSettings.servers` 自动成为 default resolver,`matchDomains` 会被忽略。

WireGuard-Apple 的生产代码正是:

```swift
let dnsSettings = NEDNSSettings(servers: dnsServerStrings)
dnsSettings.matchDomains = [""]
networkSettings.dnsSettings = dnsSettings
```

### DNS 服务器的路由同样必须正确

注册 resolver 不等于 DNS 服务器一定可达。如果 4G DNS 只可经 DJI/QMI relay 到达,必须保证 DNS server 地址被 included route 覆盖:

- 全隧道:使用 `0.0.0.0/0`(以及需要时 `::/0`)。
- 分隧道:至少增加 DNS IPv4 `/32`、IPv6 `/128` route。
- 评估 `NETunnelProviderProtocol.enforceRoutes = true`。

## NEAppProxyProvider 为何不适合本项目

`NEAppProxyProvider` 虽然也能设 DNS,但:
- 只能处理匹配 per-app 规则的 socket flows,不是系统所有 IP packets
- 配置只能来自 MDM per-app VPN
- 不能解决"整机默认路由通过 USB 4G"的需求

## 行业调查

| 工具 | NE 类型 | DNS 管理 |
|---|---|---|
| WireGuard-Apple | NEPacketTunnelProvider | ✅ setTunnelNetworkSettings + NEDNSSettings |
| Tailscale GUI | NEPacketTunnelProvider(System/App Extension) | ✅ MagicDNS 自动 |
| Tailscale CLI(tailscaled) | raw utun | ❌ 官方明确不管理 DNS |
| ClashX Pro | 无 NE(仅 SMJobBless helper) | ⚠️ 非 NE 方案 |
| Surge Mac | Surge VIF(具体 NE 类型未公开确认) | ✅ VIF |

## SystemExtension 与 NetworkExtension 区别

这两个概念不是竞争方案:

- **NetworkExtension**:网络 API/framework 与 provider 类型(NEPacketTunnelProvider 等)
- **SystemExtension**:macOS 上承载 provider 的包装/安装/运行上下文

同一个 Packet Tunnel provider 在 macOS 可以打包为:

| 包装 | 最低 macOS | 分发 | 运行上下文 |
|---|---:|---|---|
| App Extension (.appex) | 10.11 | App Store only | 用户上下文 |
| System Extension (.sysex) | 10.15 | Developer ID 直发 | 全局上下文 |

## 方案对比

| 方案 | 管理 L3 虚拟接口 | 系统级 DNS | 断 Wi-Fi 预期 | 结论 |
|---|---|---|---|---|
| 裸 wireguard/tun utun | 数据面而已 | 无受支持的 session DNS | Wi-Fi resolver 消失后易失败 | 当前问题根源 |
| PacketTunnel App Extension | 是 | NEDNSSettings 与 tunnel 绑定 | 正确配置时可继续 | App Store 方案 |
| PacketTunnel System Extension | 是 | 同上 | 正确配置时可继续 | **首选** |
| AppProxy | flow/per-app | 仅匹配 app | 不能解决整机默认 DNS | 不适用 |
| DNS Proxy System Extension | 不管理 utun | 可接管所有 DNS query | 可独立于 Wi-Fi | 备选,但复杂度高 |

## 参考资料

- [NEPacketTunnelProvider](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider)
- [setTunnelNetworkSettings](https://developer.apple.com/documentation/networkextension/netunnelprovider/settunnelnetworksettings(_:completionhandler:))
- [NEDNSSettings](https://developer.apple.com/documentation/networkextension/nednssettings)
- [TN3134: Network Extension Provider Deployment](https://developer.apple.com/documentation/technotes/tn3134-network-extension-provider-deployment)
- [WireGuard-Apple PacketTunnelProvider.swift](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardNetworkExtension/PacketTunnelProvider.swift)
- [WireGuard-Apple PacketTunnelSettingsGenerator.swift](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardKit/PacketTunnelSettingsGenerator.swift)
- [Tailscale macOS variants](https://tailscale.com/docs/concepts/macos-variants)
- [Tailscale tailscaled on macOS](https://github.com/tailscale/tailscale/wiki/Tailscaled-on-macOS)
