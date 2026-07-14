# macOS feth 虚拟以太网 + SCDynamicStore DNS 方案深度调研

> 调研日期:2026-07-14
> 来源:ZeroTier macOS 实现 + cloudygreybeard/feth + XNU if_fake

## 核心发现:ZeroTier 已用 feth + SCDynamicStore 解决了完全相同的问题

ZeroTier(macOS 10.13+)**不用 NetworkExtension**,而是:
1. **feth 设备对**:创建虚拟以太网接口(数据面)
2. **SCDynamicStore DNS 注入**:注入 synthetic network service(DNS + IPv4 + IPv6)
3. **无需 Apple Developer 账号、无需签名、无需 entitlement**

## 1. feth 设备对(数据面)

### ZeroTier 的实现

ZeroTier 用 feth 对代替 utun:
- `feth0`(primary):系统可见,configd/IP 栈使用
- `feth5000`(peer):ZeroTier I/O(BPF 读 + NDRV 写)

数据流:
```
ZeroTier engine → NDRV 写帧 → feth(peer) → feth(primary) → 系统 IP 栈
系统 IP 栈 → feth(primary) → feth(peer) → BPF 捕获 → ZeroTier engine
```

### Go 实现(cloudygreybeard/feth)

已有 Go 库直接可用:
```go
import "github.com/cloudygreybeard/feth"

iface, _ := feth.New()  // 创建 feth 对
defer iface.Close()
iface.Up()
// iface.Read(buf) / iface.Write(buf) — Ethernet 帧
```

- macOS 10.13+(High Sierra)以上
- 需要 root(同当前 tun-helper)
- **不需要 KEXT、不需要 entitlement**
- Apache 2.0 许可

## 2. SCDynamicStore DNS 注入(关键 DNS 技巧)

这是 ZeroTier 的 MacDNSHelper 解决 DNS 的核心方法。**我们之前所有 workaround 失败的原因**:没有创建 synthetic network service。

### DNS 注入:setDNS

```objc
// 写 SCDynamicStore key:State:/Network/Service/<service_id>/DNS
SCDynamicStoreRef ds = SCDynamicStoreCreate(NULL, CFSTR("dji-4g"), NULL, NULL);

// DNS 字典
keys[0] = CFSTR("SupplementalMatchDomains");
keys[1] = CFSTR("ServerAddresses");
keys[2] = CFSTR("SearchDomains");

values[0] = [@[""]  array];              // 空字符串 = 全域
values[1] = @[@"<4G_DNS_1>", @"<4G_DNS_2>"];  // 4G DNS
values[2] = [@[""]  array];

SCDynamicStoreSetValue(ds,
    CFSTR("State:/Network/Service/dji-4g-desktop/DNS"),
    dict);
```

### IPv4 注入:addIps4(让 macOS 认为接口活跃)

ZeroTier 源码注释:
> "As of Monterey we need IPv4 set up too."
> "using the ip from the zerotier network breaks routing on the mac"

```objc
// 写 SCDynamicStore key:State:/Network/Service/<service_id>/IPv4
keys[0] = CFSTR("Addresses");        // [4G IP]
keys[1] = CFSTR("InterfaceName");    // feth 设备名
keys[2] = CFSTR("ServerAddress");    // "127.0.0.1"
keys[3] = CFSTR("Router");           // "127.0.0.1"(注意:不用真实 IP!)

SCDynamicStoreSetValue(ds,
    CFSTR("State:/Network/Service/dji-4g-desktop/IPv4"),
    dict);
```

### IPv6 注入:addIps6(让 macOS 有 IPv6 连通性)

ZeroTier 源码注释:
> "Make macOS believe we do in fact have ipv6 connectivity"
> "only the link-local address is necessary and sufficient"

```objc
// 写 SCDynamicStore key:State:/Network/Service/<service_id>/IPv6
// 只需 link-local 地址(MAC 生成)
keys[0] = CFSTR("Addresses");        // [fe80::...]
keys[1] = CFSTR("DestAddresses");
keys[2] = CFSTR("Flags");            // "0"
keys[3] = CFSTR("InterfaceName");    // feth 设备名
keys[4] = CFSTR("PrefixLength");     // "64"
```

### 为什么这个方法有效(而我们之前的方法无效)

| 方法 | 写入位置 | configd 行为 | 断 WiFi 后 |
|---|---|---|---|
| networksetup -setdnsservers | Wi-Fi service DNS | Wi-Fi inactive 时忽略 | ❌ |
| scutil set Global DNS | State:/Network/Global/DNS | 不合并到 resolver #1 | ❌ |
| /etc/resolver/default | supplemental resolver | 被 resolver #1 遮蔽 | ❌ |
| 本地 DNS proxy 127.0.0.1:53 | proxy 工作 | resolver 不路由到它 | ❌ |
| **SCDynamicStore synthetic service** | **State:/Network/Service/xxx/{DNS,IPv4,IPv6}** | **configd 创建独立 resolver** | **✅** |

