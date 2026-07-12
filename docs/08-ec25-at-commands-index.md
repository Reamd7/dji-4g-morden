# Quectel EC25&EC21 AT Commands Manual V1.2 — 全册索引

> 本文档对官方手册 `Quectel_EC25EC21_AT_Commands_Manual_V1.2.pdf`(2017-11-14,231 页)做逐命令索引,
> 并标注每条命令对 `dji-modem-research` 三阶段路线图的相关性、标准来源,以及 `third_party/sms-gateway/modem/`
> 当前的实现状态。
>
> 创建于 2026-07-12。手册原件见同目录 PDF。本索引不替代手册原文,仅作导航与选型速查。

---

## 一、图例与约定

### 项目三阶段相关性标记

| 标记 | 含义 |
|---|---|
| ✅SMS | 阶段 1(AT+短信)相关,且 `modem` 包**已实现** |
| 🔵拨号 | 阶段 2(QMI/AT 拨号)相关,待实现 |
| 🟢上网 | 阶段 3(TUN 虚拟网卡 + 上网)相关,待实现 |
| 🔧参考 | 排障 / 错误码 / URC / 配置类,常备参考 |
| ⬜N/A | 本项目不需要(语音通话、电话本、音频、补充业务等) |

### 标准来源缩写

- **V.25ter** = ITU-T V.25ter(串口 AT 命令基础)
- **TS 27.007** = 3GPP TS 27.007(UE AT 命令集)
- **TS 27.005** = 3GPP TS 27.005(SMS/CBS 的 DTE-DCE 接口)
- **Quectel** = Quectel 私有扩展(非 3GPP 标准)

### 手册统计

全册 **14 章**,约 **140 条 AT 命令**(含 `AT+QCFG` 的 19 个子配置)。逐章索引见下文。

---

## 二、命令总览(按章节)

| 章 | 主题 | 命令数 | 项目主要用途 |
|---|---|---|---|
| §1 | Introduction | — | 语法/字符集/URC 基础 |
| §2 | General Commands | 25 | 设备识别 / 回显 / 功能级 / 错误格式 |
| §3 | Serial Interface Control | 6 | ⬜(USB 场景基本不适用) |
| §4 | Status Control(QCFG) | 4(+19 子项) | 🔧网络/频段/URC 缓存配置 |
| §5 | (U)SIM Related | 11 | ✅PIN/ICCID + eSIM 复用 |
| §6 | Network Service | 10 | ✅CSQ + 🔵注册/运营商查询 |
| §7 | Call Related | 20 | ⬜(语音通话) |
| §8 | Phonebook | 5 | ⬜ |
| §9 | **SMS** | **18** | **✅阶段 1 核心** |
| §10 | **Packet Domain** | **16** | **🔵阶段 2 拨号核心** |
| §11 | Supplementary Service | 8 | ⬜ |
| §12 | Audio | 15 | ⬜ |
| §13 | Hardware Related | 5 | 🔧电源/时钟/低功耗 |
| §14 | Appendix | — | 🔧错误码 / URC / 字符集速查(高价值) |

---

## 三、逐章命令索引

### §1 Introduction(无独立命令)

要点(手册 p9-11):

- **命令语法三类**:basic(`AT<x><n>`)、S-parameter(`ATS<n>=<m>`)、extended(`AT+<x>=...`)
- **支持的字符集**:GSM(默认)、IRA、UCS2 —— 由 `AT+CSCS` 切换,影响 SMS 与电话本
- **AT 命令接口**:两个 USB 口(USB MODEM port / USB AT port)+ 一个主 UART。USB 口也支持 AT 通信
- **URC**:非命令响应的主动上报(来电 RING、收到短信、电压/温度告警等)
- **关机流程**:`AT+QPOWD` 最安全;等 STATUS 拉低 / `POWERED DOWN` 后再断电;65s 未收到强制断 VBAT

> **本项目对照**:USB 场景下我们 claim MI_02(AT 口)走 bulk endpoint,等价于"USB AT port"。字符集默认 GSM,收发中文短信需 UCS2(项目 PDU 模式下由 DCS 字段决定,见 §14.8)。

---

