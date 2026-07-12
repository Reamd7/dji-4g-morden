# AGENTS.md — 项目长期记忆

本文件记录关于本项目的关键信息,供后续会话快速恢复上下文。

## 项目概览

`dji-modem-research` —— DJI 调制解调器研究 + 用户态驱动实现项目。

### 核心目标

写一个**纯用户态、跨平台的 DJI 4G 模组驱动**(Go),不依赖 Quectel 厂商驱动,通过 USB 直连 DJI "百望" 4G 模块(Quectel QDC507 / EG25-G),实现 **SIM 卡上网 + 短信收发**。

**为什么**:装厂商驱动 → 平台依赖(macOS 完全不支持、飞牛 NAS 需自编内核模块、闭源黑盒);切 RNDIS → 免驱但丢 AT 口、双重 NAT;虚拟机直通 → 复杂且有开销。用户态方案是唯一能让三平台(尤其 macOS)都获得完整 4G 模组能力的路径。

**技术路线**(`docs/01` 已论证):
1. 前置:Windows 用 Zadig 装 WinUSB(非厂商驱动);macOS/Linux 无需操作
2. USB transport:gousb/libusb 直连 USB bulk endpoint,替代内核 cdc-wdm + 串口驱动(~200 行)
3. 协议层:复用 `vohive-collection` 现成 Go 协议栈
   - `quectel-qmi-go`(~5000 行,QMI 全栈 + 管理器 + 三平台网络配置 netcfg)
   - `uicc-go`(~2000 行,AT + APDU,接口是 `io.ReadWriteCloser`)
   - `euicc-go`(~3000 行,eSIM LPA)
4. 虚拟网卡:wireguard/tun + QMI WDS 拨号 → 注入 TUN(~250 行)
5. 总计新增 ~600 行,80% 协议逻辑可直接复用

**关键约束**:
- 模块必须刷成**标准 EC25 PID**(2C7C:0125)。DJI 私有 PID(2CA3:4006)走私有协议,标准 QMI/AT 无效(CDC Union 描述符损坏、AT 口错位在 Iface 3)
- 选 **Go 而非 Rust**:Go 有现成协议栈;Rust 要从零写 7-9K 行(SimAdmin 绑死 ModemManager D-Bus,不可跨平台)。Rust 唯一优势 nusb(纯 Rust USB)不足以弥补

**三阶段路线图**(`docs/01` 第六节):
- 阶段 1:AT 通道 + 短信(最快验证,~200 行新代码)
- 阶段 2:QMI 通道 + 拨号(中等,~150 行)
- 阶段 3:TUN 虚拟网卡 + 上网(最难,~250 行)

### 实测验证结果(2026-07-11)

**USB endpoint 地址实测确认**(解除 `docs/01` §8.1 的未知风险)。DJI 百望 EC25 模式(PID 0125)布局:

| 接口 | 用途 | Endpoints |
|---|---|---|
| MI_00 | DM 诊断口 | EP 0x01 OUT bulk / EP 0x81 IN bulk |
| MI_01 | NMEA GPS | EP 0x02 OUT / EP 0x82 IN bulk / EP 0x83 IN intr(10B) |
| **MI_02** | **AT 命令口** | **EP 0x03 OUT bulk / EP 0x84 IN bulk / EP 0x85 IN intr(10B)** |
| MI_03 | Modem 控制 | EP 0x04 OUT / EP 0x86 IN bulk / EP 0x87 IN intr(10B) |
| **MI_04** | **QMI 数据通道** | **EP 0x05 OUT bulk / EP 0x88 IN bulk / EP 0x89 IN intr(8B)** |

规律:OUT 端点递增 0x01~0x05,IN 端点递增 0x81~0x89。MI_04 的 interrupt 端点 maxPacket=8(QMI URC 通知),其他接口的 interrupt maxPacket=10。

**AT 通路已打通**(`cmd/attest/`):通过 gousb claim MI_02 → EP 0x03 OUT 发 `AT\r\n` → EP 0x84 IN 收到 `AT\r` 回显 + `\r\nOK\r\n`。证明整条链路 gousb → libusb → WinUSB → USB bulk → 模块 AT 口工作正常。阶段 1 物理基础 OK。

**端到端里程碑达成(2026-07-12)**:`internal/usbtransport/` + `third_party/sms-gateway/modem/` 接入完成,hardware 测试 `TestHardwareModemInitializeAndCSQ` 通过。完整 AT 会话验证:
- `Initialize` 成功:AT → ATE0 → AT+CMEE=1 → AT+CPIN?(`+CPIN: READY`)→ AT+CMGF=0 → AT+CNMI=2,1 → AT+CPMS="SM"(`+CPMS: 3,50`)
- `SendAndWait("AT+CSQ")` 返回 `+CSQ: 26,99`(RSSI=26)
- 证明 USB transport 能驱动完整的 sms_gateway/modem AT 协议层(初始化 + URC 订阅 + 短信存储就绪)
- **阶段 1 核心目标达成**:纯用户态 USB → AT 协议层链路打通,无需任何厂商驱动

