# macOS NetworkExtension 调研报告:TUN DNS 系统级管理

> 调研日期:2026-07-14
> 分支:`feature/desktop`
> 状态:**调研完成,待实施**

## 一、问题回顾

desktop TUN 模式(wireguard/tun Go 包创建 utun)断 WiFi 后:
- ✅ IP 直连通(curl IP → 百度 301)
- ✅ dig @<4G_DNS_1> 通(4G DNS 经 utun route → 解析成功)
- ❌ 系统 DNS resolver 失败(Could not resolve host)

**根因**:macOS configd 断 WiFi 后清除 `resolver #1`(全局默认)的 nameserver,用户态 utun(wireguard/tun 包)不是 configd 管理的 network service。所有 workaround(networksetup / scutil / /etc/resolver)均无效。

详见 AGENTS.md「探索性记录:macOS 断 WiFi 后 TUN DNS 问题」。

## 二、为什么 raw utun(wireguard/tun)不行

wireguard/tun Go 包的 macOS 实现(`tun_darwin.go`):
```
1. connect AF_SYSTEM / SYSPROTO_CONTROL "com.apple.net.utun_control"
2. 取得 utunN 接口名
3. ioctl 设 MTU
```

**仅此而已** —— 没有创建 NetworkExtension VPN 配置,没有 `setTunnelNetworkSettings` 事务。结果是:
- BSD 数据面(读写包)正常 → IP 直连通
- 但 **没有系统控制面**:configd 不知道这个 utun 是一个"受管的 network service"
- configd 的 DNS resolver 策略基于 network service 活跃状态,Wi-Fi 断了 → 所有 resolver 标 Not Reachable

**对比**:NetworkExtension 的 `NEPacketTunnelProvider` 创建的 utun **也是同一个内核 utun**(XNU `if_utun.c`),但通过 `setTunnelNetworkSettings` 原子提交 IP/路由/DNS,系统自动:
1. 创建活动 utun 接口
2. 安装路由
3. **发布 DNS resolver**(configd/mDNSResponder 获得受管 tunnel resolver)

## 三、NetworkExtension 如何解决 DNS

### 核心 API:setTunnelNetworkSettings

```swift
// NEPacketTunnelProvider 子类中
let settings = NEPacketTunnelNetworkSettings(
    tunnelRemoteAddress: "10.51.239.212"  // gateway
)
settings.ipv4Settings = NEIPv4Settings(
    addresses: ["10.51.239.211"],
    subnetMasks: ["255.0.0.0"]
)
settings.ipv4Settings.includedRoutes = [NEIPv4Route.default()]

// DNS — matchDomains=[""] = 默认全域 resolver
settings.dnsSettings = NEDNSSettings(servers: ["<4G_DNS_1>", "<4G_DNS_2>"])
settings.dnsSettings.matchDomains = [""]  // ← 关键:空字符串 = 默认域

settings.mtu = 1500

// 原子提交:系统自动创建 utun + 路由 + DNS resolver
setTunnelNetworkSettings(settings) { error in ... }
```

**为什么能解决断 WiFi DNS 问题**:
1. `setTunnelNetworkSettings` 是**系统级事务**,configd 把 tunnel 的 DNS 注册为活跃 resolver
2. `matchDomains=[""]` = 默认全域 → 成为 resolver #1(或高优先级)
3. tunnel active 期间,configd 始终用这个 DNS,**不受 Wi-Fi inactive 影响**

### 各方案对比(sub-agent NE_DNS_Research)

| Provider 类型 | DNS 能力 | 适用场景 |
|---|---|---|
| **NEPacketTunnelProvider** | ✅ setTunnelNetworkSettings + NEDNSSettings(全域/分域) | **系统全局 VPN**(本项目) |
| NEAppProxyProvider | ⚠️ 只能 per-app(MDM 配置) | 企业 per-app VPN |
| NETransparentProxyProvider | ⚠️ 同上 | 透明代理 |

**结论**:**NEPacketTunnelProvider** 是唯一适合"系统全局 TUN + DNS"的类型。

### 行业实践

| 工具 | NE 类型 | DNS 管理 |
|---|---|---|
| **Tailscale GUI** | NEPacketTunnelProvider(System Extension) | ✅ MagicDNS 自动管理 |
| **Tailscale CLI**(tailscaled) | raw utun(/dev/utun) | ❌ 官方明确不管理 DNS |
| **WireGuard-Apple** | NEPacketTunnelProvider(System Extension) | ✅ setTunnelNetworkSettings |
| **ClashX Pro** | 无 NE(仅 SMJobBless helper) | ⚠️ 非 NE 方案 |
| **Surge** | NEPacketTunnelProvider(推测) | ✅ VIF |

