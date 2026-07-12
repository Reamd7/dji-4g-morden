# AT 命令全集对齐状态

> 把 Quectel EC25 V1.2 手册(`docs/08`)和 EC20 V1.1 手册(`docs/09`)里**每一条 AT 命令**
> 都列成对齐项,标注当前实现状态 + 上游来源(SG/VH),形成完整的对齐追踪矩阵。
> 创建于 2026-07-12。
>
> **以 EC25 V1.2(docs/08,133 条主命令)为全集**——EC20 V1.1 是其严格子集(docs/09 §〇已确认),
> 共享命令语法完全一致,不重复。EC20 独有命令(`airplanecontrol`)单独标注。

---

## 一、状态图例

| 标记 | 含义 |
|---|---|
| ✅ | **已实现**——modem 包有对应方法,已测试 |
| 🔲 | **待实现**——项目需要,尚未对齐 |
| 🔧 | **按需参考**——排障/配置类,不做成正式 API,需要时用 `SendAndWait` 手发 |
| ⬜ | **不需要**——语音通话/电话本/音频/补充业务,本项目不做 |

---

## 二、总览统计

| 状态 | 命令数 | 占比 |
|---|---|---|
| ✅ 已实现 | 39(+手册外 3 = 42) | 29% |
| 🔲 待实现 | 23 | 17% |
| 🔧 按需参考 | 12 | 9% |
| ⬜ 不需要 | 59 | 44% |
| **合计** | **133** | 100% |

> ⬜ 占比最高(语音/电话本/音频),因为本项目是 4G modem 数据+短信,不做语音。
> 2026-07-12 补齐 11 条 vohive 覆盖的命令(CIMI/CGMR/CFUN/CGATT/CGDCONT/CREG/QNWINFO/QCFG/CSIM/CRSM/CUSD),✅ 从 28→39。

---

## 三、逐章对齐清单

### §2 General Commands(25 条)

| § | 命令 | 标准 | 阶段 | 状态 | 实现方法 / 说明 |
|---|---|---|---|---|---|
| 2.1 | `ATI` | V.25ter | 🔧 | ✅ | `probeIdentity()` 回退用 |
| 2.2 | `AT+GMI` | V.25ter | 🔧 | 🔲 | 厂商标识(项目用 +CGMI 替代) |
| 2.3 | `AT+GMM` | V.25ter | 🔧 | 🔲 | 型号标识(项目用 +CGMM 替代) |
| 2.4 | `AT+GMR` | V.25ter | 🔧 | 🔲 | 软件版本 |
| 2.5 | `AT+CGMI` | TS 27.007 | 🔧 | ✅ | `probeIdentity()` |
| 2.6 | `AT+CGMM` | TS 27.007 | 🔧 | ✅ | `probeIdentity()` |
| 2.7 | `AT+CGMR` | TS 27.007 | 🔧 | 🔲 | 软件版本(可与 +GMR 二选一) |
| 2.8 | `AT+GSN` | V.25ter | ✅SMS | ✅ | `IMEI()` 回退链 |
| 2.9 | `AT+CGSN` | TS 27.007 | ✅SMS | ✅ | `IMEI()` 首选 |
| 2.10 | `AT&F` | V.25ter | 🔧 | 🔲 | 恢复出厂(排障用) |
| 2.11 | `AT&V` | V.25ter | 🔧 | 🔲 | 显示配置(排障用) |
| 2.12 | `AT&W` | V.25ter | 🔧 | 🔲 | 存参数(一般不需要) |
| 2.13 | `ATZ` | V.25ter | 🔧 | 🔲 | 恢复 profile |
| 2.14 | `ATQ` | V.25ter | 🔧 | 🔲 | 结果码模式 |
| 2.15 | `ATV` | V.25ter | 🔧 | 🔲 | 响应格式 |
| 2.16 | `ATE` | V.25ter | ✅SMS | ✅ | `Initialize` 发 `ATE0` |
| 2.17 | `A/` | V.25ter | ⬜ | ⬜ | 重复上命令 |
| 2.18 | `ATS3` | V.25ter | 🔧 | 🔲 | 终止符(默认 CR,一般不动) |
| 2.19 | `ATS4` | V.25ter | 🔧 | 🔲 | 响应格式符(默认 LF) |
| 2.20 | `ATS5` | V.25ter | 🔧 | 🔲 | 编辑符(默认 BS) |
| 2.21 | `ATX` | V.25ter | ⬜ | ⬜ | CONNECT 结果码(拨号场景) |
| 2.22 | `AT+CFUN` | TS 27.007 | 🔧 | 🔲 | 功能级(复位 `=1,1`、飞行 `=4`;健壮性用) |
| 2.23 | `AT+CMEE` | TS 27.007 | ✅SMS | ✅ | `Initialize` 设 `=1` |
| 2.24 | `AT+CSCS` | TS 27.007 | ✅SMS | ✅ | `SetCharset(ctx, charset)` |
| 2.25 | `AT+QURCCFG` | Quectel | 🔧 | 🔲 | URC 输出口(USB 场景可指定 usbat) |

---

### §3 Serial Interface Control(6 条)— USB 场景基本不适用

