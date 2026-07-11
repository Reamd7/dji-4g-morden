# sms_gateway/modem 包实现剖析

> 对 `source/sms_gateway/agent/internal/modem/` 的深度实现分析,评估其作为"USB transport 上层 AT 协议层"的复用价值。
> 创建于 2026-07-11。与 `docs/06-vohive-modem-analysis.md`、`docs/07-at-implementation-comparison.md` 配套使用。
> 代码行号基于本仓库 `source/sms_gateway/` 快照。

---

## 一、模块定位与依赖

`sms_gateway` 是一个 monorepo,包含两个独立 Go module(`agent/` + `backend/`)+ 固件 + 前端。本文关注的是 **`agent/internal/modem/`**,它是 Linux/Windows 主机端的 AT modem 驱动。

### 文件清单(3 文件,共 1711 行)

| 文件 | 行数 | 作用 |
|---|---|---|
| `modem.go` | 647 | `Modem` 结构体、并发模型、`readerLoop`、`SendAndWait`/`SendRaw`、ICMP ping(可删) |
| `sms.go` | 490 | SMS 高层操作:`Initialize`/`Send`/`ListStored`/`DeleteStored` + 信号/ICCID/IMEI/运营商查询 |
| `pdu.go` | 574 | SMS PDU 编解码(GSM-7/UCS-2/UDH/address/SCTS),手写,自包含 |

### 外部依赖(极轻)

| 依赖 | 用途 | 复制时是否需要 |
|---|---|---|
| `go.bug.st/serial v1.6.2` | 唯一 transport(跨平台串口) | ❌ USB 方案不用,改造时移除 |
| `github.com/rs/zerolog` | 日志(`log.Debug().Str(...)`) | 可选,可全局替换成项目 logger |

**无** gousb / libusb / ModemManager / D-Bus / QMI 依赖。

---

## 二、核心类型与并发模型

### Modem 结构体(`modem.go:29-43`)

```go
type Modem struct {
    port serial.Port   // ← 硬绑定 go.bug.st/serial 接口(唯一 transport 耦合点)

    mu        sync.Mutex
    pending   chan *call // currently in-flight call
    closeOnce sync.Once
    closed    chan struct{}

    urcMu       sync.Mutex
    urcHandlers []URCHandler

    iccidMu       sync.Mutex
    iccidCache    string
    iccidCachedAt time.Time
}
```

`port` 字段是 `go.bug.st/serial.Port` 接口类型——这是整个包**唯一的 transport 耦合点**(详见第八节改造方案)。

### 辅助类型

```go
type URCHandler func(line string)          // modem.go:45

type call struct {                          // modem.go:47
    cmd     string
    prefix  string    // 可选:响应行前缀过滤(如 "+CMGL:")
    done    chan callResult
    timeout time.Duration
}

type callResult struct {                    // modem.go:54
    lines []string
    err   error
}
```

### 并发模型:单 reader goroutine + channel 投递

这是本包最核心的设计。**只有一个 goroutine `readerLoop` 读 port**,所有公开方法不直接读,而是把 `*call` 投递到 `pending` channel,等 readerLoop 把响应回填到 `call.done`。

```
   ┌──────────────────────────────────────────────────────┐
   │  调用方 goroutine                                      │
   │    SendAndWait(ctx, cmd, timeout)                     │
   │      │                                                │
   │      ├─ m.port.Write(cmd + "\r\n")    ← 直接写        │
   │      ├─ m.pending <- c                 ← 投递 call     │
   │      └─ select {                                      │
   │           <-c.done  → 返回 lines/err                  │
   │           <-time.After(timeout) → 超时,drain pending  │
   │           <-ctx.Done()                                │
   │           <-m.closed                                  │
   │         }                                             │
   └──────────────────────────────────────────────────────┘
                            ▲ pending channel
                            │
   ┌────────────────────────┴─────────────────────────────┐
   │  readerLoop goroutine(单例,Open 时启动)              │
   │    bufio.NewReader(m.port).ReadString('\n')           │
   │      ├─ 行 == cmd            → 丢弃(回显)            │
   │      ├─ isTerminator(line)   → 回填 current.done       │
   │      ├─ line == ">"          → 回填(交互提示符)       │
   │      ├─ current != nil       → collected append       │
   │      └─ current == nil       → dispatchURC(line)      │
   └──────────────────────────────────────────────────────┘
```

