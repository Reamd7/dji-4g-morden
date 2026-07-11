# uicc-go `at/` 包 — AT 命令电信标准规范索引

> 本文档调研 `uicc-go/at/` 包(计划复制进本项目)实现的协议与对应的电信标准规范。
> 创建于 2026-07-11。用于代码接入时的标准依据参考,以及后续 SMS 扩展的规范定位。

---

## 一、`at/` 包实现了什么

`uicc-go/at/` 共 6 个文件 1314 行。实际能力比包名暗示的窄:

### 实现的 AT 命令(仅 1 个 3GPP 命令)

**`AT+CSIM`**(在 `csim.go`)—— APDU 透传。格式 `AT+CSIM=<length>,<hexstring>`,`<length>` 是 hex 字符串字符数(字节数 × 2),符合 TS 27.007 §8.17。

### 实现的协议层功能

| 功能 | 位置 | 说明 |
|---|---|---|
| **APDU 透传** | `csim.go` + `at.go:Transmit` | 原始 APDU 字节包进 `AT+CSIM`,解析 `+CSIM:` 响应回 APDU 字节 |
| **APDU 状态字(SW1/SW2)识别** | `csim.go:106` `hasKnownStatusWord` | SW1 ∈ {0x61–0x6F, 0x90–0x9F} 视为已知 SW |
| **AT 行收发框架** | `at.go:run` | 通用命令行写入 + 响应行读取,可发任意 AT 命令 |

### 未实现(后续阶段需要的)

- **SMS PDU 模式** —— 无 +CMGS/CMGL/CMGF/CNMA/CSCA/CPMS
- **USSD** —— 无 +CUSD
- **URC 监听** —— `run()` 是发-收同步模型,无法收 `+CMTI:`/`+CMT:` 主动上报

### OK / ERROR / +CME ERROR / +CMS ERROR 处理(`at.go:run` 142-152)

```go
upper := strings.ToUpper(line)
switch {
case upper == "OK":
    return strings.TrimSpace(builder.String()), nil
case upper == "ERROR",
    strings.HasPrefix(upper, "+CME ERROR:"),
    strings.HasPrefix(upper, "+CMS ERROR:"):
    return "", errors.New(line)
```

- `OK` → 成功,返回累积响应行(`\n` 拼接)
- `ERROR` / `+CME ERROR: <err>` / `+CMS ERROR: <err>` → 把整行原文作 error 返回(**未解析数字码**)
- `+CMS ERROR:` 已识别但当前无 SMS 命令触发,为未来预留

### 回显处理(`at.go:run` 136-139)

回显抑制靠**字符串相等比较**(`line == command`),不是真正的 ATE0/ATE1 切换。包**不主动发 `ATE0`**。
- 限制:若 modem 回显行与命令字符不完全一致(如多空格)会误判
- Linux serial 层的 `Lflag &^= ECHO` 关闭的是 tty 子系统回显,不是 modem 回显——两回事

---

## 二、标准映射(每个功能 → 标准章节)

| 代码功能 | 标准编号 | 章节/条款 |
|---|---|---|
| AT 命令 `\r\n` 终止(`at.go:110`) | ITU-T V.250 | §5.2.1 / §6.3 命令行语法 |
| `OK` / `ERROR` result code(`at.go:143-146`) | ITU-T V.250 | §5.2.2 result codes 表(OK=0, ERROR=4) |
| 回显抑制逻辑(`at.go:137`) | ITU-T V.250 | §6.2.1 `En` Echo Command |
| **`AT+CSIM` 命令格式与响应** | **3GPP TS 27.007** | **§8.17 `+CSIM`**(包内核心) |
| `+CME ERROR: <err>`(`at.go:145`) | 3GPP TS 27.007 | §9.2 + §8.5 `+CMEE` 控制开关 |
| `+CMS ERROR: <err>`(`at.go:145`) | 3GPP TS 27.005 | §3.2.5 Message Service Failure |
| APDU 指令/响应透传(`csim.go`) | ETSI TS 102 221 | §10(UICC CLA/INS)+ §7 APDU 传输 |
| APDU CLA/INS/P1/P2/Lc/Le 结构 | ISO/IEC 7816-4 | §5 interindustry commands |
| SW1/SW2 状态字定义(`csim.go:106`) | ISO/IEC 7816-4 | SW1-SW2 编码表 |

> 注:APDU 的 SW1/SW2 语义定义在 **ISO/IEC 7816-4**(不是 7816-3);7816-3 是电气接口与 T=0/T=1 传输协议。`hasKnownStatusWord` 把 0x61–0x6F 和 0x90–0x9F 视为合法 SW1,覆盖了 7816-4 表的 normal(90xx)、warning(62xx/63xx)、GET RESPONSE(61xx)、error(64xx–6Fxx)。

### 历史命名对照

| 现行编号 | 旧名(GSM 时代) |
|---|---|
| 3GPP TS 27.005 | GSM 07.05 |
| 3GPP TS 27.007 | GSM 07.07 |

---

## 三、标准当前版本(WebSearch 验证,2026-07)