### §2 General Commands(手册 p12-30)

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 | modem 现状 |
|---|---|---|---|---|---|---|
| 2.1 | `ATI` | 显示产品标识 | `ATI` | V.25ter | 🔧 | ✅`DeviceInfo` 用 |
| 2.2 | `AT+GMI` | 厂商标识 | `AT+GMI` | V.25ter | 🔧 | — |
| 2.3 | `AT+GMM` | 型号标识 | `AT+GMM` | V.25ter | 🔧 | — |
| 2.4 | `AT+GMR` | 软件版本 | `AT+GMR` | V.25ter | 🔧 | — |
| 2.5 | `AT+CGMI` | 厂商标识 | `AT+CGMI` | TS 27.007 | 🔧 | ✅`DeviceInfo` 用 |
| 2.6 | `AT+CGMM` | 型号标识 | `AT+CGMM` | TS 27.007 | 🔧 | ✅`DeviceInfo` 用 |
| 2.7 | `AT+CGMR` | 软件版本 | `AT+CGMR` | TS 27.007 | 🔧 | — |
| 2.8 | `AT+GSN` | IMEI | `AT+GSN` | V.25ter | ✅SMS | (IMEI 经此或 +CGSN) |
| 2.9 | `AT+CGSN` | 产品序列号(IMEI) | `AT+CGSN` | TS 27.007 | ✅SMS | ✅`IMEI()` |
| 2.10 | `AT&F` | 恢复出厂默认 | `AT&F[<value>]` | V.25ter | 🔧 | — |
| 2.11 | `AT&V` | 显示当前配置 | `AT&V` | V.25ter | 🔧 | — |
| 2.12 | `AT&W` | 存当前参数到用户 profile | `AT&W[<n>]` | V.25ter | 🔧 | — |
| 2.13 | `ATZ` | 恢复用户 profile | `ATZ[<value>]` | V.25ter | 🔧 | — |
| 2.14 | `ATQ` | 结果码呈现模式 | `ATQ<n>` | V.25ter | 🔧 | — |
| 2.15 | `ATV` | 响应格式 | `ATV<value>` | V.25ter | 🔧 | — |
| 2.16 | `ATE` | 回显开关 | `ATE<value>` | V.25ter | ✅SMS | ✅`Initialize` 发 `ATE0` |
| 2.17 | `A/` | 重复上一命令 | `A/` | V.25ter | ⬜ | — |
| 2.18 | `ATS3` | 命令行终止符 | `ATS3=<n>`(默认 13=CR) | V.25ter | 🔧 | — |
| 2.19 | `ATS4` | 响应格式符 | `ATS4=<n>`(默认 10=LF) | V.25ter | 🔧 | — |
| 2.20 | `ATS5` | 命令行编辑符 | `ATS5=<n>`(默认 8=BS) | V.25ter | 🔧 | — |
| 2.21 | `ATX` | CONNECT 结果码/呼叫监测 | `ATX<value>` | V.25ter | ⬜ | — |
| 2.22 | `AT+CFUN` | 功能级 | `AT+CFUN=<fun>[,<rst>]` | TS 27.007 | 🔧 | — |
| 2.23 | `AT+CMEE` | 错误消息格式 | `AT+CMEE=<n>`(0/1/2) | TS 27.007 | ✅SMS | ✅`Initialize` 设 `=1`(数字错误码) |
| 2.24 | `AT+CSCS` | TE 字符集 | `AT+CSCS=<chset>` | TS 27.007 | ✅SMS | (PDU 模式下影响有限,见 §14.8) |
| 2.25 | `AT+QURCCFG` | URC 输出口配置 | `AT+QURCCFG="urcport"[,<v>]` | Quectel | 🔧 | (USB 场景可指定 usbat/usbmodem) |

---

### §3 Serial Interface Control Commands(手册 p31-36)

> **整章 ⬜ 不适用于本项目**:这些是 UART 物理流控/波特率/DCD/DTR/RI 信号控制。USB bulk endpoint 传输无 DCD/DTR/RTS/CTS 概念,波特率固定。仅 `AT+QRIR`(RI 行为复位)在 USB 虚拟 RI 场景偶有参考价值。

| § | 命令 | 描述 | 标准 |
|---|---|---|---|
| 3.1 | `AT&C` | DCD 功能模式 | V.25ter |
| 3.2 | `AT&D` | DTR 功能模式 | V.25ter |
| 3.3 | `AT+IFC` | TE-TA 本地流控(RTS/CTS) | V.25ter |
| 3.4 | `AT+ICF` | TE-TA 字符帧格式/校验 | V.25ter |
| 3.5 | `AT+IPR` | UART 固定波特率(默认 115200) | V.25ter |
| 3.6 | `AT+QRIR` | RI 行为复位为 inactive | Quectel |

---

### §4 Status Control Commands(手册 p37-59)

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 |
|---|---|---|---|---|---|
| 4.1 | `AT+CPAS` | ME 活动状态 | `AT+CPAS` | TS 27.007 | 🔧 |
| 4.2 | `AT+CEER` | 扩展错误报告(上次失败原因) | `AT+CEER` | TS 27.007 | 🔧排障(释放原因见 §14.9) |
| 4.3 | `AT+QCFG` | 扩展配置(见下方 19 子项) | `AT+QCFG="<type>"[,...]` | Quectel | 🔵🔧 |
| 4.4 | `AT+QINDCFG` | URC 指示配置 | `AT+QINDCFG=<urctype>[,<enable>]` | Quectel | 🔧 |

#### §4.3 `AT+QCFG` 子配置(手册 p40-56)