**短信收发验证完成(2026-07-12)**:`sms_hardware_test.go` 全套通过(hardware tag):
- 设备信息:ICCID `89000000000000000000` / IMEI `860000000000000` / 运营商 `Carrier` / 本机号 `+8613800000000` / 信号 `-61 dBm`
- 短信接收:`ListStored` + `DecodeDeliver` 读出 SIM 已存的 3 条中文短信(UCS-2 自动解码),发件人/正文/时间戳完整
- **短信发送**:`Send` 完整 CMGS 两步握手(`AT+CMGS=N` → 等 `>` → PDU+Ctrl-Z → OK)成功发出短信到 `+8613800000001`
- **readLine 修正**(上游 sms_gateway 在 USB 场景的 bug):`>` 提示符以空格结尾非 `\n`,传统串口靠短读部分返回能工作,USB CDC-AT bulk endpoint 上 `ReadString('\n')` 会把 `>` 永久卡在 bufio 缓冲区。修正:`readLine` 在部分读(无 `\n`)时,若 TrimSpace 后内容是 `>`,当一行返回。这是 USB transport 方案相对串口的必要适配。

**smscodec 升级完成(路线 B,2026-07-12,commit e73e4d9)**:PDU 编解码层从 SG 手写 `pdu.go` 升级到 vohive `smscodec`(委托 warthog618/sms,MIT)。三缺陷全部消除:GSM-7 扩展表完整(TS 23.038 §6.2.1)、长短信自动分段(`BuildSubmitTPDUs`)+ 自动重组(`Reassembler`)、16-bit ref UDH。`pdu.go` 改为 ~100 行 facade(保留 `DecodedSMS`/`ConcatInfo`/`DecodeDeliver` 对外形状,内部委托 smscodec);`Send` 改多段循环 CMGS(段间 500ms)。详见 `plans/archive/upgrade-smscodec.md`、`third_party/smscodec/AGENTS.md`。

**USB transport 方向 F 修复(2026-07-12)**:Read 从 200ms 短轮询改为**长阻塞读**(`readCtx = context.WithCancel` 无超时,运行期 0 次 `libusb_cancel_transfer`,只有 Close 时 cancel 一次)。原因:200ms 轮询在 WinUSB 上 send 时 read-cancel 撞 write transfer 偶发 segfault;判别实验证实 10× 降频(2000ms)消除崩溃,方向 F 把 cancel 降到 0 根治。配套:`readLine` 改逐字节读(不再依赖超时检测 `>`),同时对串口和 USB 工作。详见 `issue/001-gousb-close-transfer-cancel-crash.md`、`internal/usbtransport/AGENTS.md`。

**AT 命令全集对齐(2026-07-12)**:按 `plans/archive/at-commands-roadmap.md` Phase A-E 补 11 条 vohive 源码发过但 SG 没有的命令(全部带离线测试 `roadmap_test.go` + 硬件验证):Phase A 设备信息(IMSI/SoftwareVersion)、Phase B 网络注册(CSRegistration/PSAttached/DefinePDP/ListPDPs/QueryNetworkInfo)、Phase C 配置(SetFunctionLevel/SetQCFG)、Phase D SIM APDU(CSIM/ReadSIMFile/WriteSIMFile)、Phase E USSD(SendUSSD)。状态对照见 `docs/10-at-commands-alignment.md`。

**+CMTI 实时收信管道接入(2026-07-12)**:`SetSMSCallback(cb)` 启用 +CMTI 自动收信(+CMTI URC → CMGR 读 → DecodeDeliver → smscodec.Reassembler 重组 → CMGD 删除 → cb 回调)。`handleIncomingSMS` 在独立 goroutine 避免 readerLoop 死锁。Reassembler 真正接入(原计划留阶段 2,已提前完成)。

**QMI Phase 0 探针完成(2026-07-12)**:`cmd/qmiprobe/` 实测 QMI 传输模型。**结论:模型 B(EP0 控制封装),需先设 DTR。**
- 模组实为 **QDC507**(DJI 定制版,固件 QDC507GLEFM21),非标准 EC25。`AT+QCFG="usbnet",0`(QMI 模式)、`AT+CGATT: 1`(PS 已附着)
- **关键发现:必须先发 DTR**(CDC `SetControlLineState`,bRequest=0x22,wValue=0x0001,wIndex=4),否则模组不响应任何 QMI 请求。源自 Linux `qmi_wwan.c`:`QMI_MATCH_FF_FF_FF(0x2c7c,0x0125)` → `qmi_wwan_info_quirk_dtr` → `qmi_wwan_change_dtr(dev,true)`。注释原文:"The device will not respond to QMI requests until we set DTR"
- **MI_04(FF/FF/FF,3 EP)模型 B:WORKS**。设 DTR 后 `SEND_ENCAPSULATED_COMMAND`→`GET_ENCAPSULATED_RESPONSE` 返回 19 字节 `SYNC_RESP`(TLV result=SUCCESS)。Interrupt EP 0x89 发出 `RESPONSE_AVAILABLE`(`a1 01 00 00 04 00 00 00`)
- MI_04 模型 A(bulk EP 0x05→0x88):不通,即使设了 DTR。Bulk 写入 USB 层成功但无响应
- MI_00(FF/FF/FF,2 EP)模型 A(bulk):不通(DM/DIAG 口,非 QMI)
- SYNC 帧已验证:按 quectel-qmi-go `Packet.Marshal()` 构造,12 字节 `01 0B 00 00 00 00 00 01 27 00 00 00`(计划原文 13 字节帧有误)
- **阶段 2 解除阻塞**:子计划 02 按 **模型 B + DTR** 实现 `QMITransport`。TX=`Control(0x21,0x00,...)`,RX=`interrupt 0x89`→`Control(0xA1,0x01,...)`
- 详见 `plans/stage2/00-phase0-transport-probe.md` 实测结果章节