**关键**:SCDynamicStore 的 `State:/Network/Service/<id>/` 创建了一个**独立的 network service**(有 DNS + IPv4 + IPv6),configd 为它创建独立的 resolver。即使 Wi-Fi 断了,这个 synthetic service 仍然活跃(DNS + IPv4 state 都在),resolver 保持 Reachable。

## 3. 完整架构(feth + SCDynamicStore)

```
┌─────────────────────────────────────────────────────┐
│  desktop app(Go/Wails)                               │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐  │
│  │ React UI │  │ USB/QMI  │  │ SCDynamicStore    │  │
│  │          │  │ dial+relay│  │ DNS/IP/IPv6 注入   │  │
│  └──────────┘  └────┬─────┘  └────────┬──────────┘  │
│                     │                  │              │
│                ┌────▼──────────────────▼────┐        │
│                │  feth 设备对(root)          │        │
│                │  feth0(primary):系统 IP 栈  │        │
│                │  feth1(peer):relay I/O     │        │
│                └────────────────────────────┘        │
└─────────────────────────────────────────────────────┘
                     │
              configd 识别 synthetic service
              (DNS + IPv4 + IPv6 State keys)
                     │
              ┌──────▼──────┐
              │ 系统 resolver │  ← 断 WiFi 也 Reachable
              │ <4G_DNS_1> │
              └─────────────┘
```

## 4. 与之前的方案对比

| | raw utun(当前) | NetworkExtension | **feth + SCDynamicStore** |
|---|---|---|---|
| DNS 管理 | ❌ | ✅ | ✅ |
| 断 WiFi | ❌ | ✅ | **✅(ZeroTier 验证)** |
| 系统设置可见 | ❌ | ✅ VPN | ⚠️ synthetic service |
| Apple Developer | ❌ 不需要 | ✅ **$99/年** | ❌ **不需要** |
| Swift bridge | ❌ 不需要 | ✅ 必须 | ❌ **不需要** |
| 签名/entitlement | ❌ 不需要 | ✅ 必须 | ❌ **不需要** |
| root 权限 | ✅ sudo | ❌ System Extension | ✅ sudo(同当前) |
| 数据面 | raw IP(utun) | raw IP(utun) | **Ethernet(feth)** |
| 复杂度 | 低 | 高 | **中** |

## 5. 需要的改动

### 数据面:raw IP → Ethernet 帧

feth 是 **Layer 2 Ethernet** 接口(不像 utun 是 Layer 3 raw IP)。现有 relay(USB ↔ raw IP)需要适配:
- 写入 feth 时:**加 Ethernet 头**(src/dst MAC + ethertype 0x0800)
- 从 feth 读时:**剥 Ethernet 头** → raw IP → relay → USB

这需要 ARP 响应(对 ARP 请求回复 feth 的 MAC),或用代理 ARP。

### DNS:Go 调用 SCDynamicStore

Go 通过 CGo 调用 SystemConfiguration framework:
```go
/*
#cgo LDFLAGS: -framework SystemConfiguration
#include <SystemConfiguration/SystemConfiguration.h>
*/
import "C"
```

注入 DNS/IPv4/IPv6 State keys(复刻 MacDNSHelper 逻辑)。

### tun-helper 改造

现有 `desktop/cmd/tun-helper/main.go` 改为:
1. 创建 feth 对(替代 utun)
2. QMI 拨号(不变)
3. SCDynamicStore 注入 synthetic service(DNS + IPv4 + IPv6)
4. Bridge:Ethernet 帧 ↔ raw IP ↔ USB relay

## 6. 风险

- feth 是 **undocumented**(但 ZeroTier 生产使用,XNU if_fake 稳定)
- Ethernet 帧处理需正确 ARP(否则上层 IP 不通)
- SCDynamicStore key 格式需精确匹配(否则 configd 不认)
- macOS 版本差异(Monterey+ 需 IPv4 + IPv6 state)
- 没有正式文档(参考 ZeroTier 源码 + XNU 内核源码)

## 7. 参考资料

- [ZeroTier MacEthernetTap.cpp](https://github.com/zerotier/ZeroTierOne/blob/d9a7f62a/osdep/MacEthernetTap.cpp) — feth 设备管理
- [ZeroTier MacEthernetTapAgent.c](https://github.com/zerotier/ZeroTierOne/blob/d9a7f62a/osdep/MacEthernetTapAgent.c) — BPF/NDRV I/O
- [ZeroTier MacDNSHelper.mm](https://github.com/zerotier/ZeroTierOne/blob/d9a7f62a/osdep/MacDNSHelper.mm) — SCDynamicStore DNS/IP 注入(核心)
- [ZeroTier macOS/BSD Implementation](https://deepwiki.com/zerotier/ZeroTierOne/5.7-macos-and-bsd-implementation) — 架构概述
- [cloudygreybeard/feth](https://github.com/cloudygreybeard/feth) — Go feth 库
- [XNU if_fake.c](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/net/if_fake.c) — 内核 feth 实现
- [srivatsp.com veth on MacOS](https://srivatsp.com/ostinato/macos-feth/) — feth 使用教程