| 子配置 | 作用 | 相关性 |
|---|---|---|
| `"gprsattach"` | GPRS 附着模式(0 手动/1 自动) | 🔵拨号 |
| `"nwscanmode"` | 网络搜索模式(AUTO/GSM/WCDMA/LTE only) | 🔵 |
| `"nwscanseq"` | 网络搜索顺序 | 🔵 |
| `"roamservice"` | 漫游服务开关 | 🔵 |
| `"servicedomain"` | 注册服务域(CS/PS/CS&PS) | 🔵拨号前需 PS |
| `"band"` | 频段配置(GSM/WCDMA/LTE/TDS hex 位掩码) | 🔵 |
| `"hsdpacat"` | HSDPA 类别 | ⬜ |
| `"hsupacat"` | HSUPA 类别 | ⬜ |
| `"rrc"` | RRC 发布版本 | ⬜ |
| `"sgsn"` | SGSN 发布版本 | ⬜ |
| `"msc"` | MSC 发布版本 | ⬜ |
| `"pdp/duplicatechk"` | 允许同 APN 多 PDN | 🔵 |
| `"urc/ri/ring"` | RING URC 时 RI 行为 | ⬜(USB 无物理 RI) |
| `"urc/ri/smsincoming"` | 收短信 URC 时 RI 行为 | ⬜ |
| `"urc/ri/other"` | 其他 URC 时 RI 行为 | ⬜ |
| `"risignaltype"` | RI 信号输出载体(respective/physical) | ⬜ |
| `"urc/delay"` | 延迟 URC 输出 | ⬜ |
| `"urc/cache"` | URC 缓存功能 | 🔧(USB 断连重连时可缓存) |
| `"tone/incoming"` | 来电铃声功能 | ⬜ |

#### §4.4 `AT+QINDCFG` URC 类型

- `"all"`(主开关,默认 ON)、`"csq"`(信号变化,默认 OFF)、`"smsfull"`(短信满,默认 OFF)
- `"ring"`(默认 ON)、`"smsincoming"`(默认 ON)、`"act"`(接入技术变化,默认 OFF)

---

### §5 (U)SIM Related Commands(手册 p60-73)

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 | modem 现状 |
|---|---|---|---|---|---|---|
| 5.1 | `AT+CIMI` | IMSI | `AT+CIMI` | TS 27.007 | ✅SMS | — |
| 5.2 | `AT+CLCK` | 设施锁(PIN 锁等) | `AT+CLCK=<fac>,<mode>[,<passwd>]` | TS 27.007 | 🔧 | — |
| 5.3 | `AT+CPIN` | 输入/查询 PIN | `AT+CPIN?` / `AT+CPIN=<pin>` | TS 27.007 | ✅SMS | ✅`Initialize`+`ensureSIMReady` |
| 5.4 | `AT+CPWD` | 改密码 | `AT+CPWD=<fac>,<oldpwd>,<newpwd>` | TS 27.007 | 🔧 | — |
| 5.5 | `AT+CSIM` | 通用 (U)SIM 透传(APDU) | `AT+CSIM=<length>,<command>` | TS 27.007 | 🟢eSIM | (uicc-go 复用基础) |
| 5.6 | `AT+CRSM` | 受限 (U)SIM 访问(READ/UPDATE BINARY 等) | `AT+CRSM=<cmd>[,<fileid>,...]` | TS 27.007 | 🟢eSIM | — |
| 5.7 | `AT+QCCID` | 显示 ICCID | `AT+QCCID` | Quectel | ✅SMS | ✅`ICCID()`(见下方说明) |
| 5.8 | `AT+QPINC` | PIN/PUK 剩余次数 | `AT+QPINC=<facility>` | Quectel | 🔧 | — |
| 5.9 | `AT+QINISTAT` | (U)SIM 初始化状态查询 | `AT+QINISTAT` | Quectel | 🔧 | (1=CPIN READY,2=SMS DONE,4=PB DONE) |
| 5.10 | `AT+QSIMDET` | (U)SIM 卡热插拔检测 | `AT+QSIMDET=<enable>,<insertlevel>` | Quectel | ⬜ | (USB 模组无热插拔 GPIO) |
| 5.11 | `AT+QSIMSTAT` | (U)SIM 卡插入状态上报 | `AT+QSIMSTAT=<enable>` | Quectel | ⬜ | — |

> **ICCID 命令名差异**:手册 V1.2 用 `AT+QCCID`(Quectel 私有)。`modem` 包 `ICCID()` 用 `AT+CCID` → `AT+CICCID` → `AT+QCCID` 回退链,因不同模组/固件命令名不一。EC25 上 `AT+QCCID` 直接可用。

---

