# vohive/modem 实现剖析

> 对 `source/vohive-collection/vohive/internal/modem/` + `pkg/smscodec/` 的深度实现分析,评估其作为"USB transport 上层 AT 协议层"的复用价值。
> 创建于 2026-07-11。与 `docs/05-sms-gateway-modem-analysis.md`、`docs/07-at-implementation-comparison.md` 配套使用。
> 代码行号基于本仓库 `source/vohive-collection/vohive/` 快照。

---

## 一、模块定位与依赖

`vohive` 是 VoHive 全栈网关的主仓(source-available)。本文关注两个包:
- **`internal/modem/`** —— AT modem 管理器(Manager),vohive-collection 里**功能最完整**的 AT 实现
- **`pkg/smscodec/`** —— 独立的 SMS PDU 编解码包,委托 warthog618/sms 库

### 文件清单

**`internal/modem/`**(生产代码,不含 _test):

| 文件 | 行数 | 作用 |
|---|---|---|
| `manager.go` | 2283 | `Manager` 结构体、三 goroutine 架构、`handleCommand`/`readLoop`/`runLoop`、SMS 收发编排、URC 分发、超时看门狗 |
| `commands.go` | 706 | 命令封装层:`QueryCSQ`/`SendSMS`/`QuerySMSC`/`SMSReadPDU`/`SMSDeleteAll` 等 |
| `at_parse.go` | 694 | AT 响应解析:`parseCSQ`/`parseCPMS`/`extractAllSMSPDUsAfterPrefix` 等 |
| `urc_format.go` | 238 | URC 分类与格式化 |
| `urc_listener.go` | 185 | 辅助 AT 端口的独立 URC 监听器 |
| `sim_metadata.go` | 329 | SPN/运营商元数据 |
| `serial_at.go` | 113 | 简陋 AT(独立小工具,非主路径) |
| 其余(operator_name/spn/imei_probe 等) | ~400 | 辅助 |

**`pkg/smscodec/`**(PDU 编解码,生产代码):

| 文件 | 行数 | 作用 |
|---|---|---|
| `pdu.go` | ~350 | PDU 编解码入口,委托 warthog618/sms |
| `reassembler.go` | 82 | 长短信分片重组 |
| `pdu_trim.go` | 149 | 国产模组 PDU 容错(长度裁剪/spare bit 清零) |
| `binary_classifier.go` | ~300 | 8-bit 二进制短信分类(OMA CP/WAP/MMS) |
| `wbxml_omacp.go` | ~600 | WBXML OMA CP 解码 |

### 依赖

| 依赖 | 类型 | 复制影响 |
|---|---|---|
| `config.DeviceConfig` | vohive 内部包 | ❌ **拖入 config 依赖链** |
| `apduarbiter` / `simaid` / `logger` | vohive 内部包 | ❌ 多个内部包耦合 |
| `github.com/warthog618/sms` | 外部库(MIT) | smscodec 用,Go 生态最完整 TS 23.040 实现 |
| `go.bug.st/serial` | 外部库 | transport 硬绑 |

---

## 二、核心类型与并发模型

### Manager 结构体(`manager.go:54-124+`)

Manager 结构体非常大(70+ 字段),核心字段:

```go
type Manager struct {
    cfg      config.DeviceConfig   // ← vohive 内部 config 类型
    atPort   string
    port     serial.Port           // ← 硬绑定 go.bug.st/serial(无 io 注入口)
    portMode *serial.Mode

    // channel-based 异步架构
    stop        chan struct{}
    cmdChan     chan commandRequest // 普通优先级
    cmdChanHigh chan commandRequest // 高优先级(短信, IP 切换)
    rxChan      chan rxMsg          // readLoop → runLoop
    triggerChan chan struct{}       // 短信触发信号
    ready       chan struct{}

    // 超时看门狗
    atTimeoutMu     sync.Mutex
    atTimeoutStreak int             // 连续超时计数

    // 设备信息缓存(imei/iccid/imsi/operator/signal...)
    // URC 回调(smsCallback/ringCallback/clipCallback/...)
    // 长短信重组器
    reassembler *smscodec.Reassembler
    ...
}
```

