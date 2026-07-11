# AT 协议层实现对比与选型

> 综合全 vohive-collection + sms_gateway 生态里所有 AT 实现,以电信标准(见 `docs/04`)为标尺逐维度对比,产出选型建议。
> 创建于 2026-07-11。基于 `docs/05`(sms_gateway 剖析)、`docs/06`(vohive 剖析)、`docs/04`(标准规范)。
>
> 本文核心是**标准符合度**(谁更对),不是功能多寡(谁更多)。一个实现可能功能少但每个都做对了,另一个可能功能多但实现不够规范。

---

## 一、候选全景(决策矩阵)

全 vohive-collection + sms_gateway 里跟"AT"沾边的实现共 7 处。按两个关键维度筛选——**能发裸 AT**(如 AT+CMGS,而非只能 AT+CSIM)和 **transport 可注入**(能接受外部 io.ReadWriteCloser):

| 实现 | 路径 | 能发裸 AT? | transport 可注入? | 短信能力 |
|---|---|---|---|---|
| **sms_gateway Modem** | `source/sms_gateway/agent/internal/modem/` | ✅ 任意命令 | ⚠️ 需 ~25 行改造 | ✅ 完整 |
| **vohive Manager** | `vohive/internal/modem/` | ✅ 任意命令 | ❌ 硬绑 serial+config | ✅ 完整+重组 |
| vohive SerialAT | `vohive/internal/modem/serial_at.go` | ⚠️ 简陋 | ❌ serial | ❌ |
| vohive URCListener | `vohive/internal/modem/urc_listener.go` | ❌ 只发初始化 | ❌ serial | ❌ |
| **uicc-go at.Reader** | `vohive-collection/uicc-go/at/` | ❌ 只 CSIM | ✅ `io.ReadWriteCloser` | ❌ |
| euicc-go driver/at | `vohive-collection/euicc-go/driver/at/` | ❌ 走 uicc-go | ❌ 包装 uicc-go | ❌ |
| swu-go DirectSIM | `vohive-collection/swu-go/pkg/sim/direct.go` | ⚠️ 仅 CIMI/CGSN | ❌ os.File | ❌ |

### 关键结论:不存在开箱即得的组合

**整个生态里没有"能发裸 AT + transport 可注入"的现成实现。** 最接近的两个:
- sms_gateway Modem —— 能发裸 AT,但 transport 需 ~25 行改造(把 serial.Port 换成 io 接口)
- uicc-go at.Reader —— transport 可注入,但只能 CSIM(要发裸 AT 得导出 run())

因此真正的选型在 **sms_gateway Modem** vs **vohive Manager** 之间(两者都能发裸 AT),uicc-go 因能力不足出局。下文 A-E 节逐维度对比这两个。

---

## 二、标准符合度逐维度对比表

### 图例
- ✅ 完整(符合标准且实现正确)
- ⚠️ 部分(实现但有缺陷,或覆盖不全)
- ❌ 缺失

### A. AT 语法基础(V.250 / TS 27.007)

| 维度 | 标准 | sms_gateway | vohive | 证据 |
|---|---|---|---|---|
| `\r\n` 命令终止 | V.250 §5.2.1 | ✅ `modem.go:120` | ✅ `manager.go:649` | 两边都对 |
| OK result code | V.250 §5.2.2 | ✅ 精确 `line=="OK"` `modem.go:620` | ✅ 精确 `manager.go:682` | 两边都对 |
| ERROR result code | V.250 §5.2.2 | ✅ 精确 `line=="ERROR"` `modem.go:621` | ❌ **`Contains(line,"ERROR")`** `manager.go:692` | **VH 有误判风险**(含 ERROR 子串的行被误判) |
| +CMS/+CME ERROR | TS 27.005 §3.2.5 / 27.007 §9.2 | ✅ `HasPrefix` `modem.go:624-627` | ⚠️ 同上 Contains 覆盖,但不区分 | SG 精确,VH 粗放 |
| 回显处理 | V.250 §6.2.1 En | ✅ **双保险**:ATE0 + `line==cmd` 兜底 | ⚠️ 只 ATE0,无兜底 `manager.go:877` | **SG 更对**(USB CDC-AT 场景尤其重要) |
| AT+CMEE 设置 | TS 27.007 §8.5 | ✅ `AT+CMEE=1` `sms.go:30` | ❌ **从不发 CMEE** | **VH 缺陷**→ 默认 CMEE=0,拿不到结构化错误码 |
| 错误码结构化 | TS 27.007 §9.2 | ⚠️ 原文返回(含数字码,因 CMEE=1) `modem.go:587` | ❌ 原文返回(且无数字码,因无 CMEE) `manager.go:701` | 两者都未做结构化,但 SG 有获取前提 |