### §6 Network Service Commands(手册 p74-88)

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 | modem 现状 |
|---|---|---|---|---|---|---|
| 6.1 | `AT+COPS` | 运营商选择 | `AT+COPS=<mode>[,<format>[,<oper>[,<Act>]]]` | TS 27.007 | 🔵拨号 | — |
| 6.2 | `AT+CREG` | 网络注册状态 | `AT+CREG[=<n>]` | TS 27.007 | 🔵 | — |
| 6.3 | `AT+CSQ` | 信号质量(rssi,ber) | `AT+CSQ` | TS 27.007 | ✅SMS | ✅`SignalDBm`/`csqDBm` |
| 6.4 | `AT+CPOL` | 优选运营商列表 | `AT+CPOL=<index>[,<format>[,<oper>]]` | TS 27.007 | ⬜ | — |
| 6.5 | `AT+COPN` | 读运营商名称 | `AT+COPN` | TS 27.007 | ⬜ | — |
| 6.6 | `AT+CTZU` | 自动时区更新 | `AT+CTZU=<onoff>` | TS 27.007 | 🔧 | — |
| 6.7 | `AT+CTZR` | 时区上报 | `AT+CTZR=<reporting>` | TS 27.007 | 🔧 | — |
| 6.8 | `AT+QLTS` | 查询网络同步时间 | `AT+QLTS[=<mode>]` | Quectel | 🔧 | — |
| 6.9 | `AT+QNWINFO` | 查询网络信息(Act/运营商/频段/信道) | `AT+QNWINFO` | Quectel | 🔵 | — |
| 6.10 | `AT+QSPN` | 显示注册网络名 | `AT+QSPN` | Quectel | 🔵 | ✅`Carrier`(实际用 +COPS 解析) |

> **CSQ rssi 映射**:0=-113dBm,1=-111dBm,2..30=-109..-53dBm,31=≥-51dBm,99=未知。LTE 下 CSQ 偏乐观,`modem` 包优先用 `AT+CESQ` 取 RSRP/RSRQ(见下方说明)。

---

### §7 Call Related Commands(手册 p89-109) — ⬜ 整章不需要

本项目不做语音通话。命令清单:`ATA`(接听)、`ATD`(拨号)、`ATH`(挂断)、`AT+CVHU`、`AT+CHUP`、`+++`(数据→命令模式)、`ATO`(命令→数据模式)、`ATS0/6/7/8/10`、`AT+CBST`、`AT+CSTA`、`AT+CLCC`(列当前呼叫)、`AT+CR`、`AT+CRC`、`AT+CRLP`、`AT+QECCNUM`(紧急号码)、`AT+QHUP`。

> 唯一例外:`AT+CLCC`(列当前呼叫)在排查"是否有残留呼叫占用通道"时偶有用。

---

### §8 Phonebook Commands(手册 p110-115) — ⬜ 整章不需要

`AT+CNUM`(本机号)、`AT+CPBF`(查找)、`AT+CPBR`(读)、`AT+CPBS`(选存储)、`AT+CPBW`(写)。

> 注:`modem` 包 `PhoneNumber()` 读本机号实际走 `AT+CNUM`(部分模组用 `AT+CPBR` 读 MSISDN)。

---

### §9 Short Message Service Commands(手册 p116-145)— ✅ 阶段 1 核心