### commandRequest(`manager.go:38-50`)

```go
type commandRequest struct {
    cmd          string
    respChan     chan string
    errChan      chan error
    timeout      time.Duration
    silent       bool        // 静默(降低日志)
    highPriority bool        // 走高优先级队列

    // 交互式模式(CMGS 两步握手)
    interactive bool        // 是否为交互式命令
    waitPrompt  bool        // 是否等待 "> " 提示符
    followUp    string      // 收到提示符后发送的后续指令(PDU + Ctrl-Z)
}
```

`interactive`/`waitPrompt`/`followUp` 三字段让 CMGS 两步握手在一个 commandRequest 内完成,比 sms_gateway 的"两次 SendAndWait"更内聚。

### 并发模型:三 goroutine + 双队列

```
   调用方                     ┌─ cmdChanHigh (高优先级:短信/IP)
   ExecuteAT ─→ commandRequest ─┤
   ExecuteATHigh ──────────────┘─ cmdChan (普通优先级)
                                        │
                                        ▼
                                  ┌─ runLoop ──────────────────┐
                                  │  select {                  │
                                  │    req := <-cmdChanHigh    │
                                  │    req := <-cmdChan        │
                                  │    msg := <-rxChan → URC   │
                                  │  }                         │
                                  │  handleCommand(req)        │
                                  └────────────────────────────┘
                                        ▲ rxChan
                                        │
                                  ┌─ readLoop ─────────────────┐
                                  │  port.Read → 按行切分       │
                                  │  rxChan <- rxMsg{Data:line} │
                                  └────────────────────────────┘
                                        ▲
                                  ┌─ port (serial) ────────────┐
                                  │  go.bug.st/serial.Open     │
                                  └────────────────────────────┘
```

对比 sms_gateway 的"单 readerLoop + pending channel",vohive 多了一层 `rxChan` 和 `runLoop` 调度层,支持双优先级队列。架构更复杂,但能保证短信发送(高优先级)不被普通查询阻塞。

另有独立的 `URCListener`(`urc_listener.go:52-101`),用于辅助 AT 端口(主端口忙时,辅助端口收 URC)。

---

## 三、AT 命令层

### ExecuteAT 家族(`manager.go:1698-1710`)

```go
func (m *Manager) ExecuteAT(cmd string, timeout time.Duration) (string, error) {
    return m.executeAT(cmd, timeout, false, false)
}
func (m *Manager) ExecuteATSilent(cmd string, timeout time.Duration) (string, error) { ... }
func (m *Manager) ExecuteATHigh(cmd string, timeout time.Duration) (string, error) {
    return m.executeAT(cmd, timeout, false, true)   // 高优先级
}
```

调用方传什么就发什么——`"AT+CMGS=23"`、`"AT+CSQ"`、`"AT+CFUN=1,1"` 全部直发。`vohive/internal/backend/at_backend.go` 就是直接拼 `AT+CMGD=%d`、`AT+CFUN=%d` 字符串扔给 ExecuteAT。

返回值是 `string`(多行用 `\n` join),不是 `[]string`。与 sms_gateway 的 `[]string` 相比,调用方解析时需要再 split。

### 与 sms_gateway SendAndWait 的对比

| | sms_gateway `SendAndWait` | vohive `ExecuteAT` |
|---|---|---|
| 返回类型 | `[]string`(每行一个元素) | `string`(多行 `\n` join) |
| 优先级 | 单队列 | 双队列(普通/高) |
| 并发控制 | readerLoop 串行处理 pending | runLoop 调度,支持高优先级插队 |
| ctx 支持 | ✅ `context.Context` 参数 | ❌ 只有 timeout,无 ctx |

vohive 的 ExecuteAT **不接受 context**——只有 timeout,无法从外部取消。sms_gateway 的 SendAndWait 支持 ctx.Done()。

