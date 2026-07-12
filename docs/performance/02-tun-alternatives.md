# 不用 TUN 的替代方案调研

> 调研日期: 2026-07-13
> 问题: QMI 拨号获取 raw IP 包后,除了 TUN 还有其他方式把数据送入 OS TCP/IP 栈吗?

## 核心问题

当前架构的数据路径:
```
App → OS TCP/IP → TUN(内核) → relay(用户态) → USB bulk → modem
                    ↑
               需要 admin + 平台特异代码(Wintun/utun/tun)
```

TUN 的痛点:
- **需要管理员权限**(创建 TUN + 改路由表)
- **平台差异大**(Wintun/utun/tun 三套代码 + DLL)
- **全流量劫持**(TUN 把所有流量都导入,需要处理主网络共存)
- **macOS utun 每次 Close 重建**(N 递增)

## 四种替代方案

### 方案 A:gVisor netstack + SOCKS5 代理(★ 推荐)

**原理**: 用 Google gVisor 的纯 Go 用户态 TCP/IP 栈(netstack),link layer 用 **Go channel** 而非 TUN。上层开一个 SOCKS5 代理,App 通过代理上网。

```
App (浏览器/curl) → SOCKS5 localhost:1080
                          ↕
                    gVisor netstack(纯 Go TCP/IP 栈)
                    - TCP 状态机(重传/拥塞控制/窗口)
                    - UDP
                    - ICMP(内部 ping)
                    - DNS resolver
                          ↕  Go channel(零拷贝)
                    QMI relay(已有)
                          ↕
                    USB bulk EP 0x05/0x88
                          ↕
                    Modem → 4G → Internet
```

