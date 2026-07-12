# Quectel EC20 AT Commands Manual V1.1 — 全册索引

> 本文档对官方手册 `EC20.pdf`(`EC20_AT_Commands_Manual_V1.1`,2015-07-14,191 页)做逐命令索引,
> 标注每条命令对 `dji-modem-research` 三阶段路线图的相关性、标准来源,并与 EC25 V1.2 手册(见 `docs/08`)做差异对照。
>
> 创建于 2026-07-12。手册原件见同目录 `EC20.pdf`。

---

## 〇、重要结论(先读)

**EC20 V1.1(2015)是 EC25 V1.2(2017)的前代子集手册。** 对本项目而言:

1. **本项目模组是 EG25-G/EC25 类**(DJI 百望刷 EC25 PID),**应以 `docs/08`(EC25 V1.2)为主参考**。EC20 手册仅作历史/交叉参考。
2. **EC20 V1.1 命令数 ≈ 119 条**(EC25 V1.2 ≈ 133 条)。EC20 是 EC25 的**严格子集**,仅多 1 个独有命令 `AT+QCFG="airplanecontrol"`。
3. **EC20 同样没有 `AT+CESQ` / `AT+QPING`**(已全文检索确认)。这两条命令在 EC25 V1.2 也缺失——它们是 2017 年后固件新增,需更新版手册或实测。**下载 EC20 手册不能填补该缺口。**
4. 两手册**共享命令的语法完全一致**(已抽样核对 CLCK/CPIN/QCFG 等)。下文共享命令的语法直接沿用,不重复论证。

---

## 一、图例与约定

(标记含义与 `docs/08` 第一节完全相同,此处不赘述。)

- ✅SMS = 阶段 1 已实现　🔵拨号 = 阶段 2 待实现　🟢上网 = 阶段 3　🔧参考 = 排障/配置　⬜N/A = 不需要
- V.25ter / TS 27.007 / TS 27.005 / Quectel 同 `docs/08`

---

## 二、与 EC25 V1.2 差异总览(核心价值)

### 2.1 EC20 独有(EC25 V1.2 未收录)