---

## 四、响应解析(handleCommand)

`handleCommand`(`manager.go:645-747`)处理单个命令的响应循环。

### OK 精确,ERROR 子串匹配(符合性缺陷)

```go
// manager.go:682-692
if line == "OK" {                                    // ← 精确匹配,对
    ...
    req.respChan <- strings.Join(fullResponse, "\n")
} else if strings.Contains(line, "ERROR") {          // ← 子串匹配!缺陷
    ...
    req.errChan <- fmt.Errorf("设备返回错误: %s", ...)
}
```

**OK 是精确匹配,但 ERROR 用 `strings.Contains`**。这会把任何含 "ERROR" 子串的响应行误判为错误。举例:某模组返回的 +CME ERROR 详情行、或碰巧包含这个词的 USSD 响应,都可能触发误判。对比 sms_gateway 的 `isTerminator`(`modem.go:618-630`)用精确 `line == "ERROR"` + `HasPrefix("+CMS ERROR")` + `HasPrefix("+CME ERROR")`,本包这里有真实的符合性缺陷。

### 回显处理:仅靠 ATE0,无兜底

`initModem`(`manager.go:877`)发 `ATE0` 关回显,但 **handleCommand 里没有任何 echo 剔除逻辑**——不像 sms_gateway 有 `line == current.cmd` 兜底(`modem.go:579`)。若 ATE0 未生效(部分 Quectel 固件 echo 行为特殊,或 USB CDC-AT 场景),响应解析会被回显行污染。

### 超时恢复:ESC + 看门狗(`manager.go:663-671`)

```go
case <-timeoutTimer.C:
    m.port.Write([]byte{0x1B})                       // 发 ESC 取消挂起操作
    req.errChan <- errors.New("命令执行超时")
    if failures, tripped := m.recordATTimeout(req); tripped {
        m.tripATTimeoutWatchdog(req.cmd, failures)   // 连续超时触发恢复
    }
```

超时时发 `0x1B`(ESC)取消可能的挂起操作(如短信 body 输入模式),并记录超时。`recordATTimeout`(:444)累计连续超时次数,达到阈值(连续 5 次)触发 `tripATTimeoutWatchdog`(:454)做模组级恢复(重置/重连)。这是 sms_gateway 没有的生产级容错机制。

### `>` 提示符:内聚的两步交互(`manager.go:703-723`)

```go
} else if strings.Contains(line, ">") {              // 注意:也是子串匹配
    if req.interactive && req.waitPrompt && req.followUp != "" {
        m.port.Write([]byte(req.followUp))           // 立即发 PDU+Ctrl-Z
        req.waitPrompt = false
        continue                                     // 继续等最终 OK/ERROR
    }
    req.respChan <- "> "
    break RespLoop
}
```

CMGS 两步握手在一个 commandRequest 内完成:`interactive=true` + `waitPrompt=true` + `followUp="PDUhex\x1A"`。收到 `>` 后自动发 followUp,继续等 OK。比 sms_gateway 的"两次 SendAndWait + 手动 SendRaw"更内聚。

注意 `>` 也是 `strings.Contains` 子串匹配(同 ERROR 的问题),但实际场景里 `>` 出现在响应行概率低,风险较小。

---

## 五、URC 系统

### handleURC —— 内置丰富 URC(`manager.go:1308+`)

vohive 内置了远比 sms_gateway 丰富的 URC 处理:

| URC | 处理 |
|---|---|
| `+CMTI:` | 新短信 → 自动 `AT+CMGR` 读 PDU → decode → 重组 → `AT+CMGD` 删除 |
| `+CPIN:`/`+QSIMSTAT` | SIM 状态变化 → simStatusHandler 回调 |
| `+CUSD:` | USSD → ussdChan 投递 |
| `RING` | 来电 → ringCallback |
| `+CLIP:` | 来电号码 → clipCallback |
| `NO CARRIER` | 对方挂断 → hangupCallback |
| `CONNECT`/`OK` | 对方接听 → connectCallback |
| `+QPCMV` | PCM 流控 → qpcmvChan |
| `+QIND`/`+QIURC` | Quectel 网络/网络指示 |
| `RDY`/`SMS Ready`/`Call Ready` | 模组重启就绪广播 → dispatchRDY |