这个模型的好处:**读端无锁**(单 goroutine)、**写端 mutex 保护**(SendAndWait 里 `m.mu.Lock()`),URC 和命令响应用"in-flight call 是否存在"自然区分——没有 vohive 那种启发式黑名单(见 `docs/06` 第五节)。

---

## 三、AT 命令层

### Open —— 构造与启动(`modem.go:60-82`)

```go
func Open(port string, baud int) (*Modem, error) {
    mode := &serial.Mode{BaudRate: baud, DataBits: 8, ...}
    p, err := serial.Open(port, mode)         // ← 硬编码 serial.Open
    if err := p.SetReadTimeout(200 * time.Millisecond); err != nil { ... }  // ← 关键:200ms 轮询超时
    m := &Modem{port: p, pending: make(chan *call, 1), closed: make(chan struct{})}
    go m.readerLoop()                         // ← 启动唯一 reader
    return m, nil
}
```

`SetReadTimeout(200ms)` 是**整个超时模型的基础**:底层每次 Read 最多阻塞 200ms,超时返回(零字节或短读),readerLoop 把它当"nothing to do"继续循环。这决定了 USB transport 接入时 `Read` 必须模仿这个语义(见第五节)。

### SendAndWait —— 发任意裸 AT 命令(`modem.go:113-150`)

```go
func (m *Modem) SendAndWait(ctx context.Context, cmd string, timeout time.Duration) ([]string, error) {
    c := &call{cmd: cmd, done: make(chan callResult, 1), timeout: timeout}
    m.mu.Lock()
    m.port.Write([]byte(cmd + "\r\n"))        // ← 发命令(\r\n 终止,符合 V.250 §5.2.1)
    m.pending <- c                            // ← 投递给 readerLoop
    m.mu.Unlock()

    select {
    case res := <-c.done:        return res.lines, res.err
    case <-time.After(timeout):  // 超时:drain pending 防 URC 死锁下个 call
        select { case <-m.pending: default: }
        return nil, fmt.Errorf("AT timeout: %q", cmd)
    case <-ctx.Done():           return nil, ctx.Err()
    case <-m.closed:             return nil, io.ErrClosedPipe
    }
}
```

**这正是 uicc-go/at 缺的能力**——可以发任意 AT 命令(`AT+CMGS`/`AT+CSQ`/`AT+ICCID`/`AT+COPS`...),返回多行响应 `[]string`(已过滤回显/OK/ERROR)。

注意超时分支的 drain 逻辑(`modem.go:139-142`):超时后从 `pending` 取走 call,否则 readerLoop 后续会把响应回填到一个没人等的 call,死锁下一个命令。这种细节考虑得周到。

### SendRaw —— 裸字节写入(`modem.go:154-157`)

```go
func (m *Modem) SendRaw(p []byte) error {
    _, err := m.port.Write(p)
    return err
}
```

不加 `\r\n`,用于 `AT+CMGS` 的 body:写 PDU hex + Ctrl-Z(`0x1A`)。见第六节 Send 的实现。

---

## 四、响应解析(readerLoop)

`readerLoop`(`modem.go:525-616`)是整个包的心脏,逐行解析 port 读到的数据。

### 回显过滤:双保险

```go
// modem.go:579-581
if current != nil && line == current.cmd {
    continue   // 丢弃等于命令本身的行(回显)
}
```

**加上** `sms.go:29` 初始化时发的 `ATE0`,构成双保险:
- 第一道:`ATE0` 让模组不回显(V.250 §6.2.1 标准做法)
- 第二道:`line == current.cmd` 字符串相等比较兜底(防止 ATE0 未生效)

对比 vohive 只发 ATE0、readerLoop 无兜底(见 `docs/06` 第四节),本包在回显处理上更稳健——对 USB CDC-AT 这种回显行为不确定的场景尤其重要。

### 终止符识别:精确匹配(`modem.go:618-630`)

```go
func isTerminator(line string) bool {
    switch {
    case line == "OK":                                return true
    case line == "ERROR":                             return true
    case strings.HasPrefix(line, "+CMS ERROR"):       return true
    case strings.HasPrefix(line, "+CME ERROR"):       return true
    }
    return false
}
```

