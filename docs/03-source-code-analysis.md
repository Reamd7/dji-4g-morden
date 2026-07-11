# 源码深度分析报告

> 对 VoHive 生态相关源码仓库的深度分析，包括协议栈复用性评估、跨平台可行性、开源替代品调研。

---

## 一、VoHive（source-available）

### 1.1 基本信息

仓库：`github.com/iniwex5/vohive`（私有仓库的源码快照）
许可证：PolyForm Noncommercial 1.0.0（禁止商用）
技术栈：Go 1.26 + Gin + GORM + Viper + zap + SQLite
前端：Vue 3 + Vite + TailwindCSS + Element Plus

### 1.2 核心能力

多模组并发管理（USB 热插拔自动发现，udev/netlink 监听）、SOCKS5/HTTP 代理引擎（SO_BINDTODEVICE 绑网卡出站）、短信收发（QMI WMS 或 AT）、USSD、eSIM 全生命周期管理（AT APDU 或 QMI UIM）、多渠道推送（Telegram/Bark/飞书/QQ/PushPlus/Email）、VoWiFi/IMS 通话。

### 1.3 私有依赖

4 个 `iniwex5/*` 私有仓库闭源，无法独立编译：

**iniwex5/vowifi-go**：VoWiFi/IMS 协议栈。通过解包二进制（UPX 解压后 strings 搜索）反推出约 373 个子包。三层架构：engine/（数据面协议引擎：ikev2/ipsec/swu/crypto/eap/driver）、internal/vowifi/（业务运行时：imscore/voice/sipkit/netstack/policy/profile）、runtimehost/（对外桥接层）。完整的用户态 VoWiFi/IMS 手机基带实现。

**iniwex5/quectel-qmi-go**：QMI 通信层。两个包：pkg/qmi/（协议层：ctl/dms/nas/wds/wda/wms/uim/voice/ims/imsa）和 pkg/manager/（高层管理器：device_query/service_recovery/uim_provisioning）。纯 Go 实现，不依赖 libqmi/qmicli。

**iniwex5/qqbot**：QQ 机器人 SDK。4 个子包：auth/dispatch/rest/stream。精简，就是个 QQ 推送 SDK。

**iniwex5/netlink**：vishvananda/netlink 的 fork。多了 nl 子目录，可能加了 VoHive 需要的 SO_BINDTODEVICE 等接口。

### 1.4 编译验证

CI 配置（Dockerfile.github）有 `GOPRIVATE=github.com/iniwex5/*` 和 `GH_PAT`（GitHub Personal Access Token），证实需要私有仓库访问权限才能编译。

v1.5.5 二进制的 Go buildinfo 证实：主模块 `github.com/iniwex5/vohive (devel)`，4 个私有依赖分别有版本号（vowifi-go v1.1.2 / quectel-qmi-go v0.6.0 / qqbot v1.0.1 / netlink v1.3.3）。

build tags：`with_utls`（v1.5.5 去掉了 nomsgpack，启用 msgpack 提升性能）。

---

## 二、quectel-qmi-go（开源复刻）

### 2.1 基本信息

仓库：`source/vohive-collection/quectel-qmi-go/`（vohive-collection 收录的源码快照）
语言：纯 Go（不依赖 libqmi/qmicli/quectel-CM 运行时）
Go 版本：1.24+

### 2.2 协议栈覆盖

10 个 QMI service 全部实现：CTL（客户端 ID 分配 + proxy 模式）、DMS（设备信息 + IMEI + PIN）、NAS（驻网状态 + 信号 RSRP/RSRQ + 搜网）、WDS（拨号/断开 + APN profile + 流量统计 + IP 分配）、WDA（数据格式配置）、UIM（卡状态 + PIN + APDU + 逻辑通道）、WMS（短信发送/读取/删除 + 路由 + ACK）、IMS（IMS 开关）、IMSA（IMS 注册状态）、VOICE（通话 + DTMF + USSD）。

manager 层额外提供：自动拨号 + 重连、IPv4/IPv6 双栈、QMAP Mux 多 PDN、设备自动发现、短信事件、IMS 状态事件、通话事件。

### 2.3 跨平台设计

transport 层定义了 `qmiTransport` 接口（Read/Write/Close/SetReadDeadline 四个方法）。上层协议代码全部依赖接口，不直接打开设备。

当前实现：`openRawTransport` 用 `os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)` 打开 `/dev/cdc-wdm0`（Linux 专属）。

替换成 USB endpoint 实现（gousb）只需实现 4 个接口方法，上层代码零改动。