**QMI 拨号成功(2026-07-12)**:纯用户态 USB → QMI → WDS 拨号 → 拿到运营商 IP。零内核驱动,Windows 上跑通。
- `cmd/qmidial/` 工具:`QMITransport` → `qmi.NewClientFromTransport`(SYNC)→ `manager.NewWithClient`(hook 注入,绕过 /dev/cdc-wdm0)→ `StartCore`(NAS/DMS/UIM/WDA/WDS 全 service)→ `Connect`(WDS StartNetwork)
- 拨号结果:IPv4 `10.147.0.1/27`、Gateway `10.147.0.2`、DNS `114.114.114.114, 223.5.5.5`、MTU `1500`
- **修复**:control GET buffer 从 readLoop 的 16KB 改为独立 2048B — WinUSB 拒绝大 buffer(`libusb_error_invalid_param`)。QMUX 帧 < 1500B(IP MTU),2048 足够
- manager 全包复制(子计划 07):`NewWithClient` 利用 `openClientAndAllocateServicesHook` 注入预构造 client,绕过 Linux 设备发现。新增依赖 logrus/zap
- **阶段 2 核心目标达成**

**TUN 虚拟网卡 + 实际上网完成(2026-07-12)**:纯用户态 USB → QMI → TUN relay → 真实上网。零内核驱动,Windows 上全程跑通。ICMP/TCP/UDP 三协议双向通过 4G。
- **bulk EP 数据探针**(`cmd/bulkprobe/`):确认 WDA SetDataFormat(raw-IP)后 bulk IN EP 0x88 承载裸 IP 包(IPv4 `0x45` + IPv6 `0x60`)。ZLP 确认:28B 包通,512B 包不通 → `zlp=true`
- **relay 实现**(`internal/qmidatapath/`):Bridge 双向 goroutine(TUN.Read→bulkOut.Write / bulkIn.ReadContext→TUN.Write),raw-IP 直传无头处理,ZLP 参数化(探针驱动),offset=4(macOS headroom)。88.7% 覆盖率,13 mock 测试 + 2 硬件测试
- **OpenBulkEndpoints**(`internal/qmitransport/bulkendpoints.go`):在同一 MI_04 claim 上打开 bulk IN 0x88 / bulk OUT 0x05,与 QMI 控制面(EP0+intr)不同 endpoint 无竞争
- **端到端集成**(`cmd/qmidial -dial -tun`):TUN 创建 → manager(NetInterface 触发 WDA)→ Connect(配 IP/路由)→ relay 启动 → DNS 自建(netcfg.UpdateDNS Win/macOS 不可用 → netsh/networksetup/resolvectl)
- **实测结果**:curl baidu.com HTTP 200(107ms)、nslookup 解析成功、ping baidu.com 4/4(68ms)。两种模式:源地址绑定(metric=100,主网络共存)+ 全局 TUN(metric=1,全走 4G)
- **wintun.dll 修复**:gousb cgo 环境下 wintun-go 的 `LOAD_LIBRARY_SEARCH_APPLICATION_DIR` 解析失败 → `wintun_preload_windows.go` 全路径预加载。admin UAC 无 mise PATH → libusb-1.0.dll 也放 exe 同目录
- **Linux 驱动参考**:`references/linux-driver/q_drivers/qmi_wwan/qmi_wwan_q.c` 确认 EC25 默认 `qmap_mode=0`(raw-IP)、`FLAG_SEND_ZLP`、DTR 在 bind() 设置
- **三阶段全部完成**:纯用户态 USB → AT+短信 → QMI 拨号 → TUN 上网,零内核驱动