| 命令 | 章节 | 作用 |
|---|---|---|
| `AT+QCFG="airplanecontrol"` | §4.3.12 | 飞行模式检测(W_DISABLE# 引脚控制) |

### 2.2 EC25 新增(EC20 V1.1 没有)

| 章 | EC25 新增命令 | 数量 |
|---|---|---|
| §3 | `AT+IFC`、`AT+QRIR` | 2 |
| §4 | `AT+QINDCFG`;QCFG 子项 `pdp/duplicatechk`、`urc/ri/ring`、`urc/ri/smsincoming`、`urc/ri/other`、`risignaltype`、`urc/delay`、`urc/cache`、`tone/incoming` | 1 主 + 8 子 |
| §5 | `AT+QPINC`、`AT+QINISTAT`、`AT+QSIMDET`、`AT+QSIMSTAT` | 4 |
| §6 | `AT+QLTS`、`AT+QNWINFO`、`AT+QSPN` | 3 |
| §7 | `+++`(escape)、`AT+QHUP` | 2 |
| §9 | `AT+QCMGS`、`AT+QQCMGR` | 2 |
| §10 | `AT+QGDCNT`、`AT+QAUGDCNT` | 2 |
| §12 | `AT+VTD`、`AT+QEEC`、`AT+QMIC`、`AT+QRXGAIN`、`AT+QIIC`、`AT+QTONEDET`、`AT+QLDTMF`、`AT+QLTONE` | 8 |

合计 EC25 比 EC20 多 **~32 条命令**。这符合 EC25 是 EC20 后续迭代产品的定位。

> 注:EC20 §5 标题为 "SIM Related",EC25 改为 "(U)SIM Related"(USIM 概念强化),但命令语义不变。

---

## 三、逐章命令索引

> 共享命令语法与 EC25 一致,此处列出 EC20 的章节号/页码与项目相关性;语法仅在与 EC25 有差异或独有时展开。

### §1 Introduction(p8-10)

与 EC25 §1 基本一致:命令语法三类、字符集(GSM/IRA/UCS2)、AT 接口、URC、关机流程。**差异**:EC20 手册历史记录显示 V1.1 才加入 `AT+IPR`/`AT+QCCID`(V1.0 没有)。

### §2 General Commands(p11-29)— 25 条,与 EC25 §2 完全相同

| § | 命令 | 描述 | 标准 | 相关性 |
|---|---|---|---|---|
| 2.1 | `ATI` | 产品标识 | V.25ter | 🔧(✅modem 用) |
| 2.2 | `AT+GMI` | 厂商标识 | V.25ter | 🔧 |
| 2.3 | `AT+GMM` | 型号标识 | V.25ter | 🔧 |
| 2.4 | `AT+GMR` | 软件版本 | V.25ter | 🔧 |
| 2.5 | `AT+CGMI` | 厂商标识 | TS 27.007 | 🔧(✅modem 用) |
| 2.6 | `AT+CGMM` | 型号标识 | TS 27.007 | 🔧(✅modem 用) |
| 2.7 | `AT+CGMR` | 软件版本 | TS 27.007 | 🔧 |
| 2.8 | `AT+GSN` | IMEI | V.25ter | ✅SMS |
| 2.9 | `AT+CGSN` | 产品序列号(IMEI) | TS 27.007 | ✅SMS(✅modem 用) |
| 2.10 | `AT&F` | 恢复出厂默认 | V.25ter | 🔧 |
| 2.11 | `AT&V` | 显示当前配置 | V.25ter | 🔧 |
| 2.12 | `AT&W` | 存参数到用户 profile | V.25ter | 🔧 |
| 2.13 | `ATZ` | 恢复用户 profile | V.25ter | 🔧 |
| 2.14 | `ATQ` | 结果码呈现模式 | V.25ter | 🔧 |
| 2.15 | `ATV` | 响应格式 | V.25ter | 🔧 |
| 2.16 | `ATE` | 回显开关 | V.25ter | ✅SMS(✅modem 设 ATE0) |
| 2.17 | `A/` | 重复上一命令 | V.25ter | ⬜ |
| 2.18 | `ATS3` | 命令行终止符 | V.25ter | 🔧 |
| 2.19 | `ATS4` | 响应格式符 | V.25ter | 🔧 |
| 2.20 | `ATS5` | 命令行编辑符 | V.25ter | 🔧 |
| 2.21 | `ATX` | CONNECT 结果码/呼叫监测 | V.25ter | ⬜ |
| 2.22 | `AT+CFUN` | 功能级 | TS 27.007 | 🔧 |
| 2.23 | `AT+CMEE` | 错误消息格式 | TS 27.007 | ✅SMS(✅modem 设 =1) |
| 2.24 | `AT+CSCS` | TE 字符集 | TS 27.007 | ✅SMS |
| 2.25 | `AT+QURCCFG` | URC 输出口配置 | Quectel | 🔧 |

### §3 Serial Interface Control(p30-33)— 4 条(EC25 有 6 条)

> ⬜ 整章 USB 场景不适用。**EC20 比 EC25 少 `AT+IFC`(流控)和 `AT+QRIR`(RI 复位)**。

| § | 命令 | 描述 | 标准 | EC25 是否有 |
|---|---|---|---|---|
| 3.1 | `AT&C` | DCD 功能模式 | V.25ter | ✅ |
| 3.2 | `AT&D` | DTR 功能模式 | V.25ter | ✅ |
| 3.3 | `AT+ICF` | 字符帧格式/校验 | V.25ter | ✅ |
| 3.4 | `AT+IPR` | UART 固定波特率 | V.25ter | ✅ |
| — | ~~`AT+IFC`~~ | (EC20 无) | — | EC25 §3.3 |
| — | ~~`AT+QRIR`~~ | (EC20 无) | — | EC25 §3.6 |

### §4 Status Control(p34-47)— 3 主命令 + 12 QCFG 子项

| § | 命令 | 描述 | 标准 | 相关性 |
|---|---|---|---|---|
| 4.1 | `AT+CPAS` | ME 活动状态 | TS 27.007 | 🔧 |
| 4.2 | `AT+CEER` | 扩展错误报告 | TS 27.007 | 🔧排障 |
| 4.3 | `AT+QCFG` | 扩展配置(见下方 12 子项) | Quectel | 🔵🔧 |
| — | ~~`AT+QINDCFG`~~ | (EC20 无,EC25 §4.4) | — | — |

#### §4.3 `AT+QCFG` 子配置(EC20 12 项 / EC25 19 项)

| 子配置 | 作用 | EC25 是否有 | 相关性 |
|---|---|---|---|
| `"gprsattach"` | GPRS 附着模式 | ✅ | 🔵拨号 |
| `"nwscanmode"` | 网络搜索模式 | ✅ | 🔵 |
| `"nwscanseq"` | 网络搜索顺序 | ✅ | 🔵 |
| `"roamservice"` | 漫游服务 | ✅ | 🔵 |
| `"servicedomain"` | 服务域(CS/PS/CS&PS) | ✅ | 🔵 |
| `"band"` | 频段配置 | ✅ | 🔵 |
| `"hsdpacat"` | HSDPA 类别 | ✅ | ⬜ |
| `"hsupacat"` | HSUPA 类别 | ✅ | ⬜ |
| `"rrc"` | RRC 发布版本 | ✅ | ⬜ |
| `"sgsn"` | SGSN 发布版本 | ✅ | ⬜ |
| `"msc"` | MSC 发布版本 | ✅ | ⬜ |
| **`"airplanecontrol"`** | **飞行模式检测(EC20 独有)** | ❌ EC25 V1.2 无 | 🔧 |
| ~~`"pdp/duplicatechk"`~~ | (EC20 无) | EC25 有 | — |
| ~~`"urc/ri/ring"` 等 7 项~~ | (EC20 无 URC/RI/tone 系列) | EC25 有 | — |

---

### §5 SIM Related(p48-56)— 7 条(EC25 有 11 条)

> EC20 标题为 "SIM Related"(EC25 改 "(U)SIM Related")。**EC20 比 EC25 少 4 条**:`AT+QPINC`、`AT+QINISTAT`、`AT+QSIMDET`、`AT+QSIMSTAT`。

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 | EC25 有 |
|---|---|---|---|---|---|---|
| 5.1 | `AT+CIMI` | IMSI | `AT+CIMI` | TS 27.007 | ✅SMS | ✅ |
| 5.2 | `AT+CLCK` | 设施锁 | `AT+CLCK=<fac>,<mode>[,<passwd>]` | TS 27.007 | 🔧 | ✅ |
| 5.3 | `AT+CPIN` | 输入/查询 PIN | `AT+CPIN?` / `AT+CPIN=<pin>` | TS 27.007 | ✅SMS(✅modem) | ✅ |
| 5.4 | `AT+CPWD` | 改密码 | `AT+CPWD=<fac>,<old>,<new>` | TS 27.007 | 🔧 | ✅ |
| 5.5 | `AT+CSIM` | 通用 SIM 透传(APDU) | `AT+CSIM=<length>,<command>` | TS 27.007 | 🟢eSIM | ✅ |
| 5.6 | `AT+CRSM` | 受限 SIM 访问 | `AT+CRSM=<cmd>[,<fileid>,...]` | TS 27.007 | 🟢eSIM | ✅ |
| 5.7 | `AT+QCCID` | 显示 ICCID | `AT+QCCID` | Quectel | ✅SMS(✅modem) | ✅ |
| — | ~~`AT+QPINC`~~ | (EC20 无) | — | — | — | EC25 §5.8 |
| — | ~~`AT+QINISTAT`~~ | (EC20 无) | — | — | — | EC25 §5.9 |
| — | ~~`AT+QSIMDET`~~ | (EC20 无) | — | — | — | EC25 §5.10 |
| — | ~~`AT+QSIMSTAT`~~ | (EC20 无) | — | — | — | EC25 §5.11 |

### §6 Network Service(p57-66)— 7 条(EC25 有 10 条)

> **EC20 比 EC25 少 3 条**:`AT+QLTS`、`AT+QNWINFO`、`AT+QSPN`。

| § | 命令 | 描述 | 标准 | 相关性 | EC25 有 |
|---|---|---|---|---|---|
| 6.1 | `AT+COPS` | 运营商选择 | TS 27.007 | 🔵拨号(✅modem) | ✅ |
| 6.2 | `AT+CREG` | 网络注册状态 | TS 27.007 | 🔵 | ✅ |
| 6.3 | `AT+CSQ` | 信号质量(rssi,ber) | TS 27.007 | ✅SMS(✅modem) | ✅ |
| 6.4 | `AT+CPOL` | 优选运营商列表 | TS 27.007 | ⬜ | ✅ |
| 6.5 | `AT+COPN` | 读运营商名称 | TS 27.007 | ⬜ | ✅ |
| 6.6 | `AT+CTZU` | 自动时区更新 | TS 27.007 | 🔧 | ✅ |
| 6.7 | `AT+CTZR` | 时区上报 | TS 27.007 | 🔧 | ✅ |
| — | ~~`AT+QLTS`~~ | (EC20 无) | — | — | EC25 §6.8 |
| — | ~~`AT+QNWINFO`~~ | (EC20 无) | — | — | EC25 §6.9 |
| — | ~~`AT+QSPN`~~ | (EC20 无) | — | — | EC25 §6.10 |

> **CSQ rssi 映射与 EC25 完全相同**:0=-113dBm … 31=≥-51dBm,99=未知。

### §7 Call Related(p67-85)— 18 条(EC25 有 20 条) — ⬜ 整章不需要

> **EC20 比 EC25 少 `+++`(escape 序列)和 `AT+QHUP`**(指定释放原因挂断)。

命令清单:`ATA`、`ATD`、`ATH`、`AT+CVHU`、`AT+CHUP`、`ATO`、`ATS0`、`ATS6`、`ATS7`、`ATS8`、`ATS10`、`AT+CBST`、`AT+CSTA`、`AT+CLCC`、`AT+CR`、`AT+CRC`、`AT+CRLP`、`AT+QECCNUM`。

### §8 Phonebook(p86-91)— 5 条,与 EC25 完全相同 — ⬜ 不需要

`AT+CNUM`、`AT+CPBF`、`AT+CPBR`、`AT+CPBS`、`AT+CPBW`。

### §9 Short Message Service(p92-116)— 16 条(EC25 有 18 条)— ✅ 阶段 1 核心

> **EC20 比 EC25 少 `AT+QCMGS`/`AT+QCMGR`**(拼接短信文本模式命令)。核心 SMS 命令(CMGF/CPMS/CMGL/CMGS/CNMI/CMGD 等)两手册完全一致。

| § | 命令 | 描述 | 代表语法 | 标准 | modem 现状 | EC25 有 |
|---|---|---|---|---|---|---|
| 9.1 | `AT+CSMS` | 选消息服务 | `AT+CSMS=<service>` | TS 27.005 | — | ✅ |
| 9.2 | `AT+CMGF` | 消息格式(0=PDU) | `AT+CMGF[=<mode>]` | TS 27.005 | ✅`=0` | ✅ |
| 9.3 | `AT+CSCA` | 短信中心地址 | `AT+CSCA=<sca>[,<tosca>]` | TS 27.005 | — | ✅ |
| 9.4 | `AT+CPMS` | 优选消息存储 | `AT+CPMS=<mem1>[,<mem2>[,<mem3>]]` | TS 27.005 | ✅`"SM"` | ✅ |
| 9.5 | `AT+CMGD` | 删除消息 | `AT+CMGD=<index>[,<delflag>]` | TS 27.005 | ✅`DeleteStored` | ✅ |
| 9.6 | `AT+CMGL` | 列消息 | `AT+CMGL[=<stat>]` | TS 27.005 | ✅`=4` | ✅ |
| 9.7 | `AT+CMGR` | 读消息 | `AT+CMGR=<index>` | TS 27.005 | — | ✅ |
| 9.8 | `AT+CMGS` | **发消息** | PDU:`AT+CMGS=<length><CR>` PDU `Ctrl+Z` | TS 27.005 | ✅`Send` | ✅ |
| 9.9 | `AT+CMMS` | 多消息连续发送 | `AT+CMMS=<n>` | TS 27.005 | — | ✅ |
| 9.10 | `AT+CMGW` | 写消息到存储 | `AT+CMGW=<length>[,<stat>]<CR>` | TS 27.005 | — | ✅ |
| 9.11 | `AT+CMSS` | 从存储发送 | `AT+CMSS=<index>[,<da>]` | TS 27.005 | — | ✅ |
| 9.12 | `AT+CNMA` | 新消息确认 | `AT+CNMA=<n>` | TS 27.005 | — | ✅ |
| 9.13 | `AT+CNMI` | SMS 事件上报 | `AT+CNMI=<mode>,<mt>,<bm>,<ds>,<bfr>` | TS 27.005 | ✅`2,1,0,0,0` | ✅ |
| 9.14 | `AT+CSCB` | 小区广播类型 | `AT+CSCB=<mode>[,<mids>]` | TS 27.005 | ⬜ | ✅ |
| 9.15 | `AT+CSDH` | 文本模式头参数 | `AT+CSDH[=<show>]` | TS 27.005 | — | ✅ |
| 9.16 | `AT+CSMP` | 文本模式参数 | `AT+CSMP=<fo>[,<vp>[,<pid>[,<dcs>]]]` | TS 27.005 | — | ✅ |
| — | ~~`AT+QCMGS`~~ | (EC20 无,拼接短信) | — | — | — | EC25 §9.17 |
| — | ~~`AT+QCMGR`~~ | (EC20 无) | — | — | — | EC25 §9.18 |

> **CMGS 两步握手 `>` 提示符坑**:EC20/EC25 行为一致,`usbtransport` 的 `readLine` 适配对两平台都有效。

### §10 Packet Domain(p117-143)— 14 条(EC25 有 16 条)— 🔵 阶段 2 核心

> **EC20 比 EC25 少 `AT+QGDCNT`/`AT+QAUGDCNT`**(数据计数器)。核心 PDP 命令一致。

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 | EC25 有 |
|---|---|---|---|---|---|---|
| 10.1 | `AT+CGATT` | PS 附着/分离 | `AT+CGATT=<state>` | TS 27.007 | 🔵 | ✅ |
| 10.2 | `AT+CGDCONT` | **定义 PDP context** | `AT+CGDCONT=<cid>,<type>,<APN>[,...]` | TS 27.007 | 🔵核心 | ✅ |
| 10.3 | `AT+CGQREQ` | QoS(请求) | `AT+CGQREQ=<cid>[,...]` | TS 27.007 | 🔵 | ✅ |
| 10.4 | `AT+CGQMIN` | QoS(最低) | `AT+CGQMIN=<cid>[,...]` | TS 27.007 | 🔵 | ✅ |
| 10.5 | `AT+CGEQREQ` | 3G QoS(请求) | `AT+CGEQREQ=[<cid>,...]` | TS 27.007 | 🔵 | ✅ |
| 10.6 | `AT+CGEQMIN` | 3G QoS(最低) | `AT+CGEQMIN=[<cid>,...]` | TS 27.007 | 🔵 | ✅ |
| 10.7 | `AT+CGACT` | **激活/去激活 PDP** | `AT+CGACT=<state>,<cid>` | TS 27.007 | 🔵核心(✅modem ping) | ✅ |
| 10.8 | `AT+CGDATA` | 进入数据态(PPP) | `AT+CGDATA=<L2P>[,<cid>]` | TS 27.007 | 🟢(项目走 QMI) | ✅ |
| 10.9 | `AT+CGPADDR` | PDP 地址 | `AT+CGPADDR[=<cid>]` | TS 27.007 | 🔵🟢 | ✅ |
| 10.10 | `AT+CGCLASS` | GPRS 类别 | `AT+CGCLASS=<class>` | TS 27.007 | ⬜ | ✅ |
| 10.11 | `AT+CGREG` | GPRS 注册状态 | `AT+CGREG[=<n>]` | TS 27.007 | 🔵 | ✅ |
| 10.12 | `AT+CGEREP` | 分组域事件(`+CGEV`) | `AT+CGEREP=<mode>[,<bfr>]` | TS 27.007 | 🔵 | ✅ |
| 10.13 | `AT+CGSMS` | MO SMS 服务选择 | `AT+CGSMS=[<service>]` | TS 27.007 | 🔧 | ✅ |
| 10.14 | `AT+CEREG` | **EPS(LTE)注册状态** | `AT+CEREG[=<n>]` | TS 27.007 | 🔵核心 | ✅ |
| — | ~~`AT+QGDCNT`~~ | (EC20 无,数据计数) | — | — | — | EC25 §10.15 |
| — | ~~`AT+QAUGDCNT`~~ | (EC20 无) | — | — | — | EC25 §10.16 |

### §11 Supplementary Service(p144-156)— 8 条,与 EC25 完全相同 — ⬜ 不需要

`AT+CCFC`、`AT+CCWA`、`AT+CHLD`、`AT+CLIP`、`AT+CLIR`、`AT+COLP`、`AT+CSSN`、`AT+CUSD`。

### §12 Audio(p157-163)— 7 条(EC25 有 15 条) — ⬜ 不需要

> **EC20 比 EC25 少 8 条音频命令**:`AT+VTD`、`AT+QEEC`、`AT+QMIC`、`AT+QRXGAIN`、`AT+QIIC`、`AT+QTONEDET`、`AT+QLDTMF`、`AT+QLTONE`。

EC20 命令:`AT+CLVL`、`AT+CMUT`、`AT+VTS`、`AT+QAUDMOD`、`AT+QDAI`、`AT+QSIDET`、`AT+QAUDLOOP`。

### §13 Hardware Related(p164-167)— 5 条,与 EC25 完全相同

| § | 命令 | 描述 | 代表语法 | 标准 | 相关性 |
|---|---|---|---|---|---|
| 13.1 | `AT+QPOWD` | 关机 | `AT+QPOWD[=<n>]` | Quectel | 🔧 |
| 13.2 | `AT+CCLK` | 实时时钟 | `AT+CCLK=<time>` | TS 27.007 | 🔧 |
| 13.3 | `AT+CBC` | 电池电量/状态 | `AT+CBC` | TS 27.007 | 🔧 |
| 13.4 | `AT+QADC` | 读 ADC | `AT+QADC=<port>` | Quectel | ⬜ |
| 13.5 | `AT+QSCLK` | 慢时钟/睡眠 | `AT+QSCLK=<n>` | Quectel | 🔧 |

### §14 Appendix(p168-191)— 与 EC25 结构相同(14.1-14.9)

引用标准、出厂默认(AT&F)、可存(AT&W/ATZ)、+CME ERROR 码、+CMS ERROR 码、URC 汇总、SMS 字符集转换、CEER 释放原因。**内容与 EC25 §14 基本一致**,排障时查 `docs/08` 第四节即可(两手册错误码/URC 表近乎相同)。

---

## 四、EC20 独有命令详解

### `AT+QCFG="airplanecontrol"` — 飞行模式检测(§4.3.12, p46)

**作用**:启用/禁用飞行模式检测。启用后,模组开机时检测 **W_DISABLE# 引脚**:低电平 → 进入飞行模式(RF 关闭);高电平 → 正常模式。`W_DISABLE#` 引脚状态可能影响 `+CFUN` 状态。

**语法**:

```
AT+QCFG="airplanecontrol"[,<airplanecontrol>]
```

**响应**(参数省略时查询当前状态):

```
+QCFG: "airplanecontrol",<airplanecontrol>,<airplanestatus>
```

**参数**:

| 参数 | 取值 | 含义 |
|---|---|---|
| `<airplanecontrol>` | 0 | Disable(禁用飞行模式检测) |
| | 1 | Enable(启用) |
| `<airplanestatus>` | 0 | In normal mode(正常模式) |
| | 1 | In airplane mode(飞行模式,RF 关闭) |

**示例**:

```
AT+QCFG="airplanecontrol",1          // 启用检测
OK
<W_DISABLE# 引脚拉低>
AT+QCFG="airplanecontrol"
+QCFG: "airplanecontrol",1,1         // 已启用 + 已进入飞行模式
OK
<W_DISABLE# 引脚拉高>
AT+QCFG="airplanecontrol"
+QCFG: "airplanecontrol",1,0         // 已启用 + 退出飞行模式
OK
```

**本项目相关性**:⬜/🔧。DJI 百望模组走 USB,无 W_DISABLE# 引脚控制权(那是 EVB 硬件设计层面的飞行模式开关)。本项目若需"飞行模式"语义,用 `AT+CFUN=4`(禁 RF 收发)更直接,不依赖此命令。**记录备查**,EC25 V1.2 手册虽未列,但 EC25 实测可能也支持(同代固件)。

---

## 五、对本项目的最终结论

1. **主参考用 `docs/08`(EC25 V1.2)**。EC20 V1.1 是子集,不作为主参考。两手册冲突时以 EC25 V1.2 为准(更新、更全)。
2. **EC20 手册不能填补 `AT+CESQ`/`AT+QPING` 缺口**(全文检索确认两手册都没有)。这两命令需:
   - 查 Quectel 更新版 EC25/EG25-G AT 手册(2018+ 版本),或
   - 实测模组确认行为(项目已实测 `AT+CESQ` 在本机上可用,见 `docs/08` §5.1)。
3. **EC20 独有的 `airplanecontrol`** 对 USB 用户态方案无实际价值(无引脚控制权),`AT+CFUN=4` 替代之。
4. **共享命令语义完全一致**,本索引与 `docs/08` 可交叉验证。如对某命令语义存疑,查两手册原文对照。

---

## 六、命令数统计核对

| 章 | EC20 V1.1 | EC25 V1.2 | 差值 |
|---|---|---|---|
| §2 | 25 | 25 | 0 |
| §3 | 4 | 6 | EC25 +2 |
| §4(主) | 3 | 4 | EC25 +1(QINDCFG) |
| §4 QCFG 子项 | 12 | 19 | EC25 +7 |
| §5 | 7 | 11 | EC25 +4 |
| §6 | 7 | 10 | EC25 +3 |
| §7 | 18 | 20 | EC25 +2 |
| §8 | 5 | 5 | 0 |
| §9 | 16 | 18 | EC25 +2 |
| §10 | 14 | 16 | EC25 +2 |
| §11 | 8 | 8 | 0 |
| §12 | 7 | 15 | EC25 +8 |
| §13 | 5 | 5 | 0 |
| **主命令合计** | **119** | **133** | **EC25 +14** |

> EC20 独有 1 条(airplanecontrol)未计入差值(它在 EC20 §4 的 12 个 QCFG 子项里,EC25 的 19 子项里没有对应)。故净看:EC25 比 EC20 多 14 主命令 + 7 QCFG 子项 = 21 条,EC20 比 EC25 多 1 条(airplanecontrol)。