## 四、原生网卡:系统设置中可见

### NetworkExtension 创建的接口在系统中的表现

| 位置 | 表现 |
|---|---|
| **系统设置 > VPN** | ✅ 出现为 VPN 条目(可连接/断开/状态),有 localizedDescription |
| **系统设置 > 网络**(硬件端口) | ❌ 不出现(NE utun 不是硬件端口) |
| `ifconfig` | ✅ 出现 utunN(active 时) |
| `scutil --dns` | ✅ 出现 NE DNS resolver |
| `networksetup` | ❌ 一般不管理 NE utun(它是 VPN service,非 hardware port) |

**用户期望**:启动 app 后系统层出现一个"网卡"。NE 的实现是**VPN 条目**(系统设置 > VPN),不是 Wi-Fi 式硬件端口。这是 macOS 的设计 —— 虚拟接口归 VPN,硬件端口归 networksetup。

### 另一条路:NetworkingDriverKit(不推荐)

`IOUserNetworkEthernet`(DriverKit)能创建真正的 enX 硬件式网卡,但:
- 仅 Ethernet 类型(非 raw-IP TUN)
- 需硬件匹配(USB/PCI)+ DriverKit entitlements
- 复杂度远超 NetworkExtension
- **不适合本项目**(我们是 raw-IP relay,非 Ethernet)

## 五、Go 实现架构

### 推荐架构(Swift bridge + Go c-archive)

sub-agent GoNE_Bridge 的核心结论:**gomobile 不可用**(不支持 completion block,NE 核心签名都有)。推荐手写 Swift wrapper + Go c-archive。

```
┌─────────────────────────────────────────────────┐
│  Wails 主应用(Go)                                │
│  ┌──────────┐  ┌──────────┐  ┌───────────────┐  │
│  │ React UI │  │ USB/QMI  │  │ NE 激活控制    │  │
│  │          │  │ dial+relay│  │ (启动/停止VPN) │  │
│  └──────────┘  └──────────┘  └───────┬───────┘  │
└──────────────────────────────────────┼──────────┘
                                       │ CGo
                        ┌──────────────▼──────────────┐
                        │  Go c-archive (数据面)        │
                        │  qmitransport + qmidatapath   │
                        │  + relay (raw IP ↔ USB)       │
                        └──────────────┬───────────────┘
                                       │ C ABI (FD 传递)
                ┌──────────────────────▼───────────────┐
                │  System Extension (Swift)             │
                │  ┌───────────────────────────────┐    │
                │  │ PacketTunnelProvider.swift     │    │
                │  │  - setTunnelNetworkSettings     │    │
                │  │    (IP/route/DNS)              │    │
                │  │  - 扫描 NE utun FD             │    │
                │  │  - 调 Go c-archive (数据 relay)  │    │
                │  └───────────────────────────────┘    │
                └───────────────────────────────────────┘
```

### WireGuard-Apple 的实现方式(参考)

1. `PacketTunnelProvider.swift` 继承 `NEPacketTunnelProvider`
2. Swift 调 `setTunnelNetworkSettings`(IP/route/DNS)
3. 扫描 NE 创建的 utun,获取 FD
4. 把 FD 传给 `go build -buildmode=c-archive` 导出的 `wgTurnOn(fd)`
5. Go 侧拿到 utun FD → 读写包(数据面)

**本项目可复用同样模式**:Swift NE 设 DNS/route → 传 utun FD 给 Go → Go 用现有 relay(USB ↔ utun FD)。

### 不推荐 gomobile

sub-agent GoNE_Bridge 实测:
- gomobile reverse ObjC binding **不支持 block**(completion handler)
- NE 的 `startTunnel` / `setTunnelNetworkSettings` 核心签名都有 completion block
- gomobile importer 硬编码 iphonesimulator SDK(实务不可用)
- **推荐:手写 Swift wrapper + 窄 C ABI c-archive**

## 六、编译 / 签名 / 分发

### TN3134 关键规则

| 分发方式 | Extension 类型 | 要求 |
|---|---|---|
| **Mac App Store** | .appex(Network Extension) | App Store provisioning |
| **Developer ID / 官网直发** | .sysex(**System Extension**, macOS 10.15+) | Developer ID + provisioning + notarize |