**精确匹配 + HasPrefix**,不是子串匹配。对比 vohive 的 `strings.Contains(line, "ERROR")`(`manager.go:692`)——后者会把任何含 "ERROR" 子串的响应行误判为错误(如某行内容碰巧包含这个词)。本包做法符合 V.250 §5.2.2 result code 定义。

### `>` 提示符:CMGS 两步交互(`modem.go:600-607`)

```go
if line == ">" {
    if current != nil {
        current.done <- callResult{lines: append(collected, ">")}
        current = nil
        collected = nil
    }
    continue
}
```

`AT+CMGS=<len>` 后模组返回 `>` 提示符要求输入 PDU body。readerLoop 把 `>` 当作单行结果返回、关闭当前 call,调用方收到后写 PDU+Ctrl-Z,再投递一个空 call 等最终 OK。这是为短信发送专门设计的两步握手(见第六节 Send)。

### URC 分发:空闲期 dispatch(`modem.go:609-614`)

```go
if current != nil {
    collected = append(collected, line)     // 有 in-flight call → 当响应行
} else {
    log.Debug().Str("urc", line).Msg("AT ⇢ URC")
    m.dispatchURC(line)                     // 无 call → 当 URC 分发
}
```

URC(主动上报)和命令响应的区分靠**"当前有没有 in-flight call"**:
- 有 call → 行属于该命令的响应
- 无 call → 行是 URC,分发给所有 `OnURC` 注册的 handler

这个模型干净:不需要预知哪些前缀是 URC(白盒),调用方自己 parse。vohive 则用启发式黑名单(`isURC` 排除 `+CSIM`/`+CMGR` 等)区分,逻辑更复杂(见 `docs/06` 第五节)。

### 超时容错(`modem.go:538-560`)

```go
line, err := readLine(r)
if err != nil {
    if errors.Is(err, io.EOF) { continue }          // EOF:继续
    if isTimeout(err) {                              // 短读超时:继续
        if current == nil {                          // 顺手 latch 一下 pending
            select { case c := <-m.pending: current = c; default: }
        }
        continue
    }
    if errors.Is(err, io.ErrNoProgress) { continue } // bufio 空转:继续
    log.Debug().Err(err).Msg("modem read error")
    continue
}
```

readerLoop 对各类读错误都"继续循环",不退出——保证 reader goroutine 健壮。`isTimeout`(`modem.go:632-641`)用 `interface{ Timeout() bool }` 类型断言,防御性地处理 go.bug.st/serial 跨平台超时行为差异。

---

## 五、超时模型(USB transport 接入的关键)

本包的超时是**双层**的:

| 层 | 机制 | 位置 |
|---|---|---|
| 上层(命令级) | `SendAndWait` 的 `select <-time.After(timeout)` | modem.go:137 |
| 底层(读级) | `serial.SetReadTimeout(200ms)` 短超时轮询 | modem.go:71 |

底层 200ms 短超时是关键:它让 `bufio.NewReader(m.port).ReadString('\n')` 不会永久阻塞——每 200ms 唤醒一次,readerLoop 得以检查 `m.closed` channel、响应 Close。

### 与 uicc-go/at 的 deadline 链对比

| | uicc-go/at | sms_gateway/modem |
|---|---|---|
| Read 超时控制 | `SetReadDeadline` + ctx deadline 链 | 一次性 `SetReadTimeout(200ms)`,内部短超时轮询 |
| 需要 deadline 接口? | ✅ `readDeadliner`/`writeDeadliner` | ❌ 不需要 |
| ctx 取消在哪生效 | `run()` 里 `select <-ctx.Done()` | `SendAndWait` 的 `select <-ctx.Done()`(Read 层不感知 ctx) |

### 对 USB transport 的影响

新的 `ATTransport.Read` 应模仿 go.bug.st/serial 的行为:**每次读带 ~200ms 内部超时,超时返回(让 readerLoop 的 isTimeout 分支处理)**。不需要实现 `SetReadDeadline`/`SetWriteDeadline`。用 gousb 的 `context.WithTimeout(200ms)` + `in.ReadContext` 即可实现。

这比 uicc-go/at 方案(原 `plans/usb-transport.md` 第三章设计的 deadline 链)**简单**——deadline 链那一整套 `SetReadDeadline`/`readDeadliner` 接口都不需要。

---