| § | 命令 | 描述 | 代表语法 | 标准 | modem 现状 |
|---|---|---|---|---|---|
| 9.1 | `AT+CSMS` | 选消息服务 | `AT+CSMS=<service>` | TS 27.005 | — |
| 9.2 | `AT+CMGF` | 消息格式(0=PDU/1=Text) | `AT+CMGF[=<mode>]` | TS 27.005 | ✅`Initialize` 设 `=0`(PDU) |
| 9.3 | `AT+CSCA` | 短信中心地址 | `AT+CSCA=<sca>[,<tosca>]` | TS 27.005 | (PDU 模式 SCA 写在 PDU 头,项目用 `00` 占位) |
| 9.4 | `AT+CPMS` | 优选消息存储 | `AT+CPMS=<mem1>[,<mem2>[,<mem3>]]` | TS 27.005 | ✅`Initialize` 设 `"SM","SM","SM"` |
| 9.5 | `AT+CMGD` | 删除消息 | `AT+CMGD=<index>[,<delflag>]` | TS 27.005 | ✅`DeleteStored` |
| 9.6 | `AT+CMGL` | 列消息 | `AT+CMGL[=<stat>]`(PDU:0..4) | TS 27.005 | ✅`ListStored`(`=4` 全部) |
| 9.7 | `AT+CMGR` | 读消息 | `AT+CMGR=<index>` | TS 27.005 | (读单条,项目用 CMGL 批量) |
| 9.8 | `AT+CMGS` | **发消息** | PDU:`AT+CMGS=<length><CR>` PDU `Ctrl+Z` | TS 27.005 | ✅`Send`(两步握手) |
| 9.9 | `AT+CMMS` | 多消息连续发送(保活链路) | `AT+CMMS=<n>` | TS 27.005 | — |
| 9.10 | `AT+CMGW` | 写消息到存储 | `AT+CMGW=<length>[,<stat>]<CR>` PDU | TS 27.005 | — |
| 9.11 | `AT+CMSS` | 从存储发送 | `AT+CMSS=<index>[,<da>[,<toda>]]` | TS 27.005 | — |
| 9.12 | `AT+CNMA` | 新消息确认(给网络) | `AT+CNMA=<n>` | TS 27.005 | — |
| 9.13 | `AT+CNMI` | SMS 事件上报配置 | `AT+CNMI=<mode>,<mt>,<bm>,<ds>,<bfr>` | TS 27.005 | ✅`Initialize` 设 `2,1,0,0,0` |
| 9.14 | `AT+CSCB` | 小区广播类型选择 | `AT+CSCB=<mode>[,<mids>[,<dcss>]]` | TS 27.005 | ⬜ |
| 9.15 | `AT+CSDH` | 显示文本模式头参数 | `AT+CSDH[=<show>]` | TS 27.005 | (PDU 模式下不相关) |
| 9.16 | `AT+CSMP` | 设置文本模式参数(fo/vp/pid/dcs) | `AT+CSMP=<fo>[,<vp>[,<pid>[,<dcs>]]]` | TS 27.005 | (PDU 模式这些写在 TPDU) |
| 9.17 | `AT+QCMGS` | 发长短信(拼接,8-bit UDH) | `AT+QCMGS=<da>[,<toda>][,<uid>,<seg>,<total>]<CR>` | Quectel | (项目用 PDU 自拼 UDH,见 pdu.go) |
| 9.18 | `AT+QCMGR` | 读拼接消息 | `AT+QCMGR` | Quectel | — |

> **`AT+CNMI=<mode>,<mt>,...` 关键值**:项目用 `mode=2`(TA-TE 链路忙时缓冲,否则直传)、`mt=1`(SMS-DELIVER 存储后用 `+CMTI: <mem>,<index>` 通知)。这组合要求 TE 主动 `AT+CMGR`/`AT+CMGL` 拉取,与项目轮询模型一致。

> **CMGS 两步握手坑**(项目已解):`AT+CMGS=<len>` 后模块返回 `> ` 提示符(**以空格结尾,无 `\n`**)。USB CDC-AT bulk endpoint 上 `ReadString('\n')` 会把 `>` 永久卡在 bufio 缓冲区。`usbtransport` 的 `readLine` 已做适配:部分读且 TrimSpace 后为 `>` 时当一行返回。

---

### §10 Packet Domain Commands(手册 p146-174)— 🔵 阶段 2 拨号核心

> 项目主走 **QMI**(经 quectel-qmi-go),但 AT PDP context 是理解数据通路的基础,且部分场景(AT 拨号 / ICMP 原生 ping)直接使用。

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 | modem 现状 |
|---|---|---|---|---|---|---|
| 10.1 | `AT+CGATT` | PS 附着/分离 | `AT+CGATT=<state>` | TS 27.007 | 🔵 | — |
| 10.2 | `AT+CGDCONT` | **定义 PDP context** | `AT+CGDCONT=<cid>,<type>,<APN>[,<addr>...]` | TS 27.007 | 🔵核心 | — |
| 10.3 | `AT+CGQREQ` | QoS Profile(请求) | `AT+CGQREQ=<cid>[,...]` | TS 27.007 | 🔵 | — |
| 10.4 | `AT+CGQMIN` | QoS Profile(最低可接受) | `AT+CGQMIN=<cid>[,...]` | TS 27.007 | 🔵 | — |
| 10.5 | `AT+CGEQREQ` | 3G QoS Profile(请求) | `AT+CGEQREQ=[<cid>,<Traffic class>...]` | TS 27.007 | 🔵 | — |
| 10.6 | `AT+CGEQMIN` | 3G QoS Profile(最低) | `AT+CGEQMIN=[<cid>,...]` | TS 27.007 | 🔵 | — |
| 10.7 | `AT+CGACT` | **激活/去激活 PDP context** | `AT+CGACT=<state>,<cid>` | TS 27.007 | 🔵核心 | ✅原生 ping 用(`=1,1`/`=0,1`) |
| 10.8 | `AT+CGDATA` | 进入数据态(PPP) | `AT+CGDATA=<L2P>[,<cid>]` | TS 27.007 | 🟢(PPP 路径,项目走 QMI 替代) | — |
| 10.9 | `AT+CGPADDR` | 显示 PDP 地址 | `AT+CGPADDR[=<cid>]` | TS 27.007 | 🔵🟢 | — |
| 10.10 | `AT+CGCLASS` | GPRS 移动台类别 | `AT+CGCLASS=<class>` | TS 27.007 | ⬜ | — |
| 10.11 | `AT+CGREG` | GPRS 网络注册状态 | `AT+CGREG[=<n>]` | TS 27.007 | 🔵 | — |
| 10.12 | `AT+CGEREP` | 分组域事件上报(`+CGEV`) | `AT+CGEREP=<mode>[,<bfr>]` | TS 27.007 | 🔵 | — |
| 10.13 | `AT+CGSMS` | 选 MO SMS 服务(GPRS/CS) | `AT+CGSMS=[<service>]` | TS 27.007 | 🔧 | — |
| 10.14 | `AT+CEREG` | **EPS 网络注册状态(LTE)** | `AT+CEREG[=<n>]` | TS 27.007 | 🔵核心 | (LTE 拨号前必查) |
| 10.15 | `AT+QGDCNT` | 数据计数器(收发字节) | `AT+QGDCNT=<op>` | Quectel | 🟢 | — |
| 10.16 | `AT+QAUGDCNT` | 自动保存数据计数器 | `AT+QAUGDCNT=<value>` | Quectel | 🟢 | — |