| § | 命令 | 标准 | 状态 | 说明 |
|---|---|---|---|---|
| 3.1 | `AT&C` | V.25ter | ⬜ | DCD 模式(USB 无 DCD) |
| 3.2 | `AT&D` | V.25ter | ⬜ | DTR 模式(USB 无 DTR) |
| 3.3 | `AT+IFC` | V.25ter | ⬜ | RTS/CTS 流控(USB bulk 无流控) |
| 3.4 | `AT+ICF` | V.25ter | ⬜ | 字符帧(USB 固定) |
| 3.5 | `AT+IPR` | V.25ter | ⬜ | 波特率(USB 固定) |
| 3.6 | `AT+QRIR` | Quectel | ⬜ | RI 复位(USB 无物理 RI) |

---

### §4 Status Control(4 主命令 + 19 QCFG 子项)

| § | 命令 | 标准 | 阶段 | 状态 | 说明 |
|---|---|---|---|---|---|
| 4.1 | `AT+CPAS` | TS 27.007 | 🔧 | 🔲 | ME 活动状态(排障) |
| 4.2 | `AT+CEER` | TS 27.007 | 🔧 | 🔲 | 上次失败原因(拨号排障必查) |
| 4.3 | `AT+QCFG` | Quectel | 🔵🔧 | 🔲 | 扩展配置(19 子项见下) |
| 4.4 | `AT+QINDCFG` | Quectel | 🔧 | 🔲 | URC 指示配置(csq/smsfull/ring 等) |

#### §4.3 `AT+QCFG` 子配置(19 项)

| 子配置 | 阶段 | 状态 | 说明 |
|---|---|---|---|
| `"gprsattach"` | 🔵 | 🔲 | GPRS 附着模式 |
| `"nwscanmode"` | 🔵 | 🔲 | 搜索模式(AUTO/LTE only) |
| `"nwscanseq"` | 🔵 | 🔲 | 搜索顺序 |
| `"roamservice"` | 🔵 | 🔲 | 漫游服务 |
| `"servicedomain"` | 🔵 | 🔲 | 服务域(拨号前需 PS) |
| `"band"` | 🔵 | 🔲 | 频段配置 |
| `"hsdpacat"` | ⬜ | ⬜ | HSDPA 类别(3G,本项目 LTE) |
| `"hsupacat"` | ⬜ | ⬜ | HSUPA 类别 |
| `"rrc"` | ⬜ | ⬜ | RRC 版本 |
| `"sgsn"` | ⬜ | ⬜ | SGSN 版本 |
| `"msc"` | ⬜ | ⬜ | MSC 版本 |
| `"pdp/duplicatechk"` | 🔵 | 🔲 | 同 APN 多 PDN |
| `"urc/ri/ring"` | ⬜ | ⬜ | RING 的 RI 行为(USB 无 RI) |
| `"urc/ri/smsincoming"` | ⬜ | ⬜ | 收短信 RI |
| `"urc/ri/other"` | ⬜ | ⬜ | 其他 URC RI |
| `"risignaltype"` | ⬜ | ⬜ | RI 信号类型 |
| `"urc/delay"` | ⬜ | ⬜ | URC 延迟 |
| `"urc/cache"` | 🔧 | 🔲 | URC 缓存(USB 断连重连) |
| `"tone/incoming"` | ⬜ | ⬜ | 来电铃声 |

---

### §5 (U)SIM Related(11 条)

| § | 命令 | 标准 | 阶段 | 状态 | 实现方法 / 说明 |
|---|---|---|---|---|---|
| 5.1 | `AT+CIMI` | TS 27.007 | ✅SMS | 🔲 | IMSI(设备信息,与 ICCID 互补) |
| 5.2 | `AT+CLCK` | TS 27.007 | 🔧 | 🔲 | 设施锁(PIN 锁状态查询) |
| 5.3 | `AT+CPIN` | TS 27.007 | ✅SMS | ✅ | `Initialize` + `ensureSIMReady`(查询+PIN 解锁) |
| 5.4 | `AT+CPWD` | TS 27.007 | 🔧 | 🔲 | 改密码 |
| 5.5 | `AT+CSIM` | TS 27.007 | 🟢eSIM | 🔲 | APDU 透传(eSIM/eUICC,uicc-go 复用基础) |
| 5.6 | `AT+CRSM` | TS 27.007 | 🟢eSIM | 🔲 | 受限 SIM 访问(READ/UPDATE BINARY) |
| 5.7 | `AT+QCCID` | Quectel | ✅SMS | ✅ | `probeICCID()` 回退链首选 |
| 5.8 | `AT+QPINC` | Quectel | 🔧 | 🔲 | PIN/PUK 剩余次数(排障) |
| 5.9 | `AT+QINISTAT` | Quectel | 🔧 | 🔲 | 初始化完成位(1=CPIN,2=SMS,4=PB) |
| 5.10 | `AT+QSIMDET` | Quectel | ⬜ | ⬜ | 热插拔检测(USB 模组无 GPIO) |
| 5.11 | `AT+QSIMSTAT` | Quectel | ⬜ | ⬜ | SIM 插入状态上报 |

---

### §6 Network Service(10 条)