**A 类小结:SG 完整,VH 部分。** 三个关键差异(ERROR 精确匹配、回显双保险、CMEE 设置)都是 SG 更对。

### B. SMS 命令族(TS 27.005)

| 命令 | 章节 | sms_gateway | vohive |
|---|---|---|---|
| +CMGF | §3.2.1 | ✅ `sms.go:35` | ✅ `manager.go:878` |
| +CSCA | §3.1.1 | ❌ 未实现 | ✅ `commands.go:205` `QuerySMSC` |
| +CMGS | §3.5.1 | ✅ `sms.go:456`(两步握手) | ✅ `manager.go:1999`(interactive followUp) |
| +CMGL | §3.4.2 | ✅ `sms.go:417` | ✅ `commands.go:189` + PDU 长度裁剪(更鲁棒) |
| +CMGR | §3.4.3 | ❌ 无单条读 | ✅ `commands.go:180` `SMSReadPDU` |
| +CMGD | §3.5.4 | ✅ 单条 `sms.go:443` | ✅ 单条+全删(`AT+CMGD=1,4`) |
| +CNMI | §3.4.1 | ✅ `sms.go:38` | ✅ `manager.go:879` |
| +CNMA | §3.4.3 | ❌ | ❌(两者都用 CNMI=2,1 存储模式,CNMA 非必需) |
| +CPMS | §3.3.1 | ✅ 预设 `sms.go:42` | ✅ **切换+恢复** `manager.go:1609`(更贴合标准) |
| +CSCS | §3.2 | ❌ | ✅ `manager.go:2153`(USSD 前设) |

**B 类小结:VH 完整(8/9),SG 部分(6/9)。** VH 多覆盖 CSCA/CMGR/CSCS,且 CPMS 存储区切换+恢复更贴合标准语义。

### C. URC 监听(架构层)

| 维度 | sms_gateway | vohive |
|---|---|---|
| 后台 reader goroutine | ✅ `modem.go:80` 单 readerLoop | ✅ 三 goroutine(readLoop→rxChan→runLoop) |
| URC/响应区分机制 | ✅ **干净**:in-flight call 有无 `modem.go:609` | ⚠️ **启发式黑名单**:`isURC` 排除 +CSIM/+CMGR 等 `manager.go:1262` |
| 内置 URC 种类 | ⚠️ 框架(白盒 dispatch,调用方 parse) | ✅ **丰富**:+CMTI/+CPIN/+CUSD/RING/+CLIP/+QSIMSTAT/+QPCMV... |
| +CMTI 自动读短信 | ❌ 只 dispatch,调用方自己 CMGR | ✅ 自动 CMGR→decode→重组→CMGD |
| 专用辅助端口 | ❌ | ✅ URCListener |

**C 类小结:VH 功能更全(URC 种类多一个数量级+自动编排),SG 架构更干净(无黑名单)。** 从标准覆盖面看 VH 更完整,但从代码整洁度看 SG 更优。

### D. PDU 编解码(TS 23.040 + TS 23.038)