**macOS 平台验证启动(2026-07-12)**:Windows 全程跑通后转向 macOS(darwin/arm64)。零配置——brew libusb + go 默认 clang 即可,无需 Zadig/无需卸内核驱动。
- AT 通路:`attest` + `TestHardwareModemInitializeAndCSQ` PASS(完整 Initialize 序列 + `+CSQ: 23,99`,0.04s)
- 短信收发:`sms_hardware_test.go` 全套 PASS——`ListStored`+`DecodeDeliver` 读出 SIM 3 条中文短信(UCS-2)、`Send` 单段(CMGS 两步握手)、`SendMultiPart` 长短信分段。设备:ICCID `<ICCID>`/IMEI `<IMEI>`/<CARRIER>/本机 `<本机号>`/-59 dBm
- **接口类**:usbprobe 实测 5 个接口均 `class=0xFF` vendor-specific,AppleUSBACM 只匹配 CDC-ACM(02/02)故无内核驱动抢占,gousb 直接 claim
- **单例 context 修复**:macOS 上 gousb `NewContext()` 反复 init/exit 不可靠,第二次 `libusb_init_context` 返回 -99 且 gousb 直接 panic(连续多 hardware 测试崩)。改 `usbtransport` 为 package 级单例 context(`sharedContext` + `sync.Once`),Open 复用、Close 不关 context。修复后连续 5 个 hardware 测试全 PASS,Windows 同样受益
- QMI SYNC 握手:`qmiprobe` 验证模型 B + DTR——设 DTR(CDC SetControlLineState,wIndex=4)后 EP0 控制封装(SEND_ENCAPSULATED_COMMAND `0x21,0x00` → GET_ENCAPSULATED_RESPONSE `0xA1,0x01`)返回 19 字节 SYNC_RESP(`01 12 00 80 00 00 01 01 27 00 07 00 02 04 00 00 00 00 00`,MsgID `0x0027`,TLV result SUCCESS),与 Windows 逐字节一致。模型 A(bulk)无响应。结论:QMITransport 用 EP0 封装,阶段 2 物理基础 OK
- QMI transport + 拨号:`qmitransport` hardware 测试全套 PASS(9 个 hardware + 12 个 mock,28s)——`TestHardwareSyncExchange`(SYNC_RESP 19B)、`TestHardwareManagerStartCore`(全 service 分配)、**`TestHardwareManagerDialup`(IPv4 `<IPv4>`/MTU 1500/PDH `<PDH>`)**、**`TestHardwareManagerDialupIPv6`(IPv6 `<IPv6-addr>/64` 双栈)**、并发 Close 5+10 轮稳定。**阶段 2 在 macOS 闭环**(零内核驱动,纯用户态 USB→QMI→WDS 拨号)。注:`qmitransport.openInternal` 也每次 `NewContext()`,但连续 hardware 测试未 panic(与 usbtransport 不同,疑 Close 实现差异,暂不改动)
- TUN 上网(阶段 3):`qmidial -dial -tun` 端到端跑通——curl http://www.baidu.com HTTP 200(0.287s)、curl --interface <IPv4> HTTP 200(0.082s)、ping baidu.com 61-78ms、nslookup 经 4G DNS `<DNS1>` 解析成功(4 IP)。relay TX 1384 pkts/230KB + RX 1079 pkts/372KB(真实双向数据)。**三阶段在 macOS 全部闭环,零内核驱动**
- **DarwinConfigurator 修复(macOS netcfg)**:`third_party/quectel-qmi-go/netcfg/darwin.go` 三处适配——(1) `SetIPAddress` IPv4 延迟到 `AddDefaultRoute`:utun 是 point-to-point 且无主地址,`inet alias` 与纯 `inet IP/PREFIX` 均失败(utun 只配得上 inet6),改为记 v4addr,在 AddDefaultRoute 用 `ifconfig utun inet LOCAL DEST`(point-to-point,dest=gateway);(2) `AddDefaultRoute` 用 `0/1`+`128/1` split 覆盖 default:macOS 已有 en0 的 default 路由,`route add default` 报 "File exists" 静默失败,split 比 default 更具体且不动现有路由,FlushRoutes 删除即可恢复;(3) 路由用 `-iface`(link-direct,point-to-point 无需 ARP gateway)

### 下一步:macOS 平台验证

三阶段路线图在 **Windows + macOS 上全程跑通**(AT+短信+QMI拨号+TUN 上网,零内核驱动)。代码已设计为跨平台。**macOS(2026-07-12,darwin/arm64)三阶段全部验证通过**:AT 通路 + 短信收发 + QMI(SYNC + 拨号 IPv4+IPv6 双栈)+ TUN 上网(curl HTTP 200/ping 61-78ms/DNS 解析)。brew libusb + 默认 clang,无需 Zadig/无需卸内核驱动。

- **USB transport**:macOS 无需 Zadig(libusb 原生支持),gousb 直接 claim 成功(5 接口均 `class=0xFF` vendor-specific,AppleUSBACM 不匹配,无内核驱动抢占)。✅ AT transport(MI_02);✅ QMI transport(MI_04 模型 B + DTR);✅ TUN 上网
- **TUN**:wireguard/tun macOS utun,relay `offset=4` ✅ 已验证(utun 4 字节 AF-family 前缀,wireguard/tun Read/Write `offset-4:` 语义匹配)
- **DNS**:`configureDNSDarwin`(`networksetup`)把运营商 DNS 配到主网络服务(Wi-Fi/Ethernet)——macOS 限制:utun 无 networksetup 管理的 Network Service,不能直接给 utun 配 DNS;DNS 解析又是全局的,所以临时借主网络服务设 4G DNS。经 split 路由走 utun9 → 4G 解析成功(`<DNS1>` nslookup 验证)。**退出自动恢复**:`configureDNSDarwin` 先 `networksetup -getdnsservers` 快照原值并注册 `dnsRestore` 闭包,cleanup 调 `restoreDNS()` 还原(原手动值或 `empty`=DHCP),不再污染主网络
- **网络配置**:`DarwinConfigurator` 三处适配已验证——IPv4 point-to-point 延迟配置(`ifconfig utun inet LOCAL DEST`)、`0/1`+`128/1` split 覆盖 default、`-iface` link-direct 路由、FlushRoutes 删 split 恢复主网络
- **已知差异**:macOS utun 不支持 `Close()` 后重建同名 adapter(每次创建新 utunN),relay lifecycle 可能需调整

### macOS 适配改动(2026-07-12,待 Windows 回归验证)

为在 macOS 跑通三阶段,本次改了 6 个文件。对 Windows 的影响分三类,**Windows 新会话须据此回归验证**:

**① 对 Windows 无运行时影响(平台隔离)**:
- `third_party/quectel-qmi-go/netcfg/darwin.go`:DarwinConfigurator 的 SetIPAddress/AddDefaultRoute/FlushRoutes 改动。darwin.go 无 build tag(全平台编译),但 Windows 走 `factory_windows.go` 的 WindowsConfigurator,**不调用** DarwinConfigurator,改了等于没改
- `cmd/qmidial/dns.go`:`dnsRestore`/`restoreDNS` 只在 `configureDNSDarwin` 设置;Windows 走 `configureDNSWindows`(netsh,**未改**)。cleanup 调 `restoreDNS()` 时 Windows 上 `dnsRestore=nil` → no-op
- `cmd/qmidial/main.go`:抽出的 `allowICMPFirewall()`/`setInterfaceMetric()`/`nullDevice()` 在 Windows 上跑**原封不动的 netsh 命令**(`if runtime.GOOS != "windows" { return }` 守卫,Windows 分支逻辑与原代码逐行一致)