| § | 命令 | 标准 | 阶段 | 状态 | 实现方法 / 说明 |
|---|---|---|---|---|---|
| 6.1 | `AT+COPS` | TS 27.007 | ✅SMS/🔵 | ✅ | `Carrier()`(`=3,0` 设格式 + `?` 查询) |
| 6.2 | `AT+CREG` | TS 27.007 | 🔵 | 🔲 | 网络注册状态(拨号前查) |
| 6.3 | `AT+CSQ` | TS 27.007 | ✅SMS | ✅ | `csqDBm()`(CESQ 回退) |
| 6.4 | `AT+CPOL` | TS 27.007 | ⬜ | ⬜ | 优选运营商列表 |
| 6.5 | `AT+COPN` | TS 27.007 | ⬜ | ⬜ | 读运营商名称 |
| 6.6 | `AT+CTZU` | TS 27.007 | 🔧 | 🔲 | 自动时区更新 |
| 6.7 | `AT+CTZR` | TS 27.007 | 🔧 | 🔲 | 时区上报(`+CTZV` URC) |
| 6.8 | `AT+QLTS` | Quectel | 🔧 | 🔲 | 查询网络同步时间 |
| 6.9 | `AT+QNWINFO` | Quectel | 🔵 | 🔲 | 网络信息(Act/运营商/频段/信道) |
| 6.10 | `AT+QSPN` | Quectel | 🔵 | 🔲 | 注册网络名(项目用 +COPS 替代) |

> **注**:`AT+CESQ`(LTE RSRP/RSRQ)已被 `SignalMetrics()` 实现,但**不在 V1.2 手册中**
> (2017 后固件新增)。计入已实现但不计入手册 133 条统计。

---

### §7 Call Related(20 条)— ⬜ 整章不需要

本项目不做语音通话。命令:`ATA` `ATD` `ATH` `AT+CVHU` `AT+CHUP` `+++` `ATO` `ATS0` `ATS6` `ATS7` `ATS8` `ATS10` `AT+CBST` `AT+CSTA` `AT+CLCC` `AT+CR` `AT+CRC` `AT+CRLP` `AT+QECCNUM` `AT+QHUP`。

> 例外:`AT+CLCC`(列当前呼叫)排查残留呼叫时偶用,按需 🔧。

---

### §8 Phonebook(5 条)— ⬜ 整章不需要

`AT+CNUM` `AT+CPBF` `AT+CPBR` `AT+CPBS` `AT+CPBW`。

> 例外:`AT+CNUM`(本机号)**已实现**(`PhoneNumber()`)。

---

### §9 Short Message Service(18 条)— ✅ 阶段 1 核心

| § | 命令 | 标准 | 状态 | 实现方法 / 说明 |
|---|---|---|---|---|
| 9.1 | `AT+CSMS` | TS 27.005 | 🔲 | 选消息服务(GSM/UMTS/LTE) |
| 9.2 | `AT+CMGF` | TS 27.005 | ✅ | `Initialize` 设 `=0`(PDU) |
| 9.3 | `AT+CSCA` | TS 27.005 | ✅ | `SMSC(ctx)` |
| 9.4 | `AT+CPMS` | TS 27.005 | ✅ | `Initialize` 设 `"SM","SM","SM"` |
| 9.5 | `AT+CMGD` | TS 27.005 | ✅ | `DeleteStored` + `DeleteAllStored`(=1,4) |
| 9.6 | `AT+CMGL` | TS 27.005 | ✅ | `ListStored`(`=4`) |
| 9.7 | `AT+CMGR` | TS 27.005 | ✅ | `ReadStored(ctx, index)` |
| 9.8 | `AT+CMGS` | TS 27.005 | ✅ | `Send`(多段 CMGS 两步握手) |
| 9.9 | `AT+CMMS` | TS 27.005 | 🔲 | 多消息连续发送(保活链路) |
| 9.10 | `AT+CMGW` | TS 27.005 | 🔲 | 写消息到存储 |
| 9.11 | `AT+CMSS` | TS 27.005 | 🔲 | 从存储发送 |
| 9.12 | `AT+CNMA` | TS 27.005 | 🔲 | 新消息确认(CNMI=2,1 存储模式下非必需) |
| 9.13 | `AT+CNMI` | TS 27.005 | ✅ | `Initialize` 设 `2,1,0,0,0` |
| 9.14 | `AT+CSCB` | TS 27.005 | ⬜ | 小区广播(不做) |
| 9.15 | `AT+CSDH` | TS 27.005 | 🔲 | 文本模式头(PDU 模式不相关,按需) |
| 9.16 | `AT+CSMP` | TS 27.005 | 🔲 | 文本模式参数(PDU 模式写在 TPDU) |
| 9.17 | `AT+QCMGS` | Quectel | 🔲 | 发长短信(拼接,项目用 PDU 自拼 UDH) |
| 9.18 | `AT+QCMGR` | Quectel | 🔲 | 读拼接消息 |

> **阶段 1 核心已全对齐**:CMGF/CSCA/CPMS/CMGD/CMGL/CMGR/CMGS/CNMI 八条 ✅。
> 剩余 🔲 是进阶能力(写存储/从存储发/连续发送),非收发短信必需。