**关键:gVisor netstack 的 link layer 可以是 Go channel,不是 TUN。** 原文(https://gvisor.dev/docs/architecture_guide/networking/):
> "Netstack supports a variety of underlying link layers. Currently supported link layers include AF_PACKET sockets, AF_XDP sockets, shared memory, and **Go channels**."
> "Netstack aims to be usable independent of gVisor."

Go 包: `gvisor.dev/gvisor/pkg/tcpip`

**实现步骤**:
1. QMI 拨号(已有)→ 获取 IP/GW/DNS
2. 创建 netstack instance,配置 IP 地址,link layer = channel endpoint
3. DL: USB bulk IN → `channel.InjectPacket()` → netstack → SOCKS5 handler
4. UL: SOCKS5 handler → netstack → `channel.ReadPacket()` → USB bulk OUT
5. SOCKS5 server 监听 `localhost:1080`

**代码量估算**:
- netstack 初始化 + channel link layer: ~150 行
- SOCKS5 server: ~100 行(`armon/go-socks5` 库或手写)
- DNS resolver(用 QMI DNS 或 114.114.114.114): ~30 行
- 总计: **~300 行新代码** + 已有的 QMI transport/datapath

| 维度 | TUN(当前) | gVisor netstack |
|---|---|---|
| 管理员权限 | ✅ 必须 | ❌ 不需要 |
| 平台特异代码 | ✅ Wintun/utun/tun | ❌ 纯 Go,零平台代码 |
| 透明度 | ✅ 所有流量自动走 | ❌ 需配置 SOCKS5(浏览器/curl 原生支持) |
| ICMP(ping) | ✅ 外部可达 | ❌ 仅 netstack 内部(外部 ping 不经代理) |
| 性能 | ✅ 内核 TCP/IP | ❌ 用户态 TCP(重传/拥塞控制开销) |
| 跨平台一致性 | ❌ 三套 TUN 实现 | ✅ 完全统一 |
| 依赖大小 | wireguard/tun(~小) | gvisor netstack(~大但纯 Go) |
| 主网络共存 | ❌ 需 source-routing | ✅ 天然隔离(SOCKS5 显式选择) |
| API 稳定性 | ✅ 稳定 | ⚠️ gVisor 不保证 module 版本稳定 |

**最大优势**: 不需要管理员权限 + 跨平台完全统一 + 天然隔离。
**最大劣势**: 不透明(App 需配置 SOCKS5);用户态 TCP 性能不如内核。

**适用场景**: 只需要特定应用走 4G(浏览器、API 调用),不需要全系统透明上网的场景。

### 方案 B:AT Socket 模式(QIOPEN/QISEND)

**原理**: 模块固件自带 TCP/IP 栈。用 AT 命令直接在模块上开 TCP/UDP socket,数据通过 AT 接口收发。

```
App → SOCKS5 proxy → AT+QIOPEN("TCP","example.com",80)
                         → AT+QISEND(data)
                         ← AT+QIRD(data)
```

**AT 命令流程**:
```
AT+QICSGP=1,"APN"                        // 配置 APN
AT+QIOPEN=1,0,"TCP","example.com",80,0,0 // 打开 TCP socket
OK
+QIOPEN: 0,0                              // 连接成功,connectID=0

AT+QISEND=0                               // 发送数据
> GET / HTTP/1.1\r\nHost: example.com     // 数据
OK
SEND OK

AT+QIRD=0,0                              // 读数据
+QIRD: 1024                              // 1024 bytes received
<HTTP response data>

AT+QICLOSE=0                             // 关闭 socket
OK
```

**代码量**: ~200 行(AT 命令封装 + SOCKS5 server)

| 维度 | 评估 |
|---|---|
| 管理员权限 | ❌ 不需要 |
| 透明度 | ❌ SOCKS5 only |
| ICMP | ❌ 不支持 |
| 性能 | ❌ 每次数据收发需 AT 命令往返,延迟高(~100ms+) |
| 并发 socket | ⚠️ 通常限制 ~12 个 |
| DNS | ⚠️ 需 AT+QIDNSGPIP,不支持 DoH |
| QMI 拨号 | ❌ 不需要(模块自己管 PS 附着) |

**劣势**: AT 命令每次发送都有握手开销(`>` 提示符 → 数据 → Ctrl-Z),吞吐量受限于 AT 接口效率。QDC507 是否支持 QIOPEN 需实测(EC25 标准 PID 支持,DJI 私有 PID 未知)。

**适用场景**: 极低带宽需求(如 IoT 传感器上报),或仅需偶尔 HTTP 请求。

### 方案 C:MBIM 模式(让 OS 全权接管)

**原理**: `AT+QCFG="usbnet",2` 切到 MBIM 模式,模块变成标准 USB MBIM 设备。Windows/macOS 内置 WWAN 栈自动接管,零代码。

| 维度 | 评估 |
|---|---|
| 代码量 | **0**(OS 全做) |
| 性能 | ✅ 最佳(内核数据路径) |
| 透明度 | ✅ 完全透明(OS 原生管理) |
| AT 控制 | ❌ **丢失 AT 口**(无法收发短信) |
| QMI 控制 | ❌ **丢失 QMI 口** |
| 跨平台 | ❌ 各平台 WWAN 栈不同 |
| 本项目兼容 | ❌ 完全偏离用户态驱动初衷 |

**致命问题**: MBIM 模式下模块重新枚举,5 个串口接口消失,AT 和 QMI 都不可用。这等于放弃了阶段 1(短信收发)的全部成果。

**结论**: 只在"仅需要上网,完全不需要 AT/SMS"时考虑。

### 方案 D:保持 TUN(当前方案,已验证)

已完整实现并验证:
- Windows: Wintun + 路由 + DNS,源地址绑定 + 全局双 phase 测试通过
- macOS: utun + split 路由,三阶段验证通过
- 代码量: 已完成(internal/qmidatapath/ relay + cmd/qmidial/ TUN 集成)

## 方案对比总表

| 维度 | A. gVisor netstack | B. AT Socket | C. MBIM | D. 当前 TUN |
|---|---|---|---|---|
| 需要 admin | ❌ | ❌ | ❌ | ✅ |
| 透明上网 | ❌ SOCKS5 | ❌ SOCKS5 | ✅ | ✅ |
| TCP/UDP | ✅ | ✅ | ✅ | ✅ |
| ICMP/ping | ⚠️ 内部 | ❌ | ✅ | ✅ |
| 性能 | ○ 用户态 TCP | ❌ AT 开销 | ✅ 内核 | ✅ 内核 |
| 跨平台 | ✅ 纯 Go | ✅ AT 命令 | ❌ | ⚠️ 三套 TUN |
| 需要 QMI | ✅ 已有 | ❌ 不需要 | ❌ | ✅ 已有 |
| AT/SMS 并存 | ✅ MI_02 不受影响 | ✅ 共用 AT 口 | ❌ 丢失 | ✅ MI_02 不受影响 |
| 代码量 | ~300 行新 | ~200 行新 | 0 | 已完成 |
| 依赖 | gVisor netstack(大) | 无 | 无 | wireguard/tun(小) |

## 推荐策略

### 首选:混合模式(TUN + netstack 双后端)

不互斥,可以共存为可选后端:

```
qmidial -dial -tun         # 当前: 全局透明上网(需 admin)
qmidial -dial -socks5      # 新增: SOCKS5 代理(无需 admin)
```

**TUN 后端**(已有):适合需要全系统透明上网的场景(所有 App 自动走 4G)。

**netstack + SOCKS5 后端**(新增):适合:
- **macOS 开发环境**: 不想每次 sudo,只让浏览器走 4G
- **服务器/NAS**: 不想改路由表影响其他服务
- **CI/测试**: 无需 admin 的快速验证
- **IoT/嵌入式**: 只需 HTTP API 调用

两者共享同一个 QMI transport + datapath,**差异仅在最后一跳**(TUN device vs netstack channel)。

### 架构设计

```go
// 统一接口:数据后端
type DataSink interface {
    // 从 modem 收到的 raw IP 包 → 送入后端
    InjectPacket(pkt []byte) error
    // 从后端取 raw IP 包 → 发往 modem
    ReadPacket() ([]byte, error)
    Close() error
}

// 实现 1:TUN(已有)
type TUNSink struct { dev tun.Device }
func (s *TUNSink) InjectPacket(pkt []byte) error { _, err := s.dev.Write(pkt, 0); return err }
func (s *TUNSink) ReadPacket() ([]byte, error) { ... }

// 实现 2:gVisor netstack channel(新增)
type NetstackSink struct { ch chan tcpip.PacketBuffer }
func (s *NetstackSink) InjectPacket(pkt []byte) error { s.ch <- toBuffer(pkt); return nil }
func (s *NetstackSink) ReadPacket() ([]byte, error) { ... }
```

`modemToTun` / `tunToModem` relay 代码完全不变,只是 `DataSink` 换一个实现。

### 实施路线

1. **Phase 1**: 验证 QDC507 是否支持 AT+QIOPEN(socket 模式)
   - 如果支持:可以作为最快的"无 TUN 上网"验证(~1 小时)
   - 如果不支持:跳过,直接走 netstack

2. **Phase 2**: 实现 netstack + SOCKS5 后端
   - 导入 gVisor netstack(`gvisor.dev/gvisor/pkg/tcpip`)
   - 创建 channel link endpoint
   - SOCKS5 server(`armon/go-socks5` 或手写)
   - 接入现有 DataSink 接口
   - 测试:浏览器配置 SOCKS5 → curl --socks5 → 访问 baidu.com

3. **Phase 3**: 打磨
   - DNS 解析(netstack 内置 DNS resolver 或用 114.114.114.114)
   - IPv6 双栈(netstack 支持 IPv6)
   - 连接统计(netstack metrics)

## 多设备场景:多 4G 链路(进一步强化 netstack 方案)

如果同时插入多个 DJI 4G 模块(各自独立 SIM/运营商),TUN 方案和 netstack 方案的差距会进一步拉大。

### TUN 方案:多设备 = 路由地狱

```
qmi0: 10.147.0.1/27  (Modem #1)
qmi1: 10.147.0.2/27  (Modem #2)     ← 3 个 TUN 适配器
qmi2: 10.147.0.3/27  (Modem #3)     ← route table 冲突 + source binding
```
- 多条默认路由 metric 谁先?
- 源地址绑定每个包都要查
- DNS 用谁的?
- failover 检测 + 切换逻辑复杂
- macOS utun 序号乱跳

### netstack + SOCKS5 方案:多设备 = 天然多端口/负载均衡

```
# 独立端口模式:每个模块一个 SOCKS5
Modem #1 (<运营商>) → netstack → SOCKS5 :1081
Modem #2 (移动) → netstack → SOCKS5 :1082
Modem #3 (电信) → netstack → SOCKS5 :1083

# 负载均衡模式:所有模块共用一个 SOCKS5
┌─ netstack #1 ─┐
├─ netstack #2 ─┤→ SOCKS5 :1080 (round-robin / least-latency / failover)
└─ netstack #3 ─┘
```

### 架构改动(极小)

每个组件已是实例化设计,只需多实例化:

```go
devices, _ := ctx.ListDevicesBySerial()       // 按 serial 枚举全部模块
for i, dev := range devices {
    transport := qmitransport.Open(dev)         // 独立 QMI(每个模块各 claim MI_04)
    mgr := manager.New(transport)               // 独立 manager
    mgr.Dial(apn)                               // 独立拨号,各拿各的 IP/PDH
    sink := netstack.New(mgr.IP(), mgr.DNS())   // 独立 netstack 实例
    socks5.Listen(fmt.Sprintf(":%d", 1081+i), sink)
}
```

### 多设备能力矩阵

| 场景 | 说明 | netstack 实现方式 |
|---|---|---|
| 负载均衡 | 3× 4G = 3× 带宽 | SOCKS5 round-robin 分发到 N 个 netstack |
| Failover | 主模块断线自动切备用 | health check + 自动摘除故障后端 |
| 多运营商 | 运营商 A+B+C,选最优 | 每模块独立 SOCKS5 端口,App 手选 |
| 多 APN | 专网+公网同时在线 | 不同 APN 拨不同模块,各走各的 |
| SIM 池 | N 张卡轮换规避限额 | 动态启停不同模块的 QMI session |

### 对比结论

| 维度 | TUN 多设备 | netstack 多设备 |
|---|---|---|
| 路由冲突 | ❌ 严重 | ✅ 无(SOCKS5 不碰路由表) |
| 管理员权限 | ✅ 每个都要 | ❌ 零 |
| 负载均衡 | ❌ 需 ECMP/policy routing | ✅ SOCKS5 层 round-robin |
| Failover | ❌ 复杂(route convergence) | ✅ SOCKS5 层摘除 |
| 跨平台 | ❌ 每平台 TUN 行为不同 | ✅ 完全统一 |
| App 选择性 | ❌ 全局路由不可选 | ✅ 连接级别选 SOCKS5 端口 |

**多设备场景是 netstack+SOCKS5 方案的杀手级用例。** TUN 方案在单设备时勉强可用,多设备时复杂度爆炸。