URC 格式化在 `urc_format.go` 里分类(Warn/Info/Debug 级别 + 字段提取)。

### isURC —— 启发式黑名单(`manager.go:1256-1274`)

```go
func (m *Manager) isURC(line string) bool {
    s := strings.TrimSpace(line)
    // 排除同步命令的回显,防止被 URC 处理拦截
    if strings.HasPrefix(s, "+CSIM:") || strings.HasPrefix(s, "+CGLA:") ||
       strings.HasPrefix(s, "+CCHO:") || strings.HasPrefix(s, "+CMGR:") ||
       strings.HasPrefix(s, "+CMGS:") || strings.HasPrefix(s, "+QENG:") {
        return false
    }
    if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "^") || strings.HasPrefix(s, "$") {
        return true
    }
    switch s {
    case "RING", "RDY", "SMS Ready", ...: return true
    }
    return false
}
```

判断逻辑:**以 `+`/`^`/`$` 开头的行都算 URC**,但用一个**硬编码黑名单**排除同步命令回显(+CSIM/+CGLA/+CCHO/+CMGR/+CMGS/+QENG)。这是个补丁——因为 URC 和命令响应共用 rxChan,需要区分。`handleCommand` 里还有 `isPureAsyncURC`(`manager.go:730-737`)进一步区分"纯异步 URC"(从响应里剔除)vs "既是 URC 也是命令回显"(保留进 fullResponse)。

对比 sms_gateway 的模型:**sms_gateway 用 "in-flight call 是否存在" 自然区分**(有 call → 响应;无 call → URC),不需要预知 URC 前缀,也不需要黑名单。vohive 的方式功能更全(内置 +CMTI 自动读短信等),但架构上不如 sms_gateway 干净。

### URCListener —— 辅助端口监听(`urc_listener.go`)

独立的辅助 AT 端口 URC 监听器,主端口忙时用它收 URC。可 `RegisterHandler("+CMTI", ...)`。比主 URC 系统简单(只解析不编排),对本项目价值不大(单端口场景)。

---

## 六、SMS 操作

### SendSMSWithOptions —— PDU 发送(`manager.go:1978-2037`)

```go
func (m *Manager) SendSMSWithOptions(phone, message string, opts smscodec.SubmitOptions) error {
    m.ExecuteATHigh("AT+CMGF=0", 3*time.Second)                    // 确保 PDU 模式
    pduHexList, tpduLenList, _ := m.buildSMSPDUsWithOptions(...)   // smscodec 编码(自动分段)

    for i, pduHex := range pduHexList {
        req := commandRequest{
            cmd:          fmt.Sprintf("AT+CMGS=%d", tpduLenList[i]),
            timeout:      20 * time.Second,
            highPriority: true,
            interactive:  true, waitPrompt: true,
            followUp:     pduHex + "\x1A",                         // PDU + Ctrl-Z
        }
        m.cmdChanHigh <- req                                        // 高优先级原子执行
        // 等待 respChan/errChan
        if i < len(pduHexList)-1 { time.Sleep(500 * time.Millisecond) }  // 防模组队列溢出
    }
}
```

特点:
- 用 smscodec 自动分段(`pduHexList` 是切片),长短信自动拆多段
- 每段走高优先级通道,`interactive`+`waitPrompt`+`followUp` 三字段完成两步握手
- 段间 500ms 延迟防模组队列溢出

对比 sms_gateway 的 `Send`:`Send` 不自动分段(超长报错),单段发送。vohive 在长短信发送上更完整。

### readAndProcessSMSFromStorage —— 接收编排