---

### §10 Packet Domain(16 条)— 🔵 阶段 2 拨号核心

| § | 命令 | 标准 | 阶段 | 状态 | 实现方法 / 说明 |
|---|---|---|---|---|---|
| 10.1 | `AT+CGATT` | TS 27.007 | 🔵 | 🔲 | PS 附着/分离 |
| 10.2 | `AT+CGDCONT` | TS 27.007 | 🔵核心 | 🔲 | 定义 PDP context(cid/type/APN) |
| 10.3 | `AT+CGQREQ` | TS 27.007 | 🔵 | 🔲 | QoS(请求) |
| 10.4 | `AT+CGQMIN` | TS 27.007 | 🔵 | 🔲 | QoS(最低) |
| 10.5 | `AT+CGEQREQ` | TS 27.007 | 🔵 | 🔲 | 3G QoS(请求) |
| 10.6 | `AT+CGEQMIN` | TS 27.007 | 🔵 | 🔲 | 3G QoS(最低) |
| 10.7 | `AT+CGACT` | TS 27.007 | 🔵 | ✅ | 原生 ping 用(`=1,1`/`=0,1`) |
| 10.8 | `AT+CGDATA` | TS 27.007 | 🟢 | 🔲 | 进入数据态(PPP;项目走 QMI 替代) |
| 10.9 | `AT+CGPADDR` | TS 27.007 | 🟢 | 🔲 | PDP 地址(上网后查 IP) |
| 10.10 | `AT+CGCLASS` | TS 27.007 | ⬜ | ⬜ | GPRS 类别 |
| 10.11 | `AT+CGREG` | TS 27.007 | 🔵 | 🔲 | GPRS 注册状态 |
| 10.12 | `AT+CGEREP` | TS 27.007 | 🔵 | 🔲 | 分组域事件(`+CGEV` URC) |
| 10.13 | `AT+CGSMS` | TS 27.007 | 🔧 | 🔲 | MO SMS 服务选择(GPRS/CS) |
| 10.14 | `AT+CEREG` | TS 27.007 | 🔵核心 | 🔲 | EPS(LTE)注册状态(拨号前提) |
| 10.15 | `AT+QGDCNT` | Quectel | 🟢 | 🔲 | 数据计数器 |
| 10.16 | `AT+QAUGDCNT` | Quectel | 🟢 | 🔲 | 自动保存数据计数器 |

> 阶段 2 拨号主走 QMI WDS,但 `CGDCONT`/`CGACT`/`CEREG` 仍是必要基础/排障路径。

---

### §11 Supplementary Service(8 条)— ⬜ 不需要

`AT+CCFC` `AT+CCWA` `AT+CHLD` `AT+CLIP` `AT+CLIR` `AT+COLP` `AT+CSSN` `AT+CUSD`。

> 例外:`AT+CUSD`(USSD)查话费/余额场景可能有用,按需 🔧。

---

### §12 Audio(15 条)— ⬜ 整章不需要

`AT+CLVL` `AT+CMUT` `AT+QAUDLOOP` `AT+VTS` `AT+VTD` `AT+QAUDMOD` `AT+QDAI` `AT+QEEC` `AT+QSIDET` `AT+QMIC` `AT+QRXGAIN` `AT+QIIC` `AT+QTONEDET` `AT+QLDTMF` `AT+QLTONE`。

---

### §13 Hardware Related(5 条)

| § | 命令 | 标准 | 阶段 | 状态 | 说明 |
|---|---|---|---|---|---|
| 13.1 | `AT+QPOWD` | Quectel | 🔧 | 🔲 | 安全下电 |
| 13.2 | `AT+CCLK` | TS 27.007 | 🔧 | 🔲 | 实时时钟(短信时间戳用 SCTS,不依赖) |
| 13.3 | `AT+CBC` | TS 27.007 | 🔧 | 🔲 | 电池电量/状态 |
| 13.4 | `AT+QADC` | Quectel | ⬜ | ⬜ | 读 ADC |
| 13.5 | `AT+QSCLK` | Quectel | 🔧 | 🔲 | 慢时钟/睡眠(低功耗) |

---

### EC20 独有(1 条)