| 维度 | 标准 | sms_gateway(pdu.go 手写) | vohive(smscodec + warthog618) |
|---|---|---|---|
| 地址 BCD | TS 23.040 §9.1.2.5 | ⚠️ 只 0x91/0x81,不处理 0xD0 字母数字 | ✅ 完整(国际/国内/字母数字/短号码) |
| SMS-SUBMIT/DELIVER | TS 23.040 | ✅ 手写(first octet 硬编码 0x11) | ✅ 委托库(自动 MTI/VPF/PID/DCS) |
| SCTS 时间戳 | TS 23.040 §9.1.2.5 | ✅ `decodeSCTS` `pdu.go:337`(含时区) | ✅ 委托库 |
| GSM-7 基本表 | TS 23.038 §6.2.1 | ✅ 正确 | ✅ 正确 |
| **GSM-7 扩展表** | TS 23.038 §6.2.1 | ❌ **明确放弃**,`^{}[]~|\\€`→`?` `pdu.go:4-5` | ✅ **完整**(warthog618) |
| UCS-2 | TS 23.038 | ✅ `pdu.go:467`(UTF-16BE+代理对) | ✅ 委托库 |
| UDH 接收(0x00+0x08) | TS 23.040 §9.2.3.24 | ✅ `parseUDH` `pdu.go:540` 两种都识别 | ✅ 委托库 |
| UDH 发送(16-bit ref) | TS 23.040 §9.2.3.24 | ❌ 只 8-bit ref `pdu.go:161` | ✅ 完整 |
| **长短信自动分段** | TS 23.040 | ❌ 超长报错,靠调用方切 `pdu.go:191` | ✅ 自动分段 |
| **长短信重组** | TS 23.040 | ❌ 返回 ConcatInfo,调用方拼 | ✅ `Reassembler` `reassembler.go` 自动重组 |
| 国产模组容错 | — | ❌ 无 | ✅ spare bit 清零 + PDU 长度裁剪 + 二进制分类 |

**D 类小结:VH 全面碾压。** SG 的 pdu.go 有三个明确缺陷(无扩展表/16-bit ref/自动分段重组),VH 用 warthog618 + 自研容错层,符合度接近完整。

### E. 初始化规范(对照 `docs/04` §5.3 建议)

| 步骤 | 标准/建议 | sms_gateway | vohive |
|---|---|---|---|
| AT 探测 | — | ✅ `sms.go:26` | ✅ `manager.go:868` |
| ATE0 关回显 | V.250 §6.2.1 | ✅ `sms.go:29` | ✅ `manager.go:877` |
| **AT+CMEE=1** | TS 27.007 §8.5(建议) | ✅ `sms.go:30` | ❌ **从不发** |
| **AT+CPIN? 就绪检查** | TS 27.007 §8.1(建议) | ✅ `ensureSIMReady` `sms.go:48`+PIN 解锁 | ❌ 不在 init,优先 Quectel 私有 `AT+QSIMSTAT` |
| AT+CMGF=0 | TS 27.005 §3.2.1 | ✅ `sms.go:35` | ✅ `manager.go:878` |
| AT+CNMI | TS 27.005 §3.4.1 | ✅ `sms.go:38` | ✅ `manager.go:879` |
| AT+CPMS 预设 | TS 27.005 §3.3.1 | ✅ `sms.go:42` | ❌ 按需切换 |

**E 类小结:SG 完整(严格符合 docs/04 §5.3 建议),VH 部分(缺 CMEE/CPIN/CPMS)。**

### 符合度评分总表

| 大类 | sms_gateway | vohive | 谁更符合标准 |
|---|---|---|---|
| **A. AT 语法基础** | ✅ 完整 | ⚠️ 部分 | **SG** |
| **B. SMS 命令族** | ⚠️ 部分(6/9) | ✅ 完整(8/9) | **VH** |
| **C. URC 监听** | ⚠️ 框架(架构干净) | ✅ 完整(架构脏) | VH(覆盖面)/ SG(整洁度) |
| **D. PDU 编解码** | ⚠️ 部分(3 缺陷) | ✅ 完整 | **VH** |
| **E. 初始化** | ✅ 完整 | ⚠️ 部分 | **SG** |

**整体:不是一边倒。** SG 在 AT 语法细节 + 初始化规范(A/E)更对,VH 在 SMS 命令覆盖 + PDU 深度 + URC 种类(B/C/D)更全。

