# sub-agent 调研报告 2:Go 实现 NetworkExtension 可行性 + Swift Bridge

> Agent: GoNE_Bridge(scout)
> 耗时:~30min
> 调研日期:2026-07-14

## 核心结论

可行,但推荐方案不是"纯 Go 实现 NetworkExtension",而是:**原生 Swift/Objective-C 负责 Apple 框架边界和扩展生命周期,Go 负责数据面与业务逻辑。**

## 推荐架构

```
Wails/Go 主程序
  ├─ UI、USB 探测、VPN 配置控制
  ├─ Swift/ObjC bridge: NETunnelProviderManager
  └─ 激活/启动 Packet Tunnel System Extension

PacketTunnel System Extension (Swift)
  ├─ NEPacketTunnelProvider 子类
  ├─ setTunnelNetworkSettings(IP/route/DNS/MTU)
  ├─ 获取 NE 创建的 utun FD
  └─ 调用 Go c-archive 数据面(utun FD ↔ USB relay)
```

## 1. wireguard-apple 的实现方式(最直接参考)

wireguard-apple 的结构:

- `PacketTunnelProvider.swift` 继承 `NEPacketTunnelProvider`
- Swift 在 `startTunnel` 中解析配置并调用 `WireGuardAdapter.start`
- `PacketTunnelSettingsGenerator.swift` 构造 `NEPacketTunnelNetworkSettings`(IP/routes/DNS/MTU)
- Swift 调用 `setTunnelNetworkSettings`,让 macOS 管理该 tunnel 的 DNS 和路由
- NetworkExtension 创建 utun 后,`WireGuardAdapter.swift` 识别 `com.apple.net.utun_control` FD
- Swift 调用 C ABI `wgTurnOn(config, tunFd)`
- `api-apple.go` 通过 cgo `//export` 导出 C 函数;Go 用 `tun.CreateTUNFromFile` 接管传入的 utun FD
- Go 部分使用 `go build -buildmode=c-archive` 编译成 `libwg-go.a`,由 Swift/Xcode 链接

**wireguard-apple 采用 Swift NetworkExtension 外壳 + 手写 C ABI + Go 静态库,而不是用 Go/gomobile 直接实现 NEPacketTunnelProvider。**

## 2. Go 能否直接实现 NEPacketTunnelProvider

### 不应采用纯 Go principal class

Apple 要求扩展 principal class:
- 是 `NEPacketTunnelProvider` 的子类
- 覆盖 `startTunnel(options:completionHandler:)`
- 覆盖 `stopTunnel(with:completionHandler:)`
- 由扩展 `Info.plist` 的 `NSExtensionPrincipalClass` 指定

Go 本身没有 Apple Objective-C 类继承模型。自行用 ObjC runtime 动态注册类、拼接 method IMP、处理 block ABI 相当于实现不受支持的 ObjC bridge,风险远高于写几十到几百行 Swift。

### Objective-C + cgo 可选,但仍是原生 wrapper

可以不用 Swift,改用 `PacketTunnelProvider.m`(ObjC 继承 NEPacketTunnelProvider)。与 Swift bridge 本质相同,但 Swift 对现代 NetworkExtension API 更自然。

## 3. gomobile 为何不可用

### gomobile reverse binding 限制

1. **Objective-C importer 的类型模型不支持 block 类型**。当前解析器支持 class/protocol/string/NSData/整数/浮点数,但没有 block。
2. `startTunnel`、`stopTunnel`、`setTunnelNetworkSettings` 核心签名都有 completion block。
3. reverse ObjC importer 硬编码使用 `iphonesimulator` SDK(macOS 不是成熟路径)。
4. Go 团队声明 iOS reverse bindings 从未发展到覆盖大量原生 API。
5. x/mobile 标为 experimental,无最终用户支持保证。

**不要用 gomobile 直接实现 NEPacketTunnelProvider。**

## 4. 推荐 Go 静态库 ABI

Go 静态库提供很窄的 C ABI:

```c
typedef void (*dji_log_fn)(void *ctx, int level, const char *message);

void dji_set_logger(void *ctx, dji_log_fn fn);
int32_t dji_tunnel_start(const char *config_json, int32_t tun_fd);
void dji_tunnel_stop(int32_t handle);
char *dji_tunnel_status(int32_t handle);
void dji_tunnel_free_string(char *value);
```

Go 侧:

```go
package main

// #include "bridge.h"
import "C"

//export dji_tunnel_start
func dji_tunnel_start(config *C.char, tunFD C.int32_t) C.int32_t {
    // dup FD
    // unix.SetNonblock
    // tun.CreateTUNFromFile(os.NewFile(...), mtu)
    // 启动 QMI + USB bulk relay
}

func main() {}
```

## 5. 对现有代码的迁移含义