| 命令 | 章节 | 状态 | 说明 |
|---|---|---|---|
| `AT+QCFG="airplanecontrol"` | EC20 §4.3.12 | ⬜ | 飞行模式检测(W_DISABLE# 引脚);USB 无引脚控制权,用 `AT+CFUN=4` 替代 |

---

### 手册外已实现(2 条)

| 命令 | 状态 | 说明 |
|---|---|---|
| `AT+CESQ` | ✅ | LTE RSRP/RSRQ;`SignalMetrics()` 首选;**V1.2 手册未收录**(后续固件新增) |
| `AT+MPING`/`AT+QPING`/`AT+CIPPING` | ✅ | 原生 ping(三方言探测);**V1.2 手册未收录** |

---

## 三·二、命令上游溯源(SG / VH 对照)

> 每条"本项目相关"的命令,标注上游两个来源(sms_gateway 原始 / vohive)是否实现。
> 依据 `docs/05`(SG 剖析)、`docs/06`(VH 剖析)、`docs/07`(对比)。
>
> - **SG** = sms_gateway 原始代码(`source/sms_gateway/agent/internal/modem/`,升级前)
> - **VH** = vohive Manager(`source/vohive-collection/vohive/internal/modem/`)
> - ✅ 有实现　❌ 没有　— 未查/不明确　⬜ 不需要
>
> 本项目 = SG 壳(升级后)。看"本项目有、SG 原始没有"的命令 = 我们从 VH 借鉴或自研补齐的。

### 初始化 + AT 语法(§2 / §5 / §9 init)

| 命令 | 本项目 | SG 原始 | VH | 来源说明 |
|---|---|---|---|---|
| `AT` | ✅ | ✅ | ✅ | 两边都有 |
| `ATE0` | ✅ | ✅ | ✅ | 两边都有 |
| `AT+CMEE=1` | ✅ | ✅ | ❌ | **SG 独有**(VH 从不发,缺陷) |
| `AT+CPIN?` | ✅ | ✅ | ❌ | **SG 独有**(VH 用私有 QSIMSTAT 替代,缺陷) |
| `AT+CMGF=0` | ✅ | ✅ | ✅ | 两边都有 |
| `AT+CNMI` | ✅ | ✅ | ✅ | 两边都有 |
| `AT+CPMS` | ✅(预设) | ✅(预设) | ✅(切换+恢复) | SG 预设;VH 更贴合标准(临时切换+恢复) |
| `AT+CSCS` | ✅ | ❌ | ✅ | 本项目从 VH 对齐补齐 |

### SMS 命令族(§9)

| 命令 | 本项目 | SG 原始 | VH | 来源说明 |
|---|---|---|---|---|
| `AT+CMGS` | ✅ | ✅(两步握手) | ✅(interactive) | 两边都有;本项目多段循环借鉴 VH |
| `AT+CMGL` | ✅ | ✅ | ✅(+PDU 裁剪) | VH 更鲁棒(长度裁剪);本项目经 smscodec 补齐 |
| `AT+CMGR` | ✅ | ❌ | ✅(`SMSReadPDU`) | **本项目从 VH 对齐补齐** |
| `AT+CMGD` 单条 | ✅ | ✅ | ✅ | 两边都有 |
| `AT+CMGD=1,4` 全删 | ✅ | ❌ | ✅ | **本项目从 VH 对齐补齐** |
| `AT+CSCA` | ✅ | ❌ | ✅(`QuerySMSC`) | **本项目从 VH 对齐补齐** |
| `AT+CSMS` | 🔲 | ❌ | ❌ | 两边都没有 |
| `AT+CMGW`/`CMSS`/`CMMS` | 🔲 | ❌ | ❌ | 两边都没有(写存储/从存储发) |
| `AT+CNMA` | 🔲 | ❌ | ❌ | 两边都没有(CNMI=2,1 模式下非必需) |
| `AT+CSDH`/`CSMP` | 🔲 | ❌ | ❌ | 两边都没有(PDU 模式不相关) |
| `AT+QCMGS`/`QCMGR` | 🔲 | ❌ | ❌ | Quectel 拼接短信(本项目用 PDU 自拼 UDH) |

### 设备信息 + 信号(§2 / §5 / §6)

| 命令 | 本项目 | SG 原始 | VH | 来源说明 |
|---|---|---|---|---|
| `ATI`/`AT+CGMI`/`AT+CGMM` | ✅ | ✅ | — | SG 有 |
| `AT+CGSN`/`AT+GSN`(IMEI) | ✅ | ✅ | — | SG 有 |
| `AT+QCCID`(ICCID) | ✅ | ✅(回退链) | ✅ | 两边都有 |
| `AT+CNUM`(本机号) | ✅ | ✅ | — | SG 有 |
| `AT+COPS?`(运营商) | ✅ | ✅ | ✅ | 两边都有 |
| `AT+CSQ` | ✅ | ✅ | ✅(`QueryCSQ`) | 两边都有 |
| `AT+CESQ`(LTE RSRP) | ✅ | ✅(SG 原始有) | ❌(VH 用 QENG) | SG 有;VH 走私有 QENG |
| `AT+CIMI`(IMSI) | 🔲 | ❌ | — | 两边都没做成正式 API |
| `AT+QSIMSTAT` | ⬜ | ❌ | ✅ | VH 私有(替代标准 CPIN,本项目不采用) |
| `AT+QENG="servingcell"` | ⬜ | ❌ | ✅ | VH 私有 LTE 信号(本项目用标准 CESQ) |

### 拨号 / 数据(§10)

| 命令 | 本项目 | SG 原始 | VH | 来源说明 |
|---|---|---|---|---|
| `AT+CGACT` | ✅(ping) | ✅(ping) | — | SG 有(原生 ping 路径) |
| `AT+CEREG` | 🔲 | ❌ | — | 两边都没做成正式 API(阶段 2 补) |
| `AT+CGDCONT` | 🔲 | ❌ | — | 两边都没有(阶段 2 补) |
| `AT+CGATT`/`CGREG`/`CGEREP` | 🔲 | ❌ | — | 两边都没有(阶段 2 补) |
| `AT+QNWINFO` | 🔲 | ❌ | ✅ | VH 有;本项目待补 |
| `AT+CFUN`(复位) | 🔲 | ❌ | ✅(`CFUN=%d`) | VH 有;本项目待补(排障用) |
| `AT+MPING`/`QPING`/`CIPPING` | ✅ | ✅ | ❌ | SG 有(三方言探测);手册外 |

### URC / CMTI 编排(C 类)

| 能力 | 本项目 | SG 原始 | VH | 来源说明 |
|---|---|---|---|---|
| URC dispatch 框架 | ✅ | ✅(`OnURC`) | ✅(`handleURC`) | 两边都有 |
| URC/响应区分机制 | ✅ 干净(in-flight call) | ✅ 干净 | ⚠️ 黑名单(`isURC`) | **SG 架构更优** |
| +CMTI 自动读短信 | ✅(`SetSMSCallback`) | ❌(只 dispatch) | ✅(CMGR→decode→重组→CMGD) | **本项目从 VH 对齐补齐** |
| 内置 URC 种类 | ⚠️ 框架(白盒) | ⚠️ 框架 | ✅ 丰富(+CPIN/+CUSD/RING/...) | VH 更多;本项目按需扩展 |

### PDU 编解码(D 类)

| 维度 | 本项目 | SG 原始 | VH | 来源说明 |
|---|---|---|---|---|
| PDU 编解码内核 | ✅(smscodec) | ⚠️ 手写(3 缺陷) | ✅(smscodec) | **本项目从 VH 引入 smscodec** |
| GSM-7 扩展表 | ✅ | ❌(放弃) | ✅(warthog618) | 本项目经 smscodec 补齐 |
| 长短信自动分段 | ✅ | ❌(超长报错) | ✅(`BuildSubmitTPDUs`) | 本项目经 smscodec 补齐 |
| 长短信重组 | ✅(`Reassembler`) | ❌(只返回 ConcatInfo) | ✅(`Reassembler`) | 本项目经 smscodec 补齐 |
| 国产模组容错 | ✅(pdu_trim) | ❌ | ✅(spare bit/PDU 裁剪) | 本项目经 smscodec 补齐 |

### 溯源小结

| 来源 | 命令数 | 说明 |
|---|---|---|
| **SG 原始就有**(壳继承) | ~18 | AT/ATE0/CMEE/CPIN/CMGF/CNMI/CPMS/CMGS/CMGL/CMGD单条/CSQ/CESQ/CGSN/QCCID/CGMI/CGMM/ATI/CNUM/COPS/CGACT/ping |
| **从 VH 对齐补齐** | ~19 | SMS:CSCA/CMGR/CMGD全删/CSCS/CMTI编排/Reassembler/smscodec PDU/PDU容错 + 设备/网络/配置:CIMI/CGMR/CFUN/CGATT/CGDCONT/CREG/QNWINFO/QCFG/CSIM/CRSM/CUSD |
| **本项目自研** | ~5 | NewFromIO(USB注入)/encodeSubmitPDUs(SCA前缀)/readLine逐字节(方向F)/长生命期读/Facade DecodeDeliver(SCA剥离) |
| **不补(两上游都没有)** | ~23 | CEREG/CGREG/CGEREP/CSMS/CMGW/CMSS/QoS系列/CEER/QINISTAT 等(两边源码都没发过) |

---

## 四、待实现项优先级分组

> **2026-07-12 更新**:本节原列 34 条 🔲。其中 11 条(SG/VH 有覆盖的)已在 `plans/at-commands-roadmap.md` Phase A-E 实现完毕——CIMI/CGMR/CFUN/CGATT/CGDCONT/CREG/QNWINFO/QCFG/CSIM/CRSM/CUSD,各有离线测试,build+vet+`-race` 全绿。剩余 23 条 🔲 是两边都没覆盖的(不补)。下表保留原计划作历史参考。

### P0 — 阶段 1 收尾(短信能力补全,低风险)

| 命令 | 章节 | 价值 | 建议 |
|---|---|---|---|
| `AT+CIMI` | §5.1 | IMSI(与 ICCID 互补,设备信息完整) | 加 `IMSI(ctx)` 方法 |
| `AT+CSMS` | §9.1 | 选消息服务(确认支持 GSM/UMTS/LTE SMS) | Initialize 可选加 |

### P1 — 阶段 2 拨号(🔵 核心路径)

| 命令 | 章节 | 价值 |
|---|---|---|
| `AT+CEREG` | §10.14 | **LTE 注册状态**(拨号前提,必查) |
| `AT+CGDCONT` | §10.2 | **定义 PDP context**(cid/APN) |
| `AT+CGATT` | §10.1 | PS 附着状态 |
| `AT+CGREG` | §10.11 | GPRS 注册状态(2G/3G 回退) |
| `AT+CGEREP` | §10.12 | 分组域事件 `+CGEV` URC(PDN 激活/去激活) |
| `AT+QNWINFO` | §6.9 | 网络 Act/频段/信道(排障) |
| `AT+CREG` | §6.2 | CS 注册状态 |

> 拨号主走 QMI WDS(quectel-qmi-go),但这些 AT 命令是 QMI 之外的排查/备用路径。

### P2 — 阶段 3 上网(🟢 TUN + 数据)

| 命令 | 章节 | 价值 |
|---|---|---|
| `AT+CGPADDR` | §10.9 | PDP 地址(拿到分配的 IP) |
| `AT+QGDCNT` | §10.15 | 流量统计 |
| `AT+CGDATA` | §10.8 | PPP 数据态(项目走 QMI 替代,备查) |

### P3 — eSIM(🟢 阶段 3 扩展)

| 命令 | 章节 | 价值 |
|---|---|---|
| `AT+CSIM` | §5.5 | APDU 透传(uicc-go 复用基础) |
| `AT+CRSM` | §5.6 | 受限 SIM 访问(读/写 EF) |

### P4 — 排障 / 健壮性(🔧 按需)

| 命令 | 章节 | 价值 |
|---|---|---|
| `AT+CEER` | §4.2 | 上次失败原因(拨号/短信失败排障) |
| `AT+CFUN` | §2.22 | 功能级(复位 `=1,1`、飞行模式 `=4`) |
| `AT+QINISTAT` | §5.9 | 初始化完成位(开机轮询就绪) |
| `AT+QPINC` | §5.8 | PIN/PUK 剩余次数 |
| `AT+CPAS` | §4.1 | ME 活动状态 |
| `AT+QPOWD` | §13.1 | 安全下电 |
| `AT+CBC` | §13.3 | 电池状态 |
| `AT+QINDCFG` | §4.4 | URC 指示配置(smsfull/csq 等) |
| `AT+QURCCFG` | §2.25 | URC 输出口(USB 指定 usbat) |
| `AT+CTZU`/`AT+CTZR` | §6.6/6.7 | 时区更新/上报 |
| `AT+QLTS` | §6.8 | 网络同步时间 |
| `AT+CGSMS` | §10.13 | MO SMS 服务选择 |
| `AT+CLCC` | §7(例外) | 列当前呼叫(排残留) |
| `AT+CUSD` | §11(例外) | USSD(查话费) |

---

## 五、已实现命令完整清单(✅ 28 + 手册外 3 = 31 条)

| 命令 | 方法 | 章节 | 阶段 |
|---|---|---|---|
| `AT` | `Initialize` | §1 | ✅SMS |
| `ATE0` | `Initialize` | §2.16 | ✅SMS |
| `AT+CMEE=1` | `Initialize` | §2.23 | ✅SMS |
| `AT+CSCS` | `SetCharset` | §2.24 | ✅SMS |
| `AT+CPIN?`/`=pin` | `Initialize`/`ensureSIMReady` | §5.3 | ✅SMS |
| `AT+CMGF=0` | `Initialize` | §9.2 | ✅SMS |
| `AT+CNMI=2,1,0,0,0` | `Initialize` | §9.13 | ✅SMS |
| `AT+CPMS="SM"` | `Initialize` | §9.4 | ✅SMS |
| `AT+CMGS=<len>` | `Send` | §9.8 | ✅SMS |
| `AT+CMGL=4` | `ListStored` | §9.6 | ✅SMS |
| `AT+CMGR=<idx>` | `ReadStored` | §9.7 | ✅SMS |
| `AT+CMGD=<idx>` | `DeleteStored` | §9.5 | ✅SMS |
| `AT+CMGD=1,4` | `DeleteAllStored` | §9.5 | ✅SMS |
| `AT+CSCA?` | `SMSC` | §9.3 | ✅SMS |
| `AT+CSQ` | `csqDBm` | §6.3 | ✅SMS |
| `AT+CESQ` | `SignalMetrics` | 手册外 | ✅SMS |
| `AT+CGSN`/`AT+GSN` | `IMEI` | §2.9/2.8 | ✅SMS |
| `AT+QCCID`/`+CCID` | `probeICCID` | §5.7 | ✅SMS |
| `AT+CGMI`/`+CGMM`/`ATI` | `probeIdentity` | §2.5/2.6/2.1 | 🔧 |
| `AT+CNUM` | `PhoneNumber` | §8.1 | 🔧 |
| `AT+COPS?` | `Carrier` | §6.1 | ✅SMS/🔵 |
| `AT+CGACT=1,1`/`=0,1` | `Ping`(原生 ping) | §10.7 | 🔵 |
| `AT+MPING`/`+QPING`/`+CIPPING` | `Ping` | 手册外 | 🔵 |

---

## 六、不需要命令汇总(⬜ 59 条)

| 章 | 命令数 | 原因 |
|---|---|---|
| §3 Serial Interface | 6 | USB bulk 无 DCD/DTR/RTS/CTS/波特率/RI |
| §7 Call Related | 20 | 不做语音通话 |
| §8 Phonebook | 4(+`AT+CNUM` ✅) | 不做电话本(本机号除外) |
| §11 Supplementary | 7(+`AT+CUSD` 🔧) | 呼叫转移/等待/CLIP 等(话费查询除外) |
| §12 Audio | 15 | 不做音频 |
| §4 QCFG 3G 子项 | 3 | HSDPA/HSUPA/RRC 类别(LTE 项目) |
| §4 QCFG RI 子项 | 5 | USB 无物理 RI |
| 其他零散 | 2 | `ATX`/`A/`/`AT+QSIMDET`/`AT+QSIMSTAT`/`AT+QADC`/`AT+CGCLASS` 等 |

---

## 七、标准符合度大类评分(A-E,对照 docs/07)

> 本节是**宏观**视角(A-E 五大类标准符合度),§三–§六 是**微观**视角(逐命令)。
> 两者在同一文档里互补:本节回答"符不符合标准大类",§三回答"哪条命令做了"。
> 反映实际代码状态(SG 壳 + smscodec 芯 + B/C/D 类补齐),2026-07-12。

### 评分总表

| 大类 | 纯 SG 原评 | 当前实现 | 说明 |
|---|---|---|---|
| **A. AT 语法基础** | ✅ | ✅ | 壳不变(ERROR 精确 / 回显双保险 / CMEE=1) |
| **B. SMS 命令族** | ⚠️ 6/9 | ✅ 9/9 | 新增 +CSCA / +CMGR / +CSCS / +CMGD 全删,已对齐 |
| **C. URC 监听** | ⚠️ 框架 | ✅ 框架+编排 | 新增 SetSMSCallback:+CMTI→CMGR→decode→重组→CMGD 自动管道 |
| **D. PDU 编解码** | ⚠️ 三缺陷 | ✅ | smscodec 升级 + Reassembler 接入(handleIncomingSMS) |
| **E. 初始化** | ✅ | ✅ | 壳不变 |

### B 类逐命令覆盖

| 命令 | 状态 | 实现 |
|---|---|---|
| +CMGF | ✅ | `Initialize` |
| +CSCA | ✅ 新增 | `SMSC(ctx)` |
| +CMGS | ✅ | `Send`(多段循环) |
| +CMGL | ✅ | `ListStored` |
| +CMGR | ✅ 新增 | `ReadStored(ctx, index)` |
| +CMGD | ✅ | `DeleteStored` + `DeleteAllStored`(=1,4 全删) |
| +CNMI | ✅ | `Initialize`(2,1,0,0,0) |
| +CNMA | N/A | CNMI=2,1 存储模式,CNMA 非必需(两边都 ❌,标准允许) |
| +CPMS | ✅ | `Initialize` 预设("SM") |
| +CSCS | ✅ 新增 | `SetCharset(ctx, charset)` |

### C 类 +CMTI 自动收信管道

`SetSMSCallback(cb SMSCallback)` 启用后:
1. 注册 +CMTI URC handler(`OnURC`)
2. +CMTI 到达 → handler 解析 index → **goroutine** 异步执行(避免 readerLoop 死锁)
3. `ReadStored(index)` → `DecodeDeliver` → `Reassembler.Add`(长短信重组)
4. 重组完成 → `cb(sender, fullContent, timestamp)`
5. `DeleteStored(index)` 清理 SIM 空间

> 设计要点:handler 在 readerLoop 内同步 dispatch,但编排逻辑跑在独立 goroutine,
> 否则 `ReadStored` 的 `SendAndWait` 会死锁(等 readerLoop 读响应,而 readerLoop 卡在 handler 里)。

### 验证结果

- **离线**:`sms_test.go`(parseCMTI / SMSC / ReadStored / DeleteAll / SetCharset)+ `pdu_test.go`(编解码 facade)全绿,-race 通过
- **硬件**:5 个硬件测试同进程全 PASS,含新增 `TestHardwareSMSCAndReadStored`
  - SMSC 实测:`+8613010000000`
  - ReadStored 实测:`ReadStored[0] from=+8613800000001 body="test SMS content"`
  - +CMTI 自动收信:代码就位,需真实收件触发验证(向本机号发短信)

### 仍有的差距(诚实)

- **+CMTI 实时收信**:代码就位(SetSMSCallback),但硬件验证只测了主动读(ListStored/ReadStored),
  没测"向本机号发短信 → +CMTI 自动触发 → callback 收到"这条实时链路(需要从外部发短信到测试卡)。
- **CPMS 切换+恢复**:当前 Initialize 只预设 "SM",未实现 vohive 那种"按 +CMTI 指示的 storage 临时切换 + 读完恢复"。
  单存储场景够用;多存储切换留后续。
- **Reassembler TTL 清理**:`smscodec.Reassembler` 有 `Cleanup(ttl)` 方法防内存泄漏,当前未周期调用
  (长短信收不全的分片会留在 cache 里)。生产化时需加定时 Cleanup。

---

## 八、相关文档

- `docs/08-ec25-at-commands-index.md` — EC25 V1.2 手册逐命令索引(主参考)
- `docs/09-ec20-at-commands-index.md` — EC20 V1.1 手册索引(EC25 子集,交叉参考)
- `docs/07-at-implementation-comparison.md` — AT 实现对比与选型(§一–§六 历史对比快照;§七 已合并到本文档)
- `docs/Quectel_EC25EC21_AT_Commands_Manual_V1.2.pdf` — 手册原件
- `third_party/sms-gateway/modem/AGENTS.md` — modem 包 API 清单