| 标准 | 当前最新版本 | 发布日期 |
|---|---|---|
| **3GPP TS 27.007** | V18.8.0 (Rel-18) | 2025-01;Rel-19 UCC |
| **3GPP TS 27.005** | V18.0.0 (Rel-18) | 2024-05;Rel-19 UCC |
| **ETSI TS 102 221** | V18.2.0 稳定 / V18.3.0 预告 | 2024-06 / 2025-10 |
| **ITU-T V.250** | V.250 (07/2003) | 2003-07-14,In force,**至今无新版本** |
| **ISO/IEC 7816-3** | 7816-3:2006/Amd1:2008 | 2006(电气与 T=0/T=1) |
| **ISO/IEC 7816-4** | 7816-4:2013(现行) | 2013(SW1/SW2 + interindustry) |

---

## 四、标准索引总表(含下载链接)

| 标准 | 名称 | 当前版本 | 覆盖功能 | 下载 |
|---|---|---|---|---|
| **ITU-T V.250** | Serial asynchronous automatic dialling(原 V.25ter) | 2003 | AT 语法、`\r\n`、OK/ERROR、`En` 回显、S 寄存器 | https://www.itu.int/rec/T-REC-V.250/en |
| **3GPP TS 27.007** | AT command set for UE(原 GSM 07.07) | V18.8.0 | **§8.17 AT+CSIM**(核心)、§8.5 +CMEE、§9.2 +CME ERROR、§8.18 +CRSM、§8.19a +CCHO/CCHC、§8.22 +CGLA | https://www.3gpp.org/dynareport/27007.htm |
| **3GPP TS 27.005** | SMS/CBS DTE-DCE 接口(原 GSM 07.05) | V18.0.0 | §3.2.5 +CMS ERROR(已识别未用);**SMS 全套未实现** | https://www.3gpp.org/dynareport/27005.htm |
| **ETSI TS 102 221** | UICC-Terminal interface | V18.2.0 | UICC 物理/逻辑特性,APDU 传输协议 | https://www.etsi.org/deliver/etsi_ts/102200_102299/102221/ |
| **ISO/IEC 7816-3** | ICC 电子信号与传输协议 | :2006 | T=0/T=1 协议 | ISO 官网付费 |
| **ISO/IEC 7816-4** | Interindustry commands | :2013 | APDU 结构、SW1-SW2 编码 | ISO 官网付费;免费镜像 [cardwerk](https://cardwerk.com/smart-card-standard-iso7816-4-section-5-basic-organizations/)、[EFTLab SW 表](https://www.eftlab.com/knowledge-base/complete-list-of-apdu-responses) |

---

## 五、后续 SMS 收发的标准缺口

`at/` 包当前只做了 APDU 透传 + 通用收发框架。要做短信收发需要补齐:

### 5.1 SMS 命令族(3GPP TS 27.005)

| 命令 | 章节 | 作用 | 状态 |
|---|---|---|---|
| +CMGF | §3.2.1 | 文本(1)/PDU(0) 模式 | 未实现 |
| +CSCA | §3.1.1 | SMSC 服务中心号码 | 未实现 |
| **+CMGS** | §3.5.1 | 发送 SMS(**`<Ctrl-Z>` 0x1A 终止**) | 未实现;`run()` 用 `\r\n`,**无法直接发** |
| +CMGL | §3.4.2 | 列举 SMS(需 TPDU 解析) | 未实现 |
| +CMGR | §3.4.3 | 读取单条 | 未实现 |
| +CMGD | §3.5.4 | 删除 | 未实现 |
| **+CNMI** | §3.4.1 | MT 指示(收 SMS 主动上报) | 未实现;**`run()` 无 URC 监听能力** |
| +CNMA | §3.4.3 | RP-ACK 新消息确认 | 未实现 |
| +CPMS | §3.3.1 | 选择存储区(SM/ME/MT) | 未实现 |
| +CSCS | §3.2 | 字符集(GSM7/UCS2/8bit) | 未实现 |

### 5.2 PDU 层(解析 CMGL/CMGR 响应所需)

| 标准 | 内容 |
|---|---|
| **3GPP TS 23.040** | SMS TPDU 结构:SMS-SUBMIT/DELIVER、地址编码、时间戳、UDH 长短信 |
| 3GPP TS 23.041 | CBS 广播 PDU |
| 3GPP TS 24.011 | RP 层(RP-MO-DATA / RP-MT-DATA) |

### 5.3 架构缺口(非命令)

1. **URC 监听器** —— 收短信必备。需要后台 reader goroutine 把 `+CMTI:`/`+CMT:`/`+CDS:` 推给回调
2. **`AT+CMGS` 的 `0x1A` 发送** —— 需专门处理 `>` 提示符后发 PDU,不能套 `run()` 的 `\r\n`
3. **错误码结构化** —— `+CMS ERROR`/`+CME ERROR` 数字码 → 结构化 error
4. **初始化建议** —— 显式发 `ATE0`(V.250 §6.2.1)+ `AT+CMEE=2`(TS 27.007 §8.5),不依赖脆弱的 `line == command` 相等比较

---

## 六、对项目阶段的影响

- **当前(复制 at/ 包 + USB transport)**:无影响。`AT+CSIM` APDU 透传 + 通用框架够用,标准明确(TS 27.007 §8.17)
- **后续 SMS 阶段**:需补 TS 27.005 SMS 命令族 + TS 23.040 TPDU 解析 + URC 监听器(架构改动较大,非简单命令补齐)