---

## 三、五个"谁更对"的关键差异

这些是**标准符合度**上的差异(非功能多寡),有代码行号佐证:

### 差异 1:+CMEE 与错误码(SG 对 / VH 错)

- **SG**:初始化发 `AT+CMEE=1`(`sms.go:30`),错误时模组返回 `+CME ERROR: <数字码>`,数字码出现在 error 字符串里
- **VH**:**从不发 CMEE**(grep 全包 0 命中),模组默认 CMEE=0 → 错误只返回裸 `ERROR`,**永远拿不到 3GPP TS 27.007 §9.2 定义的 CME/CMS 数字错误码**
- 标准:TS 27.007 §8.5 `+CMEE` 控制错误报告粒度,§9.2 定义 CME ERROR 数字码

### 差异 2:回显处理(SG 对 / VH 弱)

- **SG**:双保险——发 ATE0(`sms.go:29`)**且** readerLoop 里 `line == current.cmd` 兜底剔除(`modem.go:579`)
- **VH**:只发 ATE0(`manager.go:877`),handleCommand **无 echo 剔除逻辑**
- **对 USB transport 的影响**:DJI 百望 AT 口走 USB CDC-AT bulk endpoint,回显行为可能与传统串口不同。SG 的双保险更稳;VH 若 ATE0 未生效,响应解析会被回显行污染
- 标准:V.250 §6.2.1 `En` Echo Command

### 差异 3:GSM-7 扩展表(VH 对 / SG 错)

- **SG**:`pdu.go:4-5` 注释明确放弃,`gsm7Set`(`pdu.go:373`)含 `0x1b`(ESC)但不解释扩展表,`^{}[]~|\\€` 全变 `'?'`
- **VH**:委托 warthog618/sms 完整实现 TS 23.038 §6.2.1 基本表 + 扩展表
- **影响**:收到含扩展字符的短信,SG 会丢字
- 标准:TS 23.038 §6.2.1 GSM 7-bit default alphabet extension table

### 差异 4:长短信 UDH + 自动分段重组(VH 对 / SG 部分)

- **SG**:接收识别 0x00/0x08 两种 IEI(`pdu.go:540`),但发送只 8-bit ref(`pdu.go:161`)**且不自动分段**(超长报错 `pdu.go:191`),无重组器
- **VH**:接收+重组(`reassembler.go`)+ 发送自动分段(`smscodec/pdu.go` BuildSubmitTPDUs)都完整
- 标准:TS 23.040 §9.2.3.24 Concatenation

### 差异 5:初始化 SIM 就绪检查(SG 对 / VH 缺)

- **SG**:`Initialize` 调 `AT+CPIN?` 等 READY(`sms.go:48-69`),支持 PIN 解锁
- **VH**:不在 init 序列做标准 CPIN 检查,优先用 Quectel 私有 `AT+QSIMSTAT`(`commands.go:29`)
- **影响**:开机瞬间若 SIM 未就绪,VH 后续命令可能拿到 +CME ERROR 10(SIM not inserted)而无人处理;SG 会阻塞等就绪
- 标准:TS 27.007 §8.1 `+CPIN`

---

## 四、复用性对比表

| 维度 | sms_gateway | vohive(Manager) | vohive(smscodec 单独) |
|---|---|---|---|
| 复制行数 | ~1700(3 文件) | ~3000 + 5 内部包 | ~1500(smscodec 包) |
| transport 改造 | ~25 行(单文件 NewFromIO) | 大改(serial.Open+config 剥离) | 不适用(PDU 层,无 transport) |
| 外部依赖 | zerolog(可删) | config/apduarbiter/simaid/logger + Quectel 私有AT | warthog618/sms(MIT) |
| AT 语法正确性(A/E) | ✅ | ⚠️ | — |
| PDU 完整度(D) | ⚠️ 3 缺陷 | ✅ | ✅ |
| 短信编排(B/C) | ✅ 够用 | ✅ 最全 | — |

---

## 五、选型建议