## 六、SMS 操作

### Initialize —— 完整初始化序列(`sms.go:25-46`)

```go
func (m *Modem) Initialize(ctx context.Context, pin string) error {
    m.SendAndWait(ctx, "AT", 2*time.Second)          // ping
    m.SendAndWait(ctx, "ATE0", 2*time.Second)        // 关回显 (V.250 §6.2.1)
    m.SendAndWait(ctx, "AT+CMEE=1", 2*time.Second)   // 数值错误码 (TS 27.007 §8.5)
    m.ensureSIMReady(ctx, pin)                        // AT+CPIN? 等 READY + PIN 解锁
    m.SendAndWait(ctx, "AT+CMGF=0", 2*time.Second)   // PDU 模式 (TS 27.005 §3.2.1)
    m.SendAndWait(ctx, "AT+CNMI=2,1,0,0,0", ...)     // 新短信存存储 + URC
    m.SendAndWait(ctx, `AT+CPMS="SM","SM","SM"`, ...)// SIM 存储
    return nil
}
```

这个序列**严格符合 `docs/04` §5.3 的建议**(显式 ATE0 + CMEE + CPIN 就绪检查)。对比 vohive 缺 CMEE、缺标准 CPIN 检查、缺 CPMS 预设(见 `docs/06` 第八节),本包在初始化规范上更完整。

### ensureSIMReady —— SIM 就绪检查 + PIN 解锁(`sms.go:48-69`)

```go
func (m *Modem) EnsureSIMReady(ctx context.Context, pin string) error {
    lines, _ := m.SendAndWait(ctx, "AT+CPIN?", 5*time.Second)
    for _, l := range lines {
        switch {
        case strings.HasPrefix(l, "+CPIN: READY"):  return nil
        case strings.HasPrefix(l, "+CPIN: SIM PIN"):
            if pin == "" { return errors.New("SIM PIN required ...") }
            m.SendAndWait(ctx, fmt.Sprintf(`AT+CPIN="%s"`, pin), 8*time.Second)
            return nil
        }
    }
    return errors.New("CPIN? returned no recognisable status")
}
```

符合 TS 27.007 §8.1 `+CPIN` 的 READY/SIM PIN 状态机,支持 PIN 解锁。vohive 不在 init 序列做这个检查。

### Send —— CMGS 两步握手(`sms.go:456-490`)

```go
func (m *Modem) Send(ctx context.Context, recipient, body string, udh SubmitUDH) error {
    hexPDU, tpduLen, err := EncodeSubmit(recipient, body, udh)   // 本地编码 PDU
    m.SendAndWait(ctx, fmt.Sprintf("AT+CMGS=%d", tpduLen), 5*time.Second)  // 第一步:发长度,readerLoop 收到 ">"
    
    c := &call{cmd: "", done: make(chan callResult, 1), timeout: 30 * time.Second}
    m.mu.Lock()
    m.SendRaw([]byte(hexPDU))        // 写 PDU hex
    m.SendRaw([]byte{0x1A})          // Ctrl-Z (TS 27.005 §3.5.1)
    m.pending <- c                   // 投递空 call 等最终 OK
    m.mu.Unlock()

    select {
    case res := <-c.done:  return res.err
    case <-time.After(30 * time.Second): return errors.New("CMGS body: timeout waiting for OK")
    case <-ctx.Done():    return ctx.Err()
    case <-m.closed:      return errors.New("modem closed")
    }
}
```

完整覆盖 `AT+CMGS=<len>` → 等 `>` → 写 PDU+Ctrl-Z → 等 OK 的协议握手(TS 27.005 §3.5.1)。0x1A Ctrl-Z 终止符符合标准。

### ListStored / DeleteStored(`sms.go:417-446`)

```go
func (m *Modem) ListStored(ctx context.Context) ([]StoredMessage, error) {
    lines, _ := m.SendAndWait(ctx, "AT+CMGL=4", 10*time.Second)   // 4 = all status
    // 解析 "+CMGL: <idx>,<stat>,<alpha>,<length>\n<pdu hex>" 两行一组
}
func (m *Modem) DeleteStored(ctx context.Context, index int) error {
    _, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CMGD=%d", index), 5*time.Second)
    return err
}
```