> **AT PDP 拨号三步**:`AT+CGDCONT=1,"IP","<APN>"` → `AT+CGACT=1,1` → `AT+CGDATA="PPP",1`(PPP)或走 QMI WDS。项目阶段 2 用 QMI WDS 替代 `CGDATA`,但 `CGDCONT`/`CGACT`/`CEREG` 仍是排查与备用路径。

---

### §11 Supplementary Service Commands(手册 p175-187) — ⬜ 整章不需要

呼叫转移/等待/保持/来电显示/CLIR/COLP/CSSN/USSD。命令:`AT+CCFC`、`AT+CCWA`、`AT+CHLD`、`AT+CLIP`、`AT+CLIR`、`AT+COLP`、`AT+CSSN`、`AT+CUSD`。

> 例外:`AT+CUSD`(USSD)在查话费/余额(`*100#` 之类)场景可能有用,但非项目目标。

---

### §12 Audio Commands(手册 p188-203) — ⬜ 整章不需要

音量/静音/音频回环/DTMF/数字音频接口/回声消除/侧音/MIC 增益/IIC/DTMF 检测。命令:`AT+CLVL`、`AT+CMUT`、`AT+QAUDLOOP`、`AT+VTS`、`AT+VTD`、`AT+QAUDMOD`、`AT+QDAI`、`AT+QEEC`、`AT+QSIDET`、`AT+QMIC`、`AT+QRXGAIN`、`AT+QIIC`、`AT+QTONEDET`、`AT+QLDTMF`、`AT+QLTONE`。

---

### §13 Hardware Related Commands(手册 p204-207)

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 |
|---|---|---|---|---|---|
| 13.1 | `AT+QPOWD` | 关机 | `AT+QPOWD[=<n>]`(0 立即/1 正常) | Quectel | 🔧(安全下电) |
| 13.2 | `AT+CCLK` | 实时时钟 | `AT+CCLK=<time>` / `AT+CCLK?` | TS 27.007 | 🔧(短信时间戳用网络 SCTS,不依赖 RTC) |
| 13.3 | `AT+CBC` | 电池电量/状态 | `AT+CBC` | TS 27.007 | 🔧 |
| 13.4 | `AT+QADC` | 读 ADC 电压 | `AT+QADC=<port>` | Quectel | ⬜ |
| 13.5 | `AT+QSCLK` | 慢时钟/睡眠模式 | `AT+QSCLK=<n>` | Quectel | 🔧(低功耗,DTR 控制) |

---

## 四、§14 Appendix 速查(手册 p208-231)— 🔧 高价值排障参考

### 14.1 引用标准

- **V.25ter** — 串口异步自动拨号与控制
- **3GPP TS 27.007** — UE AT 命令集(UMTS/LTE)
- **3GPP TS 27.005** — SMS/CBS 的 DTE-DCE 接口

### 14.5 +CME ERROR 码(摘,手册 Table 11)

> `AT+CMEE=1` 时以数字返回。本项目 `modem` 包默认 `CMEE=1`。

| 码 | 含义 | 码 | 含义 |
|---|---|---|---|
| 0 | Phone failure | 14 | SIM busy |
| 3 | Operation not allowed | 15 | SIM wrong |
| 4 | Operation not supported | 16 | Incorrect password |
| 10 | SIM not inserted | 17 | SIM PIN2 required |
| 11 | SIM PIN required | 18 | SIM PUK2 required |
| 12 | SIM PUK required | 20 | Memory full |
| 13 | SIM failure | 21 | Invalid index |
| 30 | No network service | 32 | Access not allowed... |
| 31 | Network timeout | 300+ | (各厂家扩展) |

### 14.6 +CMS ERROR 码(摘,手册 Table 12)

> SMS 相关错误用 `+CMS ERROR`(区别于 ME 的 `+CME ERROR`)。