### 三条路线

| 路线 | 组成 | 优点 | 缺点 |
|---|---|---|---|
| **A. 纯 SG** | sms_gateway/modem 3 文件原样 | 最快打通、依赖最轻、AT 语法最对 | PDU 三缺陷(扩展表/长短信) |
| **B. SG 壳 + smscodec 芯** | SG modem.go+sms.go + vohive smscodec | 标准符合度最高(AT 对 + PDU 对) | 引入 warthog618 外部库 + 改 sms.go 调用点 |
| **C. 纯 vohive** | vohive modem.Manager | 功能最全 | 拖 5 内部包 + Quectel 私有AT + serial 难注入 + AT 语法有缺陷 |

### 推荐:A(纯 SG)起步,B 为升级路径

**阶段 1 推荐 A(纯 SG)**,理由:

1. **接入点要求 AT 语法层正确**(A/E)。USB transport 上层最关键的是:干净的 transport-agnostic 收发框架 + 鲁棒的回显处理 + 可获取的错误码。SG 在这三点上都更对,VH 的 `Contains("ERROR")` + 无 CMEE + 无回显兜底是真实的符合性缺陷。

2. **PDU 三缺陷阶段 1 可接受**:
   - GSM-7 扩展表缺失:收含 `{}[]~|` 的短信丢字——阶段 1 验证阶段可接受
   - 长短信不自动分段:发长短信先手动切——阶段 1 先发短短信够用
   - 这些缺陷**后续可升级**,不影响"先把 USB→AT→短信链路打通"这个阶段 1 目标

3. **改造量最小**:~25 行单文件改造(NewFromIO),对比 vohive 的 5 内部包剥离 + Quectel AT 验证。

**后续升级到 B(SG 壳 + smscodec 芯)**:

当阶段 1 跑通、遇到 PDU 短板(如需要收扩展字符短信、发长短信)时:
- 引入 `pkg/smscodec/`(只依赖 warthog618/sms,不拖 vohive 内部包)
- 替换 SG `sms.go` 里 `EncodeSubmit`/`DecodeDeliver` 调用点为 smscodec 的 `BuildSubmitTPDUs`/decode
- 额外引入 `Reassembler` 做长短信重组

这条路径成立的前提是 **smscodec 不依赖 vohive 内部包**——已核实:`pkg/smscodec/pdu.go:10-12` 只 import warthog618/sms 三个子包,其余生产文件全标准库。

### 不推荐 C(纯 vohive)

移植代价远超收益:
- 拖 config/apduarbiter/simaid/logger 4 个内部包(剥离时可能引入更多依赖)
- Quectel 私有 AT 集(QSIMSTAT/QENG/QPCMV)需逐一验证 DJI 百望兼容性
- serial.Open 无 io 注入口,改造量最大
- 且 A/E 类(AT 语法/初始化)有真实缺陷——功能最多不等于最对

---

## 六、与 usb-transport.md 计划的关系

选定 A(纯 SG)后,`plans/usb-transport.md` 的第二、三章需要相应调整(原计划基于 uicc-go/at):

| usb-transport.md 章节 | 原计划(uicc-go/at) | 改为(sms_gateway/modem) |
|---|---|---|
| 第二章 复制对象 | uicc-go/at 6 文件 | sms_gateway/modem 3 文件(modem.go+sms.go+pdu.go) |
| 第二章 NewReader 导出 | 加 `NewReader(io.ReadWriteCloser)` | 加 `NewFromIO(io.ReadWriteCloser)` |
| 第三章 Read 超时语义 | deadline 链,超时返回 (0,nil) | 短超时轮询(200ms),对齐 readerLoop 的 isTimeout |
| 第三章 deadline 接口 | 需要 readDeadliner/writeDeadliner | **不需要**(modem 包无 deadline 链) |
| 4.3 端到端验证 | 三条路纠结(CSIM/raw-AT/绕开) | 直接 `SendAndWait(ctx,"AT+CSQ",5s)` |

详见后续对 `plans/usb-transport.md` 的更新。