`+CMGL=4`(TS 27.005 §3.4.2)+ `+CMGD=<index>`(§3.5.4)。注意只支持单条删除,不支持 vohive 的 `AT+CMGD=1,4` 全删。

---

## 七、PDU 编解码(pdu.go)

pdu.go 是**手写**的 3GPP TS 23.040 + TS 23.038 子集,**零外部依赖**(只用 `encoding/hex`/`fmt`/`strings`/`time`/`unicode/utf16`)。

### 文件头注释明确声明范围(`pdu.go:1-17`)

```go
//   - 7-bit GSM default alphabet, packed and unpacked. No GSM-7 extension
//     table — characters outside the basic set get the substitute '?'.
//   - UCS2 (UTF-16BE) for messages containing characters outside the basic
//     GSM-7 set. Auto-selected by EncodeSubmit when the body has any non-7-bit
//     character.
//   - Receive-side UDH/concat metadata extraction. Transmit-side multi-part
//     encoding is not supported; messages longer than one PDU are rejected by
//     EncodeSubmit and the caller can split them.
```

这段注释**自己承认了三个缺陷**(见下文 7.4)。

### 7.1 EncodeSubmit —— 发送 PDU(`pdu.go:142-233`)

构建 SMS-SUBMIT PDU。first octet 硬编码 `0x11`(VPF=relative),UDH 存在时 OR `0x40`(UDHI)。PID=`0x00`,DCS=`0x00`(GSM7)或 `0x08`(UCS2),VP=`0xAA`(4天)。SMSC 用 `00`(用 SIM 里存的 SMSC)。

自动选 UCS2:body 含任何非 GSM-7 基本集字符时 `needsUCS2` 返回 true(`pdu.go:385-392`),切 UCS2 编码。

### 7.2 DecodeDeliver —— 接收 PDU(`pdu.go:43-120`)

解析 SMS-DELIVER。按字段顺序消费:SCA → PDU type → OA(originator address)→ PID → DCS → SCTS → UDL → UD。地址解码 `decodeAddress`(`pdu.go:275-298`)。

### 7.3 各编解码组件

| 组件 | 位置 | 说明 |
|---|---|---|
| **地址 BCD** | `decodeAddress`/`encodeAddress` (275-333) | semi-octet nibble-swap,0x91 国际/0x81 国内 |
| **SCTS 时间戳** | `decodeSCTS` (337-367) | 7字节 BCD nibble-swap + 时区符号位 + `time.FixedZone`,**符合 TS 23.040 §9.1.2.5** |
| **GSM-7 编码** | `encodeGSM7` (394-420) | LSB-first 打包 7-bit septets 到 8-bit octets |
| **GSM-7 解码** | `decodeGSM7FromBitOffset` (426-461) | 支持任意 bitOffset(UDH fill-bit 对齐) |
| **GSM-7 fill-bit** | `packGSM7Shifted` (245-271) | UDH 后的 fill-bit 对齐,fillBits=1 对 6字节 UDH 正确 |
| **UCS-2** | `encodeUCS2`/`decodeUCS2` (467-483) | UTF-16BE,用 `unicode/utf16` 正确处理代理对 |
| **UDH 解析** | `parseUDH` (540-574) | 识别 IEI 0x00(8-bit ref)和 0x08(16-bit ref) |

### 7.4 三个明确缺陷(重要)

**缺陷 1:无 GSM-7 扩展表(TS 23.038 §6.2.1)**

`pdu.go:4-5` 注释 + `pdu.go:251,400` 代码:扩展字符 `^{}[]~|\\€` 一律映射成 `'?'`。收到含扩展字符的短信会丢字。`gsm7Set`(`pdu.go:373`)包含 `0x1b`(ESC)但不解释扩展表。

**缺陷 2:发送只支持 8-bit ref UDH,不支持 16-bit ref**

`pdu.go:161` UDH 硬编码 IEI 0x00(8-bit ref):`[]byte{0x05, 0x00, 0x03, ...}`。IEI 0x08(16-bit ref)只在接收侧 `parseUDH`(`pdu.go:564`)识别。

**缺陷 3:发送不支持自动分段**

`pdu.go:191,199,209` 超长直接报错(`"gsm-7 multipart segment exceeds 153 septets"`),靠调用方自己切。vohive 用 smscodec 自动分段(见 `docs/06` 第七节)。