`+CMTI` URC 触发后(`manager.go:1383-1400` 附近):
1. `switchSMSStorageForRead` 切到 URC 指定的存储区
2. `AT+CMGR=<idx>` 读 PDU
3. smscodec 解码
4. `reassembler.Add` 长短信重组
5. 重组完成(或单条)→ smsCallback 投递
6. `AT+CMGD=<idx>` 删除
7. 恢复原存储区(闭包)

### switchSMSStorageForRead —— CPMS 切换+恢复(`manager.go:1609+`)

```go
// 读完短信后恢复原 CPMS 设置(闭包)
func (m *Manager) switchSMSStorageForRead(urcStorage string) (restore func(), err error)
```

这是符合 TS 27.005 §3.3.1 多存储区语义的正确做法——URC 指示的存储区可能与当前 CPMS 不同,临时切换、读完恢复。sms_gateway 不做这个(初始化时预设 `AT+CPMS="SM"` 后不动)。

---

## 七、PDU 编解码(smscodec 包)

`smscodec` 是独立包(`pkg/smscodec/`),委托 `github.com/warthog618/sms`(Go 生态最完整的 TS 23.040 开源实现)。

### smscodec/pdu.go 入口

```go
// pdu.go:10-12 import
smspdu "github.com/warthog618/sms"
"github.com/warthog618/sms/encoding/tpdu"
"github.com/warthog618/sms/encoding/ucs2"
```

核心函数委托 warthog618:
- `BuildSubmitTPDUsWithOptions`(`pdu.go:418` 附近):用 `smspdu.Encode` 自动处理 MTI/VPF/PID/DCS + 自动分段
- TPDU 解码用 `smspdu.Unmarshal`(`pdu.go:357` 附近)
- 地址编解码 `DecodeAddressValue`/`EncodeAddress`(`pdu.go:216-288`):用 `ton&0x70==0x10` 判国际(不是魔法数 0x91)

### 长短信重组(Reassembler,`reassembler.go`)

```go
func (r *Reassembler) Add(sender string, concat ConcatInfo, content string) (complete bool, fullContent string)
```

- 按 `sender_ref` 作 key 缓存分片(`reassembler.go:36`)
- 同 Seq 去重(`:39`)
- 分片数 == Total 时按 Seq 排序拼接(`:56-62`)
- TTL 清理防内存泄漏(`Cleanup`,`:65-81`)

sms_gateway 的 pdu.go 只返回 `ConcatInfo` 给调用方,**没有重组器**——调用方自己拼。vohive 这层完整。

### 国产模组容错(pdu_trim.go)

这是 sms_gateway 完全没有的能力,针对国产模组的真实 bug:

1. **GSM-7 spare bit 清零**(`normalizeDeliverTPDUGSM7SpareBits`,`pdu_trim.go:86-107`):标准要求 GSM-7 尾部 spare bit 应为 0,但很多国产模组填 1 导致解码乱码。本函数检测并清零。

2. **PDU 长度裁剪**(`TrimFullPDUHexByATHeader`/`TrimDeliverTPDUToDeclaredLength`,`pdu_trim.go:27-72`):按 +CMGL/+CMGR header 声明的 TPDU 长度裁掉尾随垃圾字节(国产模组常见多塞字节问题)。

3. **二进制短信分类**(`binary_classifier.go`):对 8-bit 二进制短信(OMA CP / WAP SI/SL / MMS / SIM OTA 23.048)分类。

4. **WBXML OMA CP 解码**(`wbxml_omacp.go`):解码 OMA Client Provisioning 消息。

### smscodec 的标准符合度

| 维度 | 状态 |
|---|---|
| GSM-7 基本表 + **扩展表**(TS 23.038 §6.2.1) | ✅ 完整(warthog618 实现) |
| UCS-2(UTF-16BE) | ✅ 完整 |
| 地址 BCD(国际/国内/字母数字/短号码) | ✅ 完整 |
| SCTS 时间戳 | ✅ 完整(委托库) |
| UDH 8-bit + 16-bit ref | ✅ 完整 |
| 长短信自动分段(发送) | ✅ 完整 |
| 长短信重组(接收) | ✅ 完整(Reassembler) |
| 国产模组容错 | ✅ smscodec 独有 |