| 码 | 含义 | 码 | 含义 |
|---|---|---|---|
| 300 | ME failure | 322 | Memory full |
| 305 | Invalid text mode | 330 | SMSC address unknown |
| 310 | SIM not inserted | 331 | No network |
| 311 | SIM pin necessary | 332 | Network timeout |
| 316 | SIM wrong | 500 | Unknown |
| 317 | SIM PUK required | 512 | SIM not ready |
| 321 | Invalid memory index | 513 | Message length exceeds |

### 14.7 URC 汇总(摘,手册 Table 13)

> 项目最关心的 URC:**`+CMTI: <mem>,<index>`**(新短信存入,`CNMI mt=1` 时触发)。

| URC | 含义 | 触发条件 |
|---|---|---|
| `+CMTI: <mem>,<index>` | 新短信存入存储 | `AT+CNMI` mt=1 |
| `+CMT: ...` | 新短信直传 TE | `AT+CNMI` mt=2/3 |
| `+CDS: ...` | 状态报告直传 | `AT+CNMI` ds=1 |
| `+CDSI: <mem>,<index>` | 状态报告存入 | `AT+CNMI` ds=2 |
| `+CREG: <stat>` | 网络注册变化 | `AT+CREG=1/2` |
| `+CGREG: <stat>` | GPRS 注册变化 | `AT+CGREG=1/2` |
| `+CEREG: <stat>` | EPS(LTE)注册变化 | `AT+CEREG=1/2` |
| `+CGEV: ...` | 分组域事件(PDN 激活/去激活等) | `AT+CGEREP=1/2` |
| `RDY` | ME 初始化完成 | 上电 |
| `+CFUN: 1` | 全功能可用 | 上电 |
| `+CPIN: <state>` | SIM PIN 状态 | 插拔 SIM / 上电 |
| `+QIND: SMS DONE` | SMS 初始化完成 | 上电 |
| `+QIND: PB DONE` | 电话本初始化完成 | 上电 |
| `POWERED DOWN` | 模块下电完成 | `AT+QPOWD` |
| `+CTZV: <tz>` / `+CTZE: ...` | 时区变化 | `AT+CTZR=1/2` |

### 14.8 SMS 字符集转换(手册 Table 14-21)

DCS(Data Coding Scheme)× `AT+CSCS` 决定文本模式 SMS 的输入输出方式:

| DCS | `AT+CSCS` | 输入/输出方式 |
|---|---|---|
| GSM 7-bit | GSM | 直接 GSM 字符 |
| GSM 7-bit | IRA | IRA↔GSM 转换 |
| GSM 7-bit | UCS2 | UCS2 hex 串 ↔ GSM |
| UCS2 | (忽略 CSCS) | PDU 式 hex 串(0-9,A-F) |
| 8-bit | (忽略 CSCS) | PDU 式 hex 串 |

> **本项目**:用 **PDU 模式**(`CMGF=0`),DCS 写在 TPDU 里,正文以 hex 串收发,GSM-7/UCS-2 编解码由 `smscodec` 包处理。`AT+CSCS` 在 PDU 模式下影响有限。手册提供完整 GSM/IRA 输入输出转换表 + 扩展字符表(§14.8 Table 15-21),排障乱码时查原文。

### 14.9 AT+CEER 释放原因(手册 Table 22)

`AT+CEER` 返回上次失败原因文本,分四类:CS Internal / CS Network / CS Network Reject / PS Internal / CS PS Network。LTE 数据拨号失败时查 PS 类原因(如 `PDP establish timeout`、`PDP activate timeout`、`Missing or unknown APN`、`Activation rejected by GGSN` 等)。排障拨号失败必查。

---

## 五、关键发现与注意事项

### 5.1 `AT+CESQ` 不在 V1.2 手册中

`modem` 包 `SignalMetrics()` 优先用 `AT+CESQ`(`+CESQ: <rxlev>,<ber>,<rscp>,<ecno>,<rsrq>,<rsrp>`)取 LTE 的 RSRP/RSRQ,但 **V1.2 手册(2017-11)未收录 `AT+CESQ`**。这是后续固件/手册版本新增的命令(源自 TS 27.007 后续 release)。EC25 实测支持。查 `CESQ` 完整语义需更新版手册或 TS 27.007 原文。

### 5.2 ICCID 命令名不统一

手册 V1.2 用 `AT+QCCID`(Quectel 私有)。`modem` 包 `ICCID()` 用回退链 `AT+CCID` → `AT+CICCID` → `AT+QCCID`。EC25 上 `AT+QCCID` 直接可用。实测本机 ICCID `89000000000000000000`(见 AGENTS.md)。

### 5.3 PDU 模式 SCA 占位

项目发短信 `AT+CMGS=<len>` 的 PDU 以 `00` 开头(SCA 占位字节 = "用 SIM 上存的 SMSC")。这与手册 §9.8 描述一致。若需指定 SMSC,改 SCA 字段。读短信时 SCA 字段在 PDU 头,`DecodeDeliver` 会先剥离。