当前 `desktop/cmd/tun-helper/main.go` 迁移后应改为:
- 不再由 Go 自行创建 utun(删除 `tun.CreateTUN`)
- utun 由 NetworkExtension 创建和管理
- Swift 把已存在的 utun FD 传给 Go
- Go 通过 `tun.CreateTUNFromFile` 包装该 FD
- 删除 macOS `networksetup`、`scutil`、`/etc/resolver` DNS workaround
- DNS 由 `NEPacketTunnelNetworkSettings.dnsSettings` 设置
- 保留现有 `qmidatapath.Bridge`,生命周期移入扩展内的 Go library

### 必须单独验证的风险

当前 QMI/USB 使用 gousb/libusb;Network/System Extension 的 sandbox、USB device entitlement 是否允许现有实现直接运行,需做实际签名后的硬件 PoC。如果 USB 必须保留在另一个进程,则不能直接复用 extension 内 utun FD,需通过 IPC/FD 传递(增加复杂度)。首选仍把 Go 数据面放进 provider 进程。

## 6. 编译流程

### Go → C archive

```sh
SDKROOT="$(xcrun --sdk macosx --show-sdk-path)"
CC="$(xcrun --sdk macosx --find clang)"

CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
CC="$CC" \
CGO_CFLAGS="-isysroot $SDKROOT -arch arm64 -mmacosx-version-min=12.0" \
CGO_LDFLAGS="-isysroot $SDKROOT -arch arm64 -mmacosx-version-min=12.0" \
go build -trimpath -buildmode=c-archive -o build/arm64/libdji-tunnel-go.a ./native/tunnelgo
```

### Swift/System Extension → xcodebuild

```sh
xcodebuild \
  -project DJI4G.xcodeproj \
  -scheme DJI4G \
  -configuration Release \
  -destination 'generic/platform=macOS' \
  -archivePath build/DJI4G.xcarchive \
  DEVELOPMENT_TEAM="$TEAM_ID" \
  archive
```

不需要 Xcode GUI。可用 XcodeGen 从 YAML 生成 project。

### TN3134 分发约束

- `.appex` app extension:**仅 Mac App Store**
- `.systemextension`:**Developer ID 直发**(macOS 10.15+)

Developer ID 直发需要:
- `com.apple.developer.system-extension.install`
- `OSSystemExtensionRequest` 激活
- 嵌套签名
- Notarization

## 7. 复杂度评估

| 方案 | 可行性 | 初始复杂度 | 长期风险 | 建议 |
|---|---:|---:|---:|---|
| Swift NEPacketTunnelProvider + 手写 C ABI + Go c-archive | 高 | 中高 | 中 | **推荐** |
| ObjC provider + 手写 C ABI + Go c-archive | 高 | 中高 | 中 | 可选 |
| Swift provider + gomobile bind Go library | 中 | 中高 | 中高 | 不推荐(block 限制) |
| gomobile reverse binding 继承 NEPacketTunnelProvider | 低 | 很高 | 很高 | **不推荐** |
| 纯 Go + ObjC runtime 动态注册 | 理论可行 | 极高 | 极高 | **不采用** |

## 最终建议

1. 复制 wireguard-apple 的技术边界(不是协议逻辑)
2. 新增很薄的 Swift Packet Tunnel Provider
3. Swift 负责 DNS、路由、地址、MTU 和 completion handler
4. 现有 QMI/USB/relay 重构为 c-archive Go library,接收 NE utun FD
5. 主程序用 Swift bridge 管理 NETunnelProviderManager / OSSystemExtensionRequest
6. Taskfile 调度 go build 与 xcodebuild
7. 第一阶段 PoC 必须验证三件事:
   - 带真实 entitlement 的 provider 能设全局 DNS
   - Go library 能接管 NE utun FD
   - 现有 gousb/QMI 在 System Extension sandbox 内能否访问 DJI 模组

**语言桥接本身是成熟可行的;真正高复杂度在 Apple extension packaging、entitlement/provisioning/signing,以及 USB 访问在 extension sandbox 内的适配。**

## 参考资料

- [WireGuard-Apple PacketTunnelProvider.swift](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardNetworkExtension/PacketTunnelProvider.swift)
- [WireGuard-Apple WireGuardAdapter.swift](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardKit/WireGuardAdapter.swift)
- [WireGuard-Apple PacketTunnelSettingsGenerator.swift](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardKit/PacketTunnelSettingsGenerator.swift)
- [WireGuard-Apple api-apple.go](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardKitGo/api-apple.go)
- [WireGuard-Apple Makefile](https://github.com/WireGuard/wireguard-apple/blob/master/Sources/WireGuardKitGo/Makefile)
- [gomobile objc importer](https://github.com/golang/mobile/blob/master/internal/importers/objc/objc.go)
- [TN3134](https://developer.apple.com/documentation/technotes/tn3134-network-extension-provider-deployment)
- [OSSystemExtensionRequest](https://developer.apple.com/documentation/systemextensions/ossystemextensionrequest)