本项目走 **Developer ID 直发**(非 App Store)→ 必须打 **System Extension**(.sysex)。

### 需要的 Apple 资质

1. **Apple Developer Program**($99/年)
2. **Entitlements**:
   - `com.apple.developer.networking.networkextension`(Packet Tunnel)
   - `com.apple.developer.system-extension.install`(System Extension 激活)
3. **Provisioning Profile**(Network Extension + System Extension)
4. **Developer ID 签名 + Notarization**

### 编译流程

```bash
# 1. Go c-archive(数据面)
go build -buildmode=c-archive -o librelay.a ./cmd/relay-lib/

# 2. System Extension(Swift,xcodebuild)
xcodebuild -project DJI4GDesktop.xcodeproj \
  -scheme DJI4GTunnel \
  -configuration Release \
  -archivePath build/DJI4GTunnel.xcarchive \
  archive

# 3. 主应用(嵌入 .syxec + librelay.a)
xcodebuild ... -exportArchive ...

# 4. 签名 + Notarize
codesign --deep --sign "Developer ID..." DJI4GDesktop.app
xcrun notarytool submit DJI4GDesktop.zip --apple-id ... --wait
```

## 七、技术选型建议

### 推荐:NEPacketTunnelProvider(System Extension)

**方案**:
- macOS 用 `NEPacketTunnelProvider`(.sysex System Extension)替代 wireguard/tun raw utun
- Swift wrapper 负责控制面(setTunnelNetworkSettings:IP/route/DNS)
- Go c-archive 负责数据面(relay:utun FD ↔ USB bulk EP)
- 主应用(Go/Wails)通过 OSSystemExtensionRequest 激活扩展

**优势**:
- ✅ 系统级 DNS(configd 管理,断 WiFi 也通)
- ✅ 系统设置 > VPN 可见(原生网卡表现)
- ✅ 原子路由/DNS/MTU(setTunnelNetworkSettings)
- ✅ WireGuard-Apple 验证过的模式

**代价**:
- ⚠️ 需要 Apple Developer Program($99/年)
- ⚠️ Swift bridge 开发(PacketTunnelProvider.swift)
- ⚠️ xcodebuild 编译链(Go + Swift 混合)
- ⚠️ 签名/Notarize 流程

### 不推荐:继续 raw utun workaround

所有 workaround 均已证明无效(AGENTS.md 记录)。raw utun 的 DNS 是 macOS 系统限制,无法用用户态工具绕过。

### 分阶段实施建议

| 阶段 | 范围 | 前置 |
|---|---|---|
| **0. 资质** | Apple Developer + entitlements 申请 | $99/年 |
| **1. Swift NE 骨架** | PacketTunnelProvider.swift(hardcoded IP/DNS)→ setTunnelNetworkSettings → 验证系统 DNS | xcodebuild |
| **2. Go c-archive 数据面** | 现有 relay 提取为 c-archive,Swift 传 utun FD → Go relay | Go c-archive |
| **3. Wails 集成** | 主应用激活 System Extension + 传参(APN → dial → NE) | OSSystemExtensionRequest |
| **4. 端到端** | 断 WiFi → DNS 通 → curl/浏览器/Clash 全通 | 全链路 |

## 八、Windows 侧说明

Windows 不受此问题影响:
- Windows 的 TUN(wintun.dll)是系统级驱动,DNS 通过 `netsh interface ip set dns` 或 Windows API 设置
- 断 WiFi 后 Windows DNS resolver 行为不同(不像 macOS configd 清除 resolver)
- 当前 `cmd/qmidial -tun` 在 Windows 验证过(curl + DNS + ping 全通)
- **NetworkExtension 是 macOS 特定**,Windows 侧保持现有 wireguard/tun + netsh 方案

## 九、结论

macOS TUN DNS 问题的**唯一系统级解决方案**是 `NEPacketTunnelProvider`(System Extension)。WireGuard-Apple 已验证 Go + Swift bridge 架构可行。

**下一步决策点**:
1. 是否申请 Apple Developer Program($99/年)?
2. 是否接受 Swift bridge 开发(PacketTunnelProvider.swift)?
3. 首期只做 macOS,还是 Windows 也同步?

确认后进入阶段 0(资质申请)→ 阶段 1(Swift NE 骨架)。