### 5.4 CNMI 模式选择与轮询

项目 `CNMI=2,1,0,0,0`:`mode=2`(缓冲)、`mt=1`(存储+`+CMTI` 通知)。这要求 TE 收到 `+CMTI` 后主动 `CMGL`/`CMGR` 拉取。`modem` 包主循环轮询模型与此匹配。若要短信直传 TE(`mt=2/3`,省一次拉取),需改 `CNMI` 并处理 `+CMT:` URC 的 PDU 直传——USB 场景下 URC 与命令响应会交错,解析更复杂,故未采用。

### 5.5 阶段 2(QMI)对 AT 命令的依赖

项目拨号主走 QMI WDS(quectel-qmi-go),但以下 AT 命令仍是必要的基础/排障路径:
- `AT+CEREG?` —— LTE 注册状态(拨号前提)
- `AT+CGDCONT` —— 定义 PDP context(QMI 也需 CID 概念)
- `AT+CGACT` —— 激活/去激活(原生 ping 备用路径已用)
- `AT+QNWINFO` —— 查询当前 Act/频段/信道
- `AT+CEER` —— 拨号失败释放原因

### 5.6 `AT+CGDATA`(PPP)与 QMI 的取舍

`AT+CGDATA="PPP",1` 走 PPP 协议进入数据态,需 TE 实现 PPP 栈 + PPPoE/PPP-IPCP。项目阶段 3 用 QMI WDS 拿到原始 IP 包直接注入 TUN,避开 PPP 栈复杂度。`CGDATA` 作为理解参考保留。

---

## 六、`modem` 包已实现命令汇总

`third_party/sms-gateway/modem/` 当前(阶段 1 完成态)使用的 AT 命令:

| 命令 | 用途 | 手册章节 |
|---|---|---|
| `AT` | 链路 ping | §1.2 |
| `ATE0` | 关回显 | §2.16 |
| `AT+CMEE=1` | 数字错误码 | §2.23 |
| `AT+CPIN?` / `AT+CPIN=<pin>` | SIM 就绪检查/解锁 | §5.3 |
| `AT+CMGF=0` | PDU 模式 | §9.2 |
| `AT+CNMI=2,1,0,0,0` | 短信存储+`+CMTI` 通知 | §9.13 |
| `AT+CPMS="SM","SM","SM"` | SIM 存储 | §9.4 |
| `AT+CMGS=<len>` | 发短信(PDU 两步握手) | §9.8 |
| `AT+CMGL=4` | 列全部短信(PDU) | §9.6 |
| `AT+CMGD=<index>` | 删短信 | §9.5 |
| `AT+CSQ` | 信号质量(回退) | §6.3 |
| `AT+CESQ` | LTE 信号(RSRP/RSRQ,**手册 V1.2 未收录**) | — |
| `AT+QCCID`/`+CCID`/`+CICCID` | ICCID(回退链) | §5.7 |
| `AT+CGMI` / `AT+CGMM` / `ATI` | 设备信息 | §2.5/§2.6/§2.1 |
| `AT+CGSN` / `AT+GSN` | IMEI | §2.9/§2.8 |
| `AT+CNUM` | 本机号 | §8.1 |
| `AT+COPS?` | 运营商名 | §6.1 |
| `AT+CGACT=1,1` / `=0,1` | 原生 ping 时激活/去激活 PDP | §10.7 |
| `AT+MPING`/`AT+QPING`/`AT+CIPPING` | 原生 ping(模组厂商相关,**手册 V1.2 未收录**) | — |

> 注:`AT+CESQ`、`AT+QPING` 等较新/厂商命令不在 V1.2 手册内。手册是 2017 版基线,后续固件新增命令需查更新版手册或实测。

---

## 七、后续阶段建议补齐的命令

| 阶段 | 建议补齐 | 手册章节 |
|---|---|---|
| 🔵 阶段 2 拨号 | `AT+CEREG?`(LTE 注册)、`AT+CGDCONT`(PDP 定义)、`AT+QNWINFO`(网络信息) | §10.14 / §10.2 / §6.9 |
| 🔵 阶段 2 排障 | `AT+CEER`(释放原因)、`AT+CGEREP`(`+CGEV` 事件) | §4.2 / §10.12 |
| 🟢 阶段 3 上网 | `AT+CGPADDR`(PDP 地址)、`AT+QGDCNT`(流量统计) | §10.9 / §10.15 |
| 🟢 eSIM | `AT+CSIM`/`AT+CRSM`(APDU 透传,uicc-go 复用基础) | §5.5 / §5.6 |
| 🔧 健壮性 | `AT+QINISTAT`(初始化完成位)、`AT+CPIN?` 重试、`AT+CFUN`(功能级复位) | §5.9 / §5.3 / §2.22 |