**smscodec 是三个候选里标准符合度最高的 PDU 层。** 代价是依赖外部库 warthog618/sms。

---

## 八、Transport 耦合与依赖

### serial.Open 硬绑定

```go
// manager.go:57
port     serial.Port           // go.bug.st/serial 接口
// manager.go:485(Open 内)
m.port, err = serial.Open(m.atPort, m.portMode)   // 直接打开串口设备路径
```

`Manager` 构造函数 `New(cfg config.DeviceConfig)`(`manager.go:164`)只接受 config,内部 `serial.Open`。**没有任何接受 `io.ReadWriteCloser` 的构造函数**。整个 vohive-collection 里没有任何文件用 `io.ReadWriteCloser` 注入到 modem.Manager。

### 内部依赖链

移植 Manager 需要同时拖入:
- `config.DeviceConfig` —— vohive 的设备配置类型(本身可能还有依赖)
- `apduarbiter` —— APDU 仲裁器(多通道 APDU 访问)
- `simaid` —— SIM AKA 鉴权辅助
- `logger` —— vohive 的日志包(基于 slog)
- `smscodec` —— 这个反而是最干净的(只依赖 warthog618)

### Quectel 私有 AT 集

Manager 强耦合 Quectel EC20 私有指令:
- `AT+QSIMSTAT?`(SIM 状态,优先于标准 `AT+CPIN?`)
- `AT+QENG="servingcell"`(LTE 信号)
- `AT+QPCMV=1,2`(UAC 语音模式,初始化序列里就有 `manager.go:881`)
- `AT+QCFG`(USBNET/USBCFG)
- `AT+QNWINFO`/`AT+QIMS?`/`AT+QCCID`

这些命令在标准 Quectel 文档里有,但不是 3GPP 标准。对 DJI 百望(EC25 模式)兼容性需验证。

### 剥离难度评估

| 部分 | 剥离难度 | 说明 |
|---|---|---|
| smscodec 包 | ⭐ 容易 | 独立包,只依赖 warthog618,不拖 vohive 内部包 |
| Manager 主体 | ⭐⭐⭐⭐ 困难 | 拖 config/apduarbiter/simaid/logger + Quectel 私有 AT + serial.Open 改造 |

---

## 九、复用评估

### Manager 整体移植:不推荐

- ❌ 拖 5 个 vohive 内部包(config/apduarbiter/simaid/logger + smscodec 依赖)
- ❌ 强耦合 Quectel 私有 AT 集(需逐一验证 DJI 百望兼容性)
- ❌ serial.Open 硬绑定,无 io 注入口,改造量大
- ❌ `strings.Contains(line,"ERROR")` 符合性缺陷
- ❌ 缺 CMEE 设置(永远拿不到结构化 CME/CMS 错误码)
- ❌ 缺标准 CPIN 就绪检查
- ✅ 但功能最全:长短信重组、超时看门狗、丰富 URC、CPMS 切换+恢复

移植代价远高于 sms_gateway(后者 ~25 行单文件改造)。

### smscodec 单独引入:可行

`smscodec` 包(`pkg/smscodec/`)是独立包,**只依赖 `warthog618/sms`(MIT 许可)**,不拖 vohive 内部包。可以作为 PDU 层单独引入,替换 sms_gateway 的 pdu.go。

这是"组合方案"(SG 壳 + smscodec 芯)的基础——见 `docs/07` 第五节。

### 结论

vohive/modem 的价值在**参考**而非直接复制:
- **Manager**:功能最全,但耦合代价太高。其长短信重组、超时看门狗、CPMS 切换+恢复等编排逻辑,等阶段 1 跑通后遇到短板时可借鉴
- **smscodec**:标准符合度最高的 PDU 层,可单独引入作为后续升级路径

对比详见 `docs/07-at-implementation-comparison.md`。