**② Windows 等价,但依赖 Tera 渲染**:
- `.mise.toml`:`os()=="windows"` 时渲染成**原来的精确值**(`C:\msys64\mingw64\bin` / CC / PKG_CONFIG_PATH)。逻辑等价,但改用 Tera `{% if %}` 块——需 Windows mise 正确渲染(mise `os()` 在 Windows 返回 `"windows"`,标准行为,低风险)。**Windows 验证点**:`mise exec -- go env CC` 应为 `C:\msys64\mingw64\bin\gcc.exe`,且 `go build ./...` 通过

**③ ⚠️ 唯一行为改变:`internal/usbtransport/usbtransport.go` 单例 context**:
- 原来:每次 `Open` 新建 `gousb.NewContext()`,`Close` 关闭 context
- 现在:进程级共享 context(`sharedContext` + `sync.Once`),`Close` 不关 context(只关 device/config/iface)
- 原因:macOS 上 gousb 反复 `libusb_init`/exit 第二次 panic(LIBUSB_ERROR_OTHER -99),单例避免反复 init/exit
- **对 Windows**:改变了 context 生命周期。共享 context 是 gousb 推荐用法,理论兼容,但 **Windows 的 WinUSB 反复 open/close 同一设备接口未实测**。"Windows 同样受益"是推断,未验证
- **Windows 回归验证清单**:
  1. `mise exec -- go run ./cmd/attest/`(单次 Open——验证单例 context 基本工作)
  2. `mise exec -- go test -tags=hardware -run 'TestHardwareDeviceInfo|TestHardwareSMS' ./internal/usbtransport/`(连续多次 Open/Close——验证共享 context 下反复 claim MI_02 不出问题)
  3. 若失败,回退方案:`sharedContext` 内加 `if runtime.GOOS != "darwin"` 分支保留 Windows 原行为(每次新 context + Close 关 context),仅 darwin 用单例

### 目录结构

- `docs/` —— 调研报告(中文 markdown)
  - `00-reference-index.md` —— 参考索引,指向上级 `vohive-release/` 的教程/资产/源码
  - `01-userland-usb-modem-feasibility.md` —— 用户态 USB modem 可行性研究(核心方案文档)
  - `02-dji-modem-hardware-and-flashing.md` —— DJI 模块硬件分析 + 刷写研究
  - `03-source-code-analysis.md` —— VoHive 生态源码深度分析(协议栈复用性 + 跨平台评估)
- Go 项目(2026-07-11 创建)—— 用户态 USB modem 程序的实现代码,当前为 hello world 骨架。
- 上级 `vohive-release/` 含教程、二进制资产、`source/` 下 5 个源码仓库(协议栈复用来源)

## 工具链管理(mise)

本项目用 **mise** 管理 Go 与工具链环境,配置在 `.mise.toml`。

### `.mise.toml` 当前内容

```toml
[tools]
go = "latest"

[env]
GOPATH = "{{ config_root }}/.gopath"
# Force `go install` into GOPATH/bin (overriding mise's GOBIN default that
# points into the Go install dir, which gets wiped on version bumps).
GOBIN = ""
# Windows-only CGO toolchain for gousb (libusb cgo binding):
#   - mingw64 gcc on PATH (also ships libusb-1.0.dll at runtime)
#   - CC pinned to mingw64 gcc (git bash otherwise injects MSYS2's /usr/bin/gcc)
#   - PKG_CONFIG_PATH so cgo finds mingw64 libusb-1.0.pc
# macOS uses homebrew clang (go's default CC) + brew pkg-config defaults,
# so these are left empty on non-Windows. Verified: `go build ./...` passes
# on darwin/arm64 with brew libusb 1.0.29 + pkgconf 2.4.3, no overrides needed.
_.path = [
    "{{ config_root }}/.gopath/bin",
    '{% if os() == "windows" %}C:\msys64\mingw64\bin{% else %}{% endif %}',
]
CC = '{% if os() == "windows" %}C:\msys64\mingw64\bin\gcc.exe{% else %}{% endif %}'
PKG_CONFIG_PATH = '{% if os() == "windows" %}C:\msys64\mingw64\lib\pkgconfig{% else %}{% endif %}'
```

### 关键约定 / 踩坑记录

- **Go 版本**:`go = "latest"`,解析为 go1.26.1（windows/amd64 + darwin/arm64 均已验证）。
- **mise 信任**:新建/修改 `.mise.toml` 后需先 `mise trust` 才能 `mise install` / `mise exec`。
- **运行 Go 命令**:统一用 `mise exec -- go <cmd>`(或在已激活 mise 的 shell 中直接 `go <cmd>`)。
- **macOS 工具链**(darwin,2026-07-12 验证):CGO 工具链零配置——`go build ./...`、`go vet ./...`、`go test ./...` 全通过。
  - 依赖:`brew install libusb pkg-config`(实测 libusb 1.0.29 + pkgconf 2.4.3)。
  - go 默认 `CC=clang`、`PKG_CONFIG_PATH` 留空(brew pkg-config 默认搜索路径已含 libusb-1.0.pc)。
  - `.mise.toml` 用 Tera `os()` 条件:Windows 设 mingw64 CC/PKG_CONFIG_PATH,macOS/其他留空。
  - **Tera 语法坑**:`{{ ... if ... else ... }}` 在 `{{}}` 表达式内非法(报 "expected or/and/..."),必须用 `{% if %}...{% else %}{% endif %}` 块语句;Windows 路径反斜杠用 TOML 单引号 raw 串(`'...'`)包裹以避免转义地狱。