netcfg 层已实现三平台 Configurator：
- `linux.go`（`//go:build linux`）— 用 iniwex5/netlink 库操作网卡
- `darwin.go`（`//go:build darwin`）— 用 ifconfig/route 命令
- `windows.go`（`//go:build windows`）— 用 netsh/ipconfig 命令

proxy_transport 有 `//go:build linux` 限制（用 Unix domain socket 连 qmi-proxy），非 Linux 走 `proxy_transport_unsupported.go`。

### 2.4 复用评估

QMI 协议栈（pkg/qmi/）和连接管理器（pkg/manager/）是平台无关的纯 Go 代码，可直接复用。

需要替换的只有 transport 层（~100 行 USB 实现）和 proxy transport（用户态方案不需要 proxy，直连即可）。

---

## 三、uicc-go（SIM/USIM/ISIM 协议库）

### 3.1 基本信息

仓库：`source/vohive-collection/uicc-go/`
语言：纯 Go，不依赖 cgo，不链接 libqmi/libmbim/libqrtr-glib
Go 版本：1.26+

### 3.2 架构

分两层：

协议包（底层 transport）：
- `apdu/` — APDU 请求/响应编解码
- `at/` — 通过 AT+CSIM 发 APDU（串口 transport）
- `ccid/` — 通过 PC/SC CCID 读卡器发 APDU
- `mbim/` — 通过 MBIM UICC 底层访问发 APDU
- `cdcwdm/` — 通过 Linux cdc-wdm 设备发 APDU（QMI UIM）
- `qcom/` — 高通 QMI UIM 和 QRTR 传输

业务包：
- `usim/` — 高层 USIM/ISIM 操作（ICCID/IMSI 读取、AKA 认证、EAP-AKA、SMS-PP 下载）
- `usim/card/` — 卡片文件系统操作
- `usim/command/` — USIM 命令封装
- `usim/simfile/` — SIM 文件路径定义
- `usim/tlv/` — SIM 文件 TLV 解析

### 3.3 AT transport 的跨平台实现

`at/serial_linux.go`（`//go:build linux`）— 用 golang.org/x/sys/unix 的 termios API 配置串口（raw 模式 + 波特率 + 超时）。

`at/serial_windows.go`（`//go:build windows`）— 用 golang.org/x/sys/windows 的 CreateFile + DCB + Overlapped I/O 配置串口。完整的 Windows 异步串口实现。

`at/serial_other.go`（`//go:build !linux && !windows`）— 返回 unsupported。

at.go 的 Reader 结构体只依赖 `io.ReadWriteCloser` 接口。替换成 USB endpoint 实现就能跨平台。

### 3.4 复用评估

AT + APDU 编解码完全平台无关。USIM/ISIM 高层操作完全平台无关。

需要替换的只有 serial transport（当前依赖 COM 端口存在，需要装 Quectel 驱动）。换成 USB endpoint 实现后，不需要 COM 端口。

---

## 四、euicc-go（eSIM LPA 协议）

### 4.1 基本信息

仓库：`source/vohive-collection/euicc-go/`
上游：`github.com/damonto/euicc-go`
语言：纯 Go

### 4.2 能力

完整的 eSIM LPA（Local Profile Assistant）协议实现：
- Profile 列表/启用/停用/删除/重命名
- SM-DP+ 服务器通信（Profile 下载）
- eUICC 元数据读取（EID、默认 SM-DP+、eUICC 版本）
- BER-TLV 编解码
- Root CI 证书验证

### 4.3 driver 层

`driver/` 支持 5 种 transport：AT（通过 uicc-go 的 at.Reader）、CCID（PC/SC）、MBIM、QMI、HTTP。

driver 层是接口化的——替换 transport 只需要实现接口，LPA 协议代码零改动。

### 4.4 复用评估

完全平台无关。可直接复用。唯一需要的 transport 适配（AT over USB）和 uicc-go 共用。

---

## 五、vowifi-go（开源复刻）

### 5.1 基本信息

仓库：`source/vowifi-go/`（boa-z 第三方复刻，非 iniwex5 原版）
规模：105 个 .go 文件、32280 行、46 个测试
git remote：`https://github.com/boa-z/vowifi-go`
时间：2026-07-04 一天内 73 个 commit 完成

### 5.2 覆盖度

覆盖原版 vowifi-go 的核心子集：

engine/（数据面协议引擎）：ikev2（IKE_SA_INIT/CREATE_CHILD_SA/INFORMATIONAL/MOBIKE）、ipsec（ESP seal/open/nat_t）、swu（SWu 隧道：session/state_init/auth/rekey/resume）、crypto（DH/AES-CBC/HMAC/PRF）、eap（EAP-AKA/AKA'）、driver（xfrm/tun/nettools）。