> 接收侧的 UDH 解析是完整的(0x00/0x08 都支持),但**没有重组**——返回 `ConcatInfo` 给调用方,调用方自己拼。vohive 有 `Reassembler` 做自动重组。

### 7.5 地址解码的限制

`decodeAddress`(`pdu.go:286-288`)注释说明不处理 0xD0 字母数字 sender( alphanumeric),那些会原样留 hex。对纯数字号码够用。

---

## 八、Transport 耦合与改造点

### serial.Port 出现的位置(全在 modem.go)

`go.bug.st/serial` 的 `serial.Port` 接口在整个 modem 包里**只出现 7 处,全部在 `modem.go`**:

| 位置 | 代码 | 用途 |
|---|---|---|
| import | `modem.go:26` | `"go.bug.st/serial"` |
| 字段 | `modem.go:30` | `port serial.Port` |
| 构造 | `modem.go:61-66` | `serial.Mode{...}` 波特率参数 |
| 打开 | `modem.go:67` | `serial.Open(port, mode)` |
| 超时 | `modem.go:71` | `p.SetReadTimeout(200*time.Millisecond)` |
| Close | `modem.go:88` | `m.port.Close()` |
| Write | `modem.go:120,155` | `m.port.Write(...)` |
| Read | `modem.go:526` | `bufio.NewReader(m.port)` |

**`sms.go` 和 `pdu.go` 完全不碰 port**——它们只用 `*Modem` 的高层方法(SendAndWait/SendRaw)和纯函数(EncodeSubmit/DecodeDeliver)。

### 改造方案(最小侵入,~25 行)

1. 把 `Modem.port` 字段类型从 `serial.Port` 改成自定义接口或直接 `io.ReadWriteCloser`:

```go
type Port interface {
    io.ReadWriteCloser
    // SetReadTimeout 可选;USB 实现里塞个 no-op,超时由 ATTransport.Read 自己管
}
```

2. 加构造函数 `NewFromIO`,跳过 `serial.Open`,直接喂 USB endpoint 包装:

```go
func NewFromIO(port io.ReadWriteCloser) *Modem {
    m := &Modem{port: port, pending: make(chan *call, 1), closed: make(chan struct{})}
    go m.readerLoop()
    return m
}
```

3. `Open` 函数保留(给串口路径用),`NewFromIO` 给 USB 路径用。

`go.bug.st/serial` 的 `Port` 接口签名实际上是 `io.ReadWriteCloser` + `SetReadTimeout` + 几个配置方法。USB transport 包装成 `io.ReadWriteCloser` 后,readerLoop 里 `bufio.NewReader(m.port).ReadString('\n')` 直接可用——唯一要求是 `Read` 要有短超时语义(见第五节),否则 readerLoop 卡死。

---

## 九、复用评估

### 复制范围

| 文件 | 行数 | 复制后改动 |
|---|---|---|
| `modem.go` | 647 | ~25 行(port 字段类型 + NewFromIO);可删 ICMP ping(~210 行,161-373) |
| `sms.go` | 490 | 几乎零改动(只依赖 `*Modem` 高层方法) |
| `pdu.go` | 574 | **零改动**,纯自包含 |

**总复制量:~1700 行(删 ping 后约 1500 行),零外部 modem 依赖。**

### 依赖处理

- `go.bug.st/serial`:USB 方案不用,改造时从 import 移除
- `zerolog`:几处 `log.Debug().Str(...).Msg(...)`,可全局替换成项目 logger 或 `fmt`

### 结论

`sms_gateway/modem` 是三个候选中**最适合"USB transport + 短信"目标**的:

- ✅ 能发任意裸 AT(`SendAndWait`)——uicc-go/at 做不到
- ✅ transport 改造量最小(~25 行,单文件集中)——vohive 要拖 5 个内部包
- ✅ 完整 SMS 收发编排(CMGS 两步握手/CMGL/CMDGD/`+CMTI` URC)
- ✅ 自包含 PDU 编解码(574 行,零依赖)
- ✅ 初始化规范完整(ATE0/CMEE/CPIN/CPMS)
- ⚠️ PDU 层有三个缺陷(无扩展表/16-bit ref/自动分段)——阶段 1 短短信够用,后续可用 smscodec 升级(见 `docs/07` 第五节)

对比详见 `docs/07-at-implementation-comparison.md`。