- **mingw64 gcc 接入**:为支持 CGO(如 SQLite 驱动),接入 `C:\msys64\mingw64\bin\gcc.exe`(16.1.0)。
  - `_.path` 把 mingw64/bin 前置到 PATH。
  - **`CC` 必须显式锁定为绝对路径**:git bash 会把 MSYS2 的 `/c/msys64/usr/bin/gcc`(15.2.0)注入 PATH 且排在 `_.path` 前面,导致 `which gcc` 命中的是错的那个。设置 `CC` 后 CGO 编译器确定,不受 PATH 顺序干扰。
  - `CGO_ENABLED=1` 默认开启。
  - 已用最小 CGO 程序验证通过。
- **libusb-1.0 + pkg-config**(gousb USB 通信必需):
  - 通过 `pacman -S mingw-w64-x86_64-libusb mingw-w64-x86_64-pkg-config` 安装(MSYS2)。
  - gousb 是 cgo 绑定 libusb,编译时 `pkg-config --cflags --libs libusb-1.0` 必须能找到。
  - **`PKG_CONFIG_PATH` 必须显式设为 `C:\msys64\mingw64\lib\pkgconfig`**,否则 cgo 报 `pkg-config: executable file not found` 或找不到 libusb。
  - libusb-1.0.dll 运行时需要在 PATH 里(mingw64/bin 已通过 `_.path` 加入)。

### Go LSP 工具链

通过 `go install` 装入项目内 `GOPATH/bin`(即 `.gopath/bin/`),不入库(`.gitignore` 已忽略 `/.gopath/`)。

| 工具 | 版本(2026-07-11) | 用途 |
|---|---|---|
| **gopls** | v0.23.0 | Go 官方 LSP server(代码补全/跳转/诊断) |
| **dlv** (Delve) | v1.27.0 | 调试器(DAP 协议) |
| **staticcheck** | 2026.1 (v0.7.0) | 静态分析 |

安装命令(均需 `mise exec` 环境下执行):
```bash
mise exec -- go install golang.org/x/tools/gopls@latest
mise exec -- go install github.com/go-delve/delve/cmd/dlv@latest
mise exec -- go install honnef.co/go/tools/cmd/staticcheck@latest
```

**踩坑 — `GOBIN` 必须显式清空**:
mise 的 Go 包默认把 `GOBIN` 指向 mise 的 Go 安装目录(`…/mise/installs/go/<ver>/bin`)。`go install` 时 `GOBIN` 优先级高于 `GOPATH/bin`,导致工具被装进 Go 安装目录——升级 Go 版本时会被清空。`.mise.toml` 中 `GOBIN = ""` 强制落回 `GOPATH/bin`(即项目内 `.gopath/bin/`)。

**踩坑 — GOPATH 隔离**:
`GOPATH` 设为 `{{ config_root }}/.gopath`(项目内),工具不污染全局 `~/go`。`.gopath/bin` 已通过 `_.path` 加入 PATH,shell 和编辑器都能直接调用 `gopls.exe` / `dlv.exe` / `staticcheck.exe`。

### 编辑器 LSP 接入

编辑器(VSCode/ZCode 等)的 Go 插件需要知道 Go/gopls 的位置。GOROOT/GOPATH 由 mise 动态给出:
```bash
mise exec -- go env GOROOT GOPATH
```

**方式 A(推荐)— 让编辑器继承 mise 环境**:从已激活 mise 的终端启动编辑器(如 `mise exec -- code .`),或用 mise 的 shell hook。编辑器的 Go 插件会自动用 PATH 里的 `gopls`。

**方式 B(手动)— 在 settings.json 指定**:
```jsonc
{
  // VSCode / ZCode settings.json
  "go.goroot": "<mise exec -- go env GOROOT 的输出>",
  "go.gopath": "C:\\Users\\<Win用户名>\\Documents\\experiment_area\\vohive-release\\dji-modem-research\\.gopath",
  "go.useLanguageServer": true,
  "go.toolsManagement.autoUpdate": false  // 工具由 mise 管,别让插件自己装
}
```
> 注意设 `go.toolsManagement.autoUpdate: false`,否则 Go 插件会把工具又装回全局 `~/go`,与 mise 管理的版本冲突。

## 测试方案

### 测试约定(与上游 vohive-collection 对齐)

本项目继承上游 `quectel-qmi-go` / `uicc-go` 的测试风格,保证代码风格统一:

- **只用标准库 `testing`** —— 不引入 testify(上游 39 个测试文件 0 个用 testify)。断言用手写 `if got != want { t.Errorf(...) }`。
- **手写 mock,不用 mock 框架** —— 参考 `uicc-go/at/at_test.go` 的 `scriptPort` 模式(实现 `io.ReadWriteCloser`,内置预设响应脚本)。
- **table-driven 风格** —— 多用例用 `[]struct{ name string; ... }` 切片 + `t.Run(tc.name, ...)`。
- **测试文件与被测代码同包同目录** —— `foo.go` ↔ `foo_test.go`,包名相同(非 `foo_test`),便于访问未导出符号。

### 测试分层(按可测性 / 是否依赖硬件)

本项目代码必须按"是否需要真实 USB 硬件"严格分层,这是可测试性的核心:

| 层 | 依赖硬件? | 测试方式 | 示例 |
|---|---|---|---|
| **协议层**(QMI/AT/SMS PDU 编解码) | ❌ 纯计算 | 内存单测,输入 bytes → 断言解析结果 | QMUX 帧编解码、AT+CMGS PDU 编码、GSM7/UCS2 转换 |
| **Transport 接口适配层** | ❌ 用 mock | mock `io.ReadWriteCloser` / `qmiTransport` | USB endpoint 包装器、帧分片重组、超时处理 |
| **USB 物理层**(gousb/libusb 调用) | ✅ 需硬件 | 集成测试,**用 build tag 隔离** | `attest`、`usbprobe`、真实收发 |
| **上层业务**(拨号/短信收发编排) | ❌ 用 mock | 注入 mock transport,验证状态机/重连/事件分发 | 自动拨号重连、短信事件回调 |

**关键设计原则:USB 操作必须被接口包裹,绝不散落到业务代码里。** 这样 80% 的逻辑(协议栈、业务编排)可以完全离线、CI 里跑,只有最薄一层 USB 物理调用需要硬件验证。

### Build tag 隔离硬件测试

需要真实设备的测试/命令用 build tag `//go:build hardware` 标记,**不参与默认 `go test ./...`**,避免无设备环境跑红:

```go
//go:build hardware

package usbtransport

// 这里的测试需要插着 EC25 模块 + WinUSB 驱动,否则跳过或失败。
```

- 默认(CI、无硬件):`go test ./...` —— 只跑纯逻辑 + mock 测试
- 有硬件时:`go test -tags=hardware ./...` —— 额外跑设备集成测试
- `cmd/` 下的 `usbprobe` / `attest` 本身就是硬件验证工具,不在 `go test` 范围(是 `go run`)
- **`.env` 自动加载**:hardware 测试包均有 `hwenv_test.go`,其 `init()` 调 `testutil.LoadDotEnv(".env")`——读取项目根 `.env` 并 `os.Setenv`(**向上查找父目录**,shell 已设变量优先不覆盖)。故 `go test -tags=hardware` 无需手动 export 即可拿到 `DJI_TEST_SMS_RECIPIENT`/`DJI_TEST_APN` 等;`Send` 测试因 `.env` 有 recipient 会**真实发送短信**(花钱),只读测试不受影响

### Transport 可测性的接口设计

`docs/01` 已确认两个核心上游接口(本项目要实现 USB 版本):

```go
// quectel-qmi-go 的 transport 接口(pkg/qmi/transport.go)
type qmiTransport interface {
    Read([]byte) (int, error)
    Write([]byte) (int, error)
    Close() error
    SetReadDeadline(time.Time) error
}

// uicc-go 的 AT 接口(at/at.go)
// Reader.port 字段类型是 io.ReadWriteCloser
```

本项目 USB transport 实现这两个接口后,**测试时注入内存版 mock 即可**,无需真实 USB。mock 直接复用上游 `scriptPort` 思路(预设响应队列 + 记录写入)。

### 跑测试的命令(均在 mise exec 下)

```bash
# 默认:纯逻辑 + mock 测试(CI 友好,无需硬件)
mise exec -- go test ./...

# 带硬件:额外跑设备集成测试(需 EC25 + WinUSB)
mise exec -- go test -tags=hardware ./...

# 详细输出 + 覆盖率
mise exec -- go test -v -cover ./...

# 单个包
mise exec -- go test -v ./internal/usbtransport/

# 竞态检测(transport/并发代码必跑)
mise exec -- go test -race ./...
```

> **`-race` 是硬性要求**:transport 层有并发读写(Read goroutine + Write 主循环),所有 transport/manager 相关测试必须通过 `-race`。

### 覆盖率目标

| 层 | 目标 | 说明 |
|---|---|---|
| 协议编解码 | ≥ 90% | 纯函数,边界用例必须覆盖 |
| Transport 适配层 | ≥ 80% | 用 mock,需覆盖分片/超时/错误路径 |
| 业务编排 | ≥ 70% | 用 mock,重点测重连/事件分发/错误恢复 |
| USB 物理层 | 不计 | 靠硬件集成测试,不追求覆盖率 |

### Pre-commit Hook(强制 go test)

`.githooks/pre-commit` 在每次 `git commit` 前自动跑 `go test -race ./internal/...`,失败则中止提交。

- **激活方式**:`git config core.hooksPath .githooks`(已对本仓库执行;新克隆后需手动执行一次)
- **绕过**:`git commit --no-verify`(不推荐)
- Hook 内部镜像 Makefile 的环境设置:`mise where go` 解析 GOROOT,从 `USERPROFILE` 推导 Windows env(go.exe 需要)
- race 检测对并发代码硬性要求,所以 hook 总是用 `-race`

## Go 项目结构(当前)