runtimehost/（对外桥接层）：carrier（运营商预设）、e911（TS.43 entitlement）、identity（ISIM 身份）、messaging（SMS/USSD）、voiceclient/voicehost（语音）。

缺失：原版的 `internal/vowifi/` 整个目录——imscore（IMS 注册状态机）、voice（SIP 通话引擎 + RTP/RTCP/SRTP）、netstack（gVisor 用户态网络栈）、policy、entitlement 等约 200 个包。

### 5.3 能否替代原版

不能。缺 `internal/vowifi/` 巨型业务层和各种 bridge 接口，直接 `replace` 进 VoHive 编译不过。

### 5.4 价值

学习 VoWiFi 协议栈内部机制的最佳开源参考（IKEv2/SWu/ESP/EAP-AKA/IMS REGISTER/SIP 通话全链路的 Go 实现）。

---

## 六、swu-go（SWu/IKEv2 引擎）

### 6.1 基本信息

仓库：`source/vohive-collection/swu-go/`
module：`github.com/iniwex5/swu-go`
依赖：iniwex5/netlink + vishvananda/netns

### 6.2 能力

纯 Go 实现的 SWu 客户端库，用于 VoWiFi 建立到 ePDG 的 IPSec 隧道。

完整实现：IKEv2（IKE_SA_INIT/IKE_AUTH/CREATE_CHILD_SA/INFORMATIONAL）、EAP-AKA 认证（含 AUTS 同步失败处理）、COOKIE 防 DoS、IKE Fragmentation（RFC 7383）、Child SA Rekey（主动+被动+碰撞检测）、IKE SA Rekey、IKE Reauthentication、EAP-AKA Fast Re-auth、Session Resumption（RFC 5723）、Soft/Hard Expire（XFRM 内核事件驱动）。

依赖 XFRM（Linux 内核的 IPSec 框架），不能跨平台。

---

## 七、SimAdmin（Rust 模组管理平台）

### 7.1 基本信息

仓库：`source/SimAdmin/`
上游：`github.com/3899/SimAdmin`
许可证：GPL-3.0
语言：Rust（backend）+ Vue（frontend）
规模：31K 行 Rust（backend/src/）

### 7.2 架构分析

**SimAdmin 不直接和 USB/QMI/AT 通信。** 一切模组操作通过 ModemManager 的 D-Bus 接口。

backend/src/modem_manager.rs（6697 行）是核心——全是 zbus（Rust D-Bus 库）调用 ModemManager 的 API：`org.freedesktop.ModemManager1.Modem`、`.Modem3gpp`、`.Simple`、`.Messaging`、`.Voice`、`.Sim`、`.Sms`。

backend/src/serial.rs（29 行）不是串口通信——是一个全局 Mutex 锁，确保 D-Bus 操作串行化。

backend/src/esim.rs（1537 行）不自己实现 APDU——调用外部 `lpac` 命令行工具（C 写的 eSIM 管理工具）。

backend/src/sms_listener.rs（575 行）不直接读 AT——通过 D-Bus 监听 ModemManager 的短信接收信号。

### 7.3 跨平台可行性

**SimAdmin 的 Rust 代码在 Windows/macOS 上完全无法使用。** ModemManager 是 Linux 专属服务。zbus D-Bus 在 Windows 上不标准。

claude_plan.md 里说"28K lines covering IKEv2, EAP-AKA, IMS SIP"——实际审查后发现 SimAdmin **不包含**这些协议的实现。它有的是"如何用 ModemManager D-Bus API 管理模组"的业务逻辑代码。claude_plan 的说法可能是计划从 SimAdmin 提取架构设计思路，而不是直接复用代码。

### 7.4 和本项目的关系

SimAdmin 的价值在于它是 Rust 生态里**唯一完整的多模组管理 Web 平台**。业务逻辑（设备管理、短信列表、eSIM 操作、通知推送、网络配置、DDNS、WLAN 管理）设计完善，代码质量高。

如果未来要做 Rust 版本的跨平台方案，SimAdmin 的业务逻辑层（modem_manager.rs、esim.rs、sms_listener.rs、notification.rs）可以作为架构参考——只需要把 D-Bus 调用替换成直接 USB/QMI/AT 调用。

---

## 八、sms-gateway（真开源短信网关）

### 8.1 基本信息

仓库：`source/sms_gateway/`
上游：`github.com/Pug265Backtrack/sms-gateway`
许可证：AGPL-3.0（可商用，需传染）
语言：Go（backend + agent）+ Vue 3（frontend）

### 8.2 架构

面板 + 探针分体分布式设计。面板（Docker：backend + web/nginx）负责 UI/认证/推送/持久化。探针（sms-agent 主机端或 ESP32-C3 固件）通过 Agent Runtime 协议和面板通信。

### 8.3 AT 命令兼容链

`agent/internal/modem/sms.go` 的设计值得学习——厂商无关的回退链：

ICCID 读取依次尝试：`AT+CCID`（Quectel/SIMCom/通用）、`AT+QCCID`（老 Quectel）、`AT+ICCID`（ASR/合舟/合宙）、`AT^ICCID`（华为/海思）。

IMEI 读取依次尝试：`AT+CGSN=1`、`AT+GSN`、`AT+QGSN`（Quectel）、`AT+CGSN`。

信号查询优先 `AT+CESQ`（LTE 专用，返回 RSRP/RSRQ），回退 `AT+CSQ`（通用，只返回 RSSI）。

### 8.4 PDU 编解码

`agent/internal/modem/pdu.go` 是完整的 SMS PDU 实现：

- `EncodeSubmit` — 构造 SMS-SUBMIT PDU（发短信）
- `DecodeDeliver` — 解析 SMS-DELIVER PDU（收短信）
- GSM7 packing/unpacking（7-bit 压缩编解码）
- UCS2 编解码（中文/Unicode）
- UDH（User Data Header）长短信拼接
- semi-octet 号码编码/解码

我们的 Windows 发短信工具 `send_sms.js` 就是参照这个 Go 实现移植的（Node.js 版本）。

### 8.5 复用评估

sms-gateway 的 PDU 编解码和 AT 兼容链可以直接用于用户态 USB 方案。agent 的架构（注册/握手/能力上报/Intent 执行/Event 上报）也值得参考。

---

## 九、dji-cellular-as-modem（RNDIS 免驱方案）

### 9.1 基本信息

仓库：`source/dji-cellular-as-modem/`
许可证：MIT
语言：Python（工具）+ Shell（脚本）

### 9.2 核心贡献

一行 AT 命令 `AT+QCFG="usbnet",1` 把模块切到 RNDIS 模式，变成免驱 USB 网卡。

提供了跨平台 AT 工具 `tools/at_send.py`：Linux 用 ttyACM 发 AT，Mac 用 pyusb fallback（直接接管 USB endpoint）。

### 9.3 硬件研究价值

RESEARCH_NOTES.md 的 USB 描述符分析是本研究的重要基础：

- DJI 私有模式的 5 接口拓扑（Iface 0 QMI data / Iface 1-3 ADB-like / Iface 4 QMI control）
- CDC Union 描述符损坏导致 qmi_wwan 错位绑定
- AT 命令在 Interface 3（不是标准的 Interface 2）
- pyusb 接管 Interface 0/4 发标准 QMI/MBIM 超时 → DJI 用私有协议
- ECM/NCM/MBIM 在 macOS 上不工作 → DJI 抹掉了 IAD 描述符

### 9.4 Android 调研

结论：消费级 Android 没有非 root 方案。framework 的 EthernetManager 默认禁用 USB 以太网接收方向。只有 DJI RC Pro 遥控器能用（有 system signature + 私有驱动）。

---

## 十、开源世界 VoWiFi 现状

完全一体化、用户态、可移植的 VoWiFi 协议栈库，开源世界几乎没有。

最接近的实现：

| 项目 | 语言 | 覆盖层 | 缺失层 |
|---|---|---|---|
| iniwex5/vowifi-go（私有） | Go | 全部（IKEv2+SWu+ESP+EAP-AKA+IMS+SIP+Voice+RTP） | 闭源 |
| boa-z/vowifi-go（复刻） | Go | 核心（IKEv2+SWu+ESP+EAP-AKA） | 缺 IMS/Voice/RTP 业务层 |
| fasferraz/SWu-IKEv2 | Python | SWu+IKEv2+ESP+EAP-AKA | 缺 IMS/SIP/Voice |
| strongSwan | C | IKEv2+ESP | 缺 SWu(VoWiFi 特有)+IMS/SIP |
| phhusson/ims | Kotlin | IMS+VoWiFi（依赖 Android modem） | 不是独立协议栈 |

Go 生态的 SIP 库（emiago/sipgo）只做 SIP，不做 IKEv2/ESP/EAP-AKA。

iniwex5/vowifi-go 是真正的技术壁垒——开源世界没有同级替代品。