```
dji-modem-research/
├── AGENTS.md        # 本文件
├── .mise.toml       # mise 工具链配置
├── .githooks/       # pre-commit hook(强制 go test -race 通过)
├── Makefile         # 标准化 test/cover/lint 等命令
├── go.mod           # module dji-modem-research
├── main.go          # hello world
├── internal/
│   ├── usbdesc/     # USB 描述符格式化(纯逻辑,100% 覆盖)
│   ├── testutil/    # ScriptPort mock io.ReadWriteCloser
│   ├── usbtransport/# ATTransport:USB bulk → io.ReadWriteCloser
│   │   ├── usbtransport.go             # Open/Read/Write/Close(方向F)
│   │   ├── usbtransport_test.go        # mock 单测
│   │   ├── usbtransport_hardware_test.go # AT 通路硬件测试
│   │   └── sms_hardware_test.go        # 短信收发硬件测试
│   ├── qmitransport/ # QMITransport:USB model B EP0 → qmi.Transport
│   │   ├── qmitransport.go             # Open/DTR/interrupt/Read(GET)/Write(SEND)/Close(ioMu)
│   │   ├── bulkendpoints.go            # OpenBulkEndpoints(EP 0x88/0x05,阶段 3 数据面)
│   │   ├── qmitransport_test.go        # 11 mock + bulkendpoints 测试
│   │   ├── qmitransport_hardware_test.go # transport 硬件测试
│   │   ├── manager_hardware_test.go    # manager 硬件测试
│   │   └── AGENTS.md                   # 记忆点:模型 B + DTR + ioMu + bulkendpoints
│   └── qmidatapath/ # 阶段 3:TUN ↔ bulk EP 双向 raw IP relay
│       ├── relay.go                    # Bridge + tunToModem + modemToTun + ZLP + Stats
│       ├── relay_test.go              # 13 mock 测试(-race,88.7% 覆盖率)
│       ├── relay_hardware_test.go     # 硬件测试(build tag: hardware)
│       └── AGENTS.md                   # 记忆点:relay 设计 + Close 时序 + ZLP
├── third_party/
│   ├── sms-gateway/ # AT 协议层"壳"(AGPL-3.0)
│   ├── smscodec/    # PDU 编解码(warthog618/sms MIT + PolyForm NC)
│   └── quectel-qmi-go/  # QMI 协议栈 + manager
│       ├── qmi/                 # 协议栈核心:QMUX + WDS/WDA/DMS/NAS/UIM/WMS
│       ├── manager/             # 全功能连接管理器(~13K 行)
│       ├── device/              # Linux sysfs 设备发现(USB 路径不用)
│       └── netcfg/              # 三平台网络配置(UpdateDNS Win/macOS 不可用)
├── cmd/
│   ├── usbprobe/    # USB endpoint 枚举探针
│   ├── attest/      # MI_02 AT 通路验证
│   ├── qmiprobe/    # QMI 传输模型探针(Phase 0)
│   ├── qmidial/     # QMI 拨号 + TUN relay(-dial -tun 端到端上网)
│   │   ├── main.go                  # -tun 标志:TUN + relay + DNS + ping/curl 测试
│   │   ├── dns.go                    # 三平台 DNS 自建(netsh/networksetup/resolvectl)
│   │   ├── wintun_preload_windows.go # wintun.dll 全路径预加载
│   │   └── run_tun.bat               # admin UAC 提升运行
│   └── bulkprobe/   # 阶段 3 门控探针(WDA + raw-IP + ZLP 确认)
├── issue/
│   └── 001-gousb-close-transfer-cancel-crash.md
├── plans/
│   ├── stage2-qmi-dialup.md   # 阶段 2 总览
│   ├── stage2/                # 阶段 2 子计划 00-08
│   ├── stage3-tun-internet.md # 阶段 3 总览
│   ├── stage3/                # 阶段 3 子计划 00-04
│   └── archive/
├── references/     # osmocom/sixfab/linux-driver/wintun-0.14.1.zip
└── docs/           # 研究文档(01-03 方案/硬件/源码,04-10 AT 命令)
```

- `go.mod` 模块名:`dji-modem-research`
- 依赖:`github.com/google/gousb`(USB)、`github.com/rs/zerolog` + `go.bug.st/serial`(modem)、`github.com/warthog618/sms`(smscodec)、`github.com/sirupsen/logrus` + `go.uber.org/zap`(manager)、`golang.zx2c4.com/wireguard/tun`(TUN 虚拟网卡,阶段 3)
- 运行 AT 测试:`mise exec -- go run ./cmd/attest/`
- 运行 QMI 拨号(只读):`mise exec -- go run ./cmd/qmidial`
- 运行 QMI 拨号(激活 PDP):`mise exec -- go run ./cmd/qmidial -dial`
- 运行 TUN 上网(需 admin + wintun.dll):`mise exec -- go build -o qmidial.exe ./cmd/qmidial && qmidial.exe -dial -tun`
- 跑 mock 单测:`make test-race`
- 跑硬件集成测试(需设备 + WinUSB):`make test-hardware`

### third_party 复制方案(非 replace,SG 壳 + smscodec 芯)

两个 third_party 包都从上级 `source/` **复制**(非 go.mod replace),理由:可移植(无绝对路径依赖)、可改(改副本不影响上游)、依赖最小。选型见 `docs/07`。

- **`sms-gateway/modem/`**(AGPL-3.0):AT 协议"壳"。核心改动:`port serial.Port` → `io.ReadWriteCloser` + `NewFromIO`(transport 注入);`readLine` USB 适配(`>` 提示符 + 逐字节读,方向F);`pdu.go` facade 化(委托 smscodec);补 11 条 AT 命令(network/sim/config 新文件);`SetSMSCallback` +CMTI 实时收信管道。保留 ICMP ping / zerolog / Open 串口路径。
- **`quectel-qmi-go/`**(license 待确认):QMI 协议栈 + manager。复制 `qmi/`(协议栈)+ `manager/`(全功能连接管理器,~13K 行)+ `device/`(Linux 设备发现)+ `netcfg/`(三平台网络配置)。`transport_export.go` 导出 `NewClientFromTransport` 注入 USB transport;`usb_entry.go` 导出 `NewWithClient` 注入预构造 client(hook 绕过 /dev/cdc-wdm0 + Linux sysfs)。阶段 2 拨号验证通过(IPv4+IPv6 双栈),阶段 3 TUN relay 端到端上网验证通过(curl+DNS+ping)。
