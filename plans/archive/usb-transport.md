# 阶段 1 实施计划:USB transport 包 + sms_gateway/modem 集成

> 本文件是**设计文档**,记录 USB transport 层 + sms_gateway/modem AT 协议层接入的实施方案。
> 创建于 2026-07-11。**v2(2026-07-12):基于 `docs/05-07` 调研结论,AT 协议层从 uicc-go/at 改为 sms_gateway/modem。**
> 对应 `docs/01` 阶段 1 的第 2 步(endpoint 探测 + AT 裸验证已完成)。

---

## 选型背景(为什么是 sms_gateway/modem)

本计划选定 **sms_gateway/modem** 作为 USB transport 的上层 AT 协议层。决策依据见 `docs/07-at-implementation-comparison.md`,核心要点:

1. **整个生态不存在"能发裸 AT + transport 可注入"的现成组合**(`docs/07` 第一节决策矩阵)。sms_gateway/modem 能发裸 AT(`SendAndWait`),transport 改造量最小(~25 行)。
2. **标准符合度对比**(A-E 五大类)不是一边倒:SG 在 AT 语法基础 + 初始化规范(A/E)更对,VH 在 SMS 命令覆盖 + PDU + URC(B/C/D)更全(`docs/07` 第二节)。
3. **接入点要求 AT 语法层正确**(transport-agnostic 框架 + 回显鲁棒 + 错误码可获取)。SG 在这三点上都更对——VH 的 `Contains("ERROR")` + 无 CMEE + 无回显兜底是真实缺陷。
4. **PDU 三缺陷阶段 1 可接受**(无 GSM-7 扩展表/16-bit ref/自动分段),后续可升级到 vohive `smscodec`(`docs/07` 第五节升级路径)。

> **与 v1 的差异**:v1 基于 uicc-go/at(只能 CSIM,要纠结"导出 run()"三路径)。v2 换成 sms_gateway/modem 后,4.3 端到端验证不再纠结——`SendAndWait(ctx, "AT+CSQ", 5s)` 直接发裸 AT。

---

## 一、目标

实现 `internal/usbtransport/` 包,把 MI_02 的 USB bulk endpoint 包装成 `io.ReadWriteCloser`,直接喂给 sms_gateway/modem 的 `NewFromIO` 构造函数,实现**纯用户态 AT 通道 + PDU 短信能力**。

这是 `docs/01` 阶段 1 路线图的第 2-3 步合并:
1. ✅ 用 gousb 打开模块,claim Iface 2,打开 bulk IN/OUT endpoint(`cmd/usbprobe/` + `cmd/attest/` 已完成)
2. **← 本计划** 实现一个 `io.ReadWriteCloser` 包装 USB endpoint,接入 sms_gateway/modem 的 `Modem`
3. **← 本计划** 验证完整链路:USB transport → modem.SendAndWait → 真实 AT 命令(含短信初始化 Initialize)

---

## 二、Step 1 — 复制 sms_gateway/modem 到项目内

### 2.1 为什么复制而非 replace

原计划曾考虑 `replace`,有致命缺陷(详见 `docs/05` 第八节):
- **不可移植**:绝对路径换机器、移动目录、CI 全断
- **污染**:sms_gateway 的 `go.bug.st/serial` 等依赖会被 `go mod tidy` 拉进来(USB 方案用不到串口)

复制进项目 = 完全自包含,可移植,零外部路径依赖。

### 2.2 复制范围

复制 `source/sms_gateway/agent/internal/modem/` 的 **3 个生产文件**(共 1711 行,`docs/05` 第一节):

| 文件 | 行数 | 作用 | 改动量 |
|---|---|---|---|
| `modem.go` | 647 | Modem 结构体、并发模型、readerLoop、SendAndWait/SendRaw、ICMP ping | **~25 行**(transport 接口化 + NewFromIO);可删 ICMP ping(~210 行,161-373) |
| `sms.go` | 490 | Initialize/Send/ListStored/DeleteStored + 信号/ICCID/IMEI/运营商查询 | **几乎零改动**(只依赖 *Modem 高层方法) |
| `pdu.go` | 574 | SMS PDU 编解码(GSM-7/UCS-2/UDH/address/SCTS),手写自包含 | **零改动** |

不复制:agent 的 `runner/`(编排层,绑 backend HTTP)、`esim/`(eUICC)、`client/`/`config/`/`state/`/`ota/`。

### 2.3 放置位置

复制到 `third_party/sms-gateway/modem/`。理由(对齐 v1 的 `third_party/uicc-go/` 思路):
- 保留原模块层级(`sms_gateway/agent/internal/modem/` → `sms-gateway/modem/`),标注第三方来源
- 将来同步上游时目录结构对应,方便 diff
- import 路径:`dji-modem-research/third_party/sms-gateway/modem`
- 加 LICENSE:sms_gateway 是 AGPL-3.0,复制 LICENSE 到 `third_party/sms-gateway/`(合规)

### 2.4 transport 接口化改造(关键,~25 行)

`modem.go:30` 的 `port serial.Port` 是整个包**唯一的 transport 耦合点**(`docs/05` 第八节列了 7 处出现位置,全在 modem.go)。改造:

**改动 1**:`port` 字段类型从 `serial.Port` 改成自定义接口

```go
// Port 是 modem 的 transport 抽象。生产代码用 USB endpoint 实现;
// 测试用 testutil.ScriptPort 实现。readPollInterval 用于短超时轮询语义
// (见 §3.5),可让 Read 在无数据时周期性返回而非永久阻塞。
type Port interface {
    io.ReadWriteCloser
}
```

> 简化版:直接用 `io.ReadWriteCloser`,不额外定义 `Port` 接口。modem.go 只调 `m.port.Write`/`m.port.Close`/`bufio.NewReader(m.port).Read`,这三个 `io.ReadWriteCloser` 都满足。

**改动 2**:加 `NewFromIO` 构造函数,跳过 `serial.Open`

```go
// NewFromIO wraps any io.ReadWriteCloser (e.g. a USB endpoint) as a Modem,
// bypassing the serial-port Open path. Enables userland USB transports to feed
// raw AT bytes into the existing protocol layer without modification.
//
// 调用方负责确保 Read 有短超时轮询语义(见 §3.5),否则 readerLoop 会卡在
// 阻塞 Read 上无法响应 Close。
func NewFromIO(port io.ReadWriteCloser) *Modem {
    m := &Modem{
        port:    port,
        pending: make(chan *call, 1),
        closed:  make(chan struct{}),
    }
    go m.readerLoop()
    return m
}
```

**改动 3**:`Open` 函数保留(串口路径,本机不常用但保留兼容),移除 `SetReadTimeout(200ms)` 的强依赖——`NewFromIO` 路径里超时由 `ATTransport.Read` 自己管。

**改动 4**(可选):移除 ICMP ping(`modem.go:161-373`,~210 行,IcmpPing/pingDialect/capturePingURC 等)。阶段 1 不需要蜂窝 ping,删掉减负。`sms.go` 里如果有引用 IcmpPing 的地方一并删。

**改动 5**(可选):处理 zerolog 依赖。modem.go 有几处 `log.Debug().Str(...)`(`modem.go:119,130,143,612`)。要么拉 zerolog,要么全局替换成 fmt/log。倾向替换(减一个依赖)。

### 2.5 go.mod 依赖

复制后 `go.mod` 变化:
- **移除** `go.bug.st/serial`(USB 方案不用,modem.go 的 serial.Open 保留但走 Open() 路径才触发,NewFromIO 不触发)
- **可能新增** zerolog(若不替换 logger)或零新增(若替换成 stdlib log)
- `go mod tidy` 会清理未用的依赖

---

## 三、Step 2 — usbtransport 包设计

### 3.1 位置与命名

新建 **`internal/usbtransport/`**(对齐已有的 `internal/usbdesc/`、`internal/testutil/`)。

```
internal/usbtransport/
├── usbtransport.go                 # ATTransport 结构体 + Open/Read/Write/Close
├── usbtransport_test.go            # mock 单测(用 testutil.ScriptPort)
└── usbtransport_hardware_test.go   # 硬件集成测试(build tag: hardware)
```

### 3.2 测试分层(遵守 AGENTS.md「测试方案」)

| 文件 | 依赖硬件? | 测试方式 | 跑在 |
|---|---|---|---|
| `usbtransport_test.go` | ❌ | 用 `testutil.ScriptPort` 注入 mock | `make test` / `make test-race` |
| `usbtransport_hardware_test.go` | ✅ | 真 EC25 + WinUSB | `make test-hardware` |

**关键设计**:USB 操作被接口包裹,业务逻辑(短超时轮询、Close 打断 Read、并发安全)必须能脱离 gousb 测试。具体做法见 3.6。

### 3.3 结构体

```go
package usbtransport

import (
    "context"
    "sync"
    "time"

    "github.com/google/gousb"
)

const (
    // readPollInterval 控制无数据时 Read 的唤醒周期。模仿 go.bug.st/serial
    // 的 SetReadTimeout(200ms) 语义(modem.go:71),让 readerLoop 能周期性
    // 检查 m.closed channel、响应 Close。见 §3.5 超时模型。
    readPollInterval = 200 * time.Millisecond
)

// ATTransport wraps MI_02 (AT command port) USB bulk endpoints as an
// io.ReadWriteCloser, ready to feed into modem.NewFromIO.
type ATTransport struct {
    dev   *gousb.Device
    cfg   *gousb.Config
    iface *gousb.Interface
    out   *gousb.OutEndpoint // EP 0x03 OUT bulk
    in    *gousb.InEndpoint   // EP 0x84 IN bulk

    mu     sync.Mutex
    closed bool
}
```

注意:与 v1 的 `ATTransport` 不同,**不需要** `readDeadline`/`writeDeadline` 字段——sms_gateway/modem 没有 deadline 链(`docs/05` 第五节),超时由内部短轮询控制。

### 3.4 方法

#### Open

```go
// Open 打开指定设备的指定接口,claim 它,返回 *ATTransport。
// 对 MI_02 AT 口:ifaceNum=2, epOut=0x03, epIn=0x84(见 AGENTS.md 实测结果)。
func Open(vid, pid uint16, ifaceNum, epOut, epIn int) (*ATTransport, error)
```

实现(对齐 `cmd/attest/main.go` 已验证的流程):
1. `ctx.OpenDeviceWithVIDPID(vid, pid)`
2. `dev.Config(1)` 激活配置 1
3. `cfg.Interface(ifaceNum, 0)` claim 接口
4. `iface.OutEndpoint(epOut)` / `iface.InEndpoint(epIn)`
5. 组装 ATTransport 返回

#### Read(关键,短超时轮询语义见 3.5)

```go
func (t *ATTransport) Read(buf []byte) (int, error)
```

#### Write

```go
func (t *ATTransport) Write(buf []byte) (int, error)
// 直接 t.out.Write(buf)。gousb OutEndpoint.Write 阻塞直到写完。
// modem.go 的 SendAndWait 里 Write 由 m.mu 保护(modem.go:115-126)。
```

#### Close

```go
func (t *ATTransport) Close() error
// 置 closed=true → iface.Close() + cfg.Close() + dev.Close(),聚合错误。
// closed 标志让阻塞中的 Read 提前返回(modem.go readerLoop 依赖 Close 唤醒)。
```

### 3.5 Read 的超时语义(关键设计点 — 与 v1 完全不同)

**v1 方案**(uicc-go/at):超时走 deadline 链,Read 超时返回 `(0, nil)` 让 bufio 重试。
**v2 方案**(sms_gateway/modem):**短超时轮询**,模仿 go.bug.st/serial 的 `SetReadTimeout(200ms)`。

**为什么变了**:sms_gateway/modem 的 `readerLoop`(`modem.go:525`)工作机制不同——
1. `bufio.NewReader(m.port).ReadString('\n')`(`modem.go:526`)循环读行
2. 底层 Read 每 200ms 唤醒一次(靠 `serial.SetReadTimeout(200ms)`,`modem.go:71`)
3. 唤醒后 readerLoop 检查 `m.closed` channel(`modem.go:531-535`),响应 Close
4. 短读/超时当 "nothing to do" 继续(`modem.go:544-554` 的 `isTimeout` 分支)

**Read 实现**:

```go
func (t *ATTransport) Read(buf []byte) (int, error) {
    // 短超时轮询:每次最多阻塞 readPollInterval(200ms)。
    // 超时返回带 Timeout() 的 error,让 modem.go isTimeout 分支处理。
    // 这样 readerLoop 能周期性唤醒,检查 closed channel。
    rctx, cancel := context.WithTimeout(context.Background(), readPollInterval)
    defer cancel()
    n, err := t.in.ReadContext(rctx, buf)
    if err == context.DeadlineExceeded {
        return n, err   // context.DeadlineExceeded 实现 Timeout() bool → true
                         // modem.go:636 isTimeout 会识别为超时,readerLoop 继续
    }
    return n, err
}
```

**关键事实**(已核实):
- gousb `InEndpoint.ReadContext` 超时,文档(`endpoint.go:139`)说返回 `TransferCancelled`,但 **attest 实测**(`cmd/attest/main.go:77`)超时返回 `context.DeadlineExceeded`
- `context.DeadlineExceeded` **实现了** `interface{ Timeout() bool }`(返回 true)
- modem.go:636 `isTimeout` 用这个接口断言,所以能**正确识别** USB 超时为"nothing to do"

这个方案比 v1 的 deadline 链**简单得多**——不需要 `SetReadDeadline`/`SetWriteDeadline`,不需要 `readDeadliner`/`writeDeadliner` 接口,modem 包根本没有 deadline 链。

### 3.6 可测性设计(满足 mock 单测要求)

`ATTransport` 直接持有 `*gousb.InEndpoint`/`*gousb.OutEndpoint`(具体结构体,`endpoint.go:116,152`),无法用 `testutil.ScriptPort` mock。为了 Read 的短超时轮询逻辑可单测,把 I/O 抽成接口:

```go
// endpointReader abstracts the gousb IN endpoint so Read 逻辑能离线测试。
type endpointReader interface {
    ReadContext(ctx context.Context, buf []byte) (int, error)
}

// endpointWriter abstracts the gousb OUT endpoint.
type endpointWriter interface {
    Write(buf []byte) (int, error)
}
```

- **生产代码**(usbtransport.go):gousb 的 `*InEndpoint`/`*OutEndpoint` 天然满足这两个接口(方法签名对得上),直接传入
- **单测**(usbtransport_test.go):注入用 `testutil.ScriptPort` 实现的 endpointReader/Writer

**适配器**:ScriptPort 的 `Read([]byte) (int,error)` 需要适配成 `ReadContext(ctx, []byte)`。写一个小适配器:

```go
// scriptReader adapts testutil.ScriptPort to endpointReader.
type scriptReader struct{ port *testutil.ScriptPort }
func (r *scriptReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
    // ScriptPort.Read 已是阻塞语义;在 goroutine 里调 + select ctx.Done 模拟短超时
    type result struct{ n int; err error }
    ch := make(chan result, 1)
    go func() { n, err := r.port.Read(buf); ch <- result{n, err} }()
    select {
    case res := <-ch:    return res.n, res.err
    case <-ctx.Done():   return 0, ctx.Err()  // 模拟 gousb 超时
    }
}
```

这样 Read 的短超时轮询逻辑(3.5 核心)有单测覆盖,满足 AGENTS.md 的覆盖率目标(Transport 适配层 ≥ 80%)。

### 3.7 对齐 modem.readerLoop 的工作流

完整调用链:

```
modem.NewFromIO(transport)        ← 传入我们的 ATTransport
  └─ go m.readerLoop()             ← 启动唯一 reader goroutine(modem.go:80)

readerLoop (modem.go:525)
  ├─ bufio.NewReader(m.port).ReadString('\n')    ← 内部调 transport.Read
  │    └─ (每次) context.WithTimeout(200ms) + in.ReadContext
  │         └─ 超时 → context.DeadlineExceeded → isTimeout → continue
  ├─ select <-m.closed → return      ← Close 在这里生效
  └─ 行分发(回显/OK/ERROR/>/URC)

SendAndWait(ctx, "AT+CSQ", 5s)    ← 公开方法
  ├─ m.port.Write([]byte("AT+CSQ\r\n"))    ← 直接调 transport.Write
  ├─ m.pending <- c
  └─ select { <-c.done | <-time.After(5s) | <-ctx.Done() | <-m.closed }
```

---

## 四、Step 3 — 测试

### 4.1 mock 单测(`usbtransport_test.go`,无硬件)

用 `testutil.ScriptPort` + 3.6 的 `scriptReader` 适配器,覆盖:

| 测试 | 验证点 |
|---|---|
| `TestReadShortTimeout` | 无数据时 Read 在 readPollInterval 后返回超时 error(带 `Timeout() bool`) |
| `TestReadReturnsData` | 有数据时 Read 正常返回字节 |
| `TestReadPartialData` | 收到部分数据正常返回 |
| `TestWriteFull` | Write 全量写出,记录到 ScriptPort.Written() |
| `TestCloseTerminatesRead` | Close 打断阻塞的 Read(modem.go readerLoop 依赖此语义) |
| `TestConcurrentReadWrite` | `-race` 下并发 Read+Write 安全 |
| `TestATTransportAsIO` | ATTransport 满足 `io.ReadWriteCloser`(编译期断言 `var _ io.ReadWriteCloser = (*ATTransport)(nil)`) |
| `TestReadTimeoutHasTimeoutMethod` | Read 返回的超时 error 实现 `Timeout() bool`(modem.go isTimeout 依赖) |

跑:`make test-race`(`-race` 硬性要求)。

### 4.2 modem 包集成测试(可选,复制进来的 modem 包能否编译)

确认 `third_party/sms-gateway/modem/` 的 `NewFromIO` + `SendAndWait` 能用 ScriptPort mock 跑通:
- 注入 ScriptPort(预载 `"\r\nOK\r\n"`)
- `NewFromIO(port)` → `SendAndWait(ctx, "AT", 2s)` → 断言返回空 `[]string` + nil error
- 验证 readerLoop + pending channel + isTerminator 全链路

### 4.3 硬件集成测试(`usbtransport_hardware_test.go`,build tag)

```go
//go:build hardware

package usbtransport

// 需要 EC25 + WinUSB。真实 Open MI_02,发 AT,收 OK。
```

跑:`make test-hardware`。

### 4.4 端到端验证(`cmd/attest2/main.go` 或 hardware test)

验证完整链路:**usbtransport → modem.NewFromIO → modem.Initialize → 真实 AT 命令**。

```go
func main() {
    // 1. USB transport 打开 MI_02
    transport, err := usbtransport.Open(0x2C7C, 0x0125, 2, 0x03, 0x84)

    // 2. 喂给 modem.NewFromIO(零改造!)
    m := modem.NewFromIO(transport)
    defer m.Close()

    // 3. 完整初始化(ATE0/CMEE=1/CPIN?/CMGF=0/CNMI/CPMS)
    if err := m.Initialize(ctx, ""); err != nil { log.Fatal(err) }

    // 4. 发裸 AT 命令验证(不再纠结 CSIM/raw-AT 三路径!)
    lines, err := m.SendAndWait(ctx, "AT+CSQ", 5*time.Second)
    fmt.Println(lines)  // ["+CSQ: 23,0"] —— 信号查询成功

    // 5. (可选)短信验证
    // m.ListStored(ctx)   // 列存储的短信
    // m.Send(ctx, "+86...", "hello", SubmitUDH{})  // 发短信
}
```

**与 v1 的关键区别**:不再纠结"at.Reader 只能 CSIM、要不要导出 run()、三路径选哪条"。`SendAndWait` 直接发任意裸 AT,`Initialize` 做完整初始化——链路验证一步到位。

**成功标准**:
- `Initialize` 无错误返回(SIM 就绪 + PDU 模式 + URC 订阅)
- `SendAndWait("AT+CSQ")` 返回 `["+CSQ: <rssi>,<ber>"]`
- 证明 USB transport 能驱动完整的 sms_gateway/modem AT 协议层

对比 `cmd/attest/main.go`(裸 endpoint):那个要自己拼 `AT\r\n` + 解析回显,这个走 modem 自动处理回显过滤、OK/ERROR 解析、初始化序列。

---

## 五、Step 4 — 文档 + 提交

- 更新 `AGENTS.md`:
  - 目录结构加 `third_party/sms-gateway/modem/` 和 `internal/usbtransport/`
  - 记录 sms_gateway/modem 复制方案(非 replace)及其理由(指向 `docs/05-07`)
  - 记录 NewFromIO 构造函数、短超时轮询 Read 语义
- git commit(会触发 `.githooks/pre-commit` 跑 `go test -race`)

---

## 六、不做的事(留给后续)

- 不接 MI_04 QMI 通道(阶段 2)
- 不升级 PDU 层到 smscodec(`docs/07` 第五节升级路径,阶段 1 后续按需)
- 不复制 euicc-go / quectel-qmi-go(各自阶段再说)
- 不做跨平台验证(本机 Windows 优先;macOS/Linux 用同一套 gousb API,后续验证)
- 不删 modem.go 的 ICMP ping(可选优化,留着不影响功能)

---

## 七、风险点

### 1. Read 超时返回值与 modem.isTimeout 的对接(主要风险)

**背景**:modem.go:636 `isTimeout` 用 `interface{ Timeout() bool }` 类型断言识别超时。我们的 `Read` 用 `context.WithTimeout` + `in.ReadContext`,超时返回的 error 类型决定能否被识别。

**已核实的事实**:
- gousb `endpoint.go:139` 文档说 context 取消返回 `TransferCancelled`
- 但 `cmd/attest/main.go:77` **实测**超时返回 `context.DeadlineExceeded`(与文档不符)
- `context.DeadlineExceeded` 实现 `Timeout() bool`(返回 true)→ modem.go isTimeout **能正确识别**
- 所以风险**比预想的小**——只要超时 error 实现 `Timeout() bool`,modem readerLoop 就会当 "nothing to do" 继续

**残留风险**:如果某些平台/libusb 版本超时返回的是 `TransferCancelled`(非 `Timeout()` 实现),isTimeout 不识别 → readerLoop 走 `log.Debug().Err(err).Msg("modem read error")`(:561)+ continue。虽然不会崩,但日志噪音。**缓解**:Read 里显式把非超时 error 转成 `context.DeadlineExceeded`,或包一层让 isTimeout 能识别。

**验证**:`TestReadTimeoutHasTimeoutMethod`(4.1)测 error 类型;硬件测试验证真实 ReadContext 行为。

### 2. gousb claim 冲突

测试前确保 `cmd/attest/main.go` 等无残留进程占用 MI_02。纯运维问题。

### 3. modem 包复制后能否编译

`modem.go` 改了 `port` 字段类型后,`Open` 函数(走 serial.Open 路径)可能受影响。需确认:
- `NewFromIO` 路径不触发 serial 相关代码
- `Open` 路径保留 serial.Open(本机不用,但保留兼容)——或者直接删 Open,只留 NewFromIO
- zerolog 依赖处理(替换成 stdlib 或保留)

`go build ./third_party/sms-gateway/modem/` 验证。

### 4. 短超时轮询的 CPU 开销

Read 每 200ms 唤醒一次(无数据时),readerLoop 空转。对 AT 命令通道(低吞吐)开销可忽略——不是数据通道。但要注意:`context.WithTimeout` 每次创建 ctx + cancel,高频时有少量分配。AT 场景频率低,不是问题。

### 5. AT 回显

modem.Initialize 发 `ATE0`(`sms.go:29`)关回显,readerLoop 还有 `line == current.cmd` 兜底(`modem.go:579`)。双保险,无需特殊处理。对比 vohive 只有 ATE0 无兜底(`docs/06` 第四节),SG 更稳——对 USB CDC-AT 场景尤其重要。

---

## 八、相关文件

| 文件 | 作用 |
|---|---|
| `source/sms_gateway/agent/internal/modem/modem.go` | 源——复制到 third_party/sms-gateway/modem/ |
| `source/sms_gateway/agent/internal/modem/sms.go` | 源——Initialize/Send/ListStored |
| `source/sms_gateway/agent/internal/modem/pdu.go` | 源——PDU 编解码(零改动) |
| `internal/testutil/scriptport.go` | 项目自己的 mock io.ReadWriteCloser(测 usbtransport 用) |
| `internal/usbdesc/usbdesc.go` | 已有的纯逻辑包(命名/分层参考) |
| `cmd/usbprobe/main.go` | endpoint 枚举(已完成,硬件) |
| `cmd/attest/main.go` | 裸 AT 验证(已完成,硬件;ReadContext 实测超时返回值的依据) |
| `docs/05-sms-gateway-modem-analysis.md` | sms_gateway/modem 深度剖析(本计划的技术依据) |
| `docs/06-vohive-modem-analysis.md` | vohive/modem 剖析(对比参考) |
| `docs/07-at-implementation-comparison.md` | 标准符合度对比 + 选型理由(本计划的决策依据) |
| `docs/04-at-command-standards.md` | AT 命令电信标准规范(对比标尺) |
| `third_party/sms-gateway/modem/` | **本计划新增**(复制 3 文件) |
| `internal/usbtransport/usbtransport.go` | **本计划新增** |
| `internal/usbtransport/usbtransport_test.go` | **本计划新增**(mock 单测) |
| `internal/usbtransport/usbtransport_hardware_test.go` | **本计划新增**(build tag) |

---

## 九、实测数据参考(来自 AGENTS.md)

DJI 百望 EC25 模式(PID 0125)MI_02 AT 口:
- EP **0x03** OUT bulk(maxPacket=512)
- EP **0x84** IN bulk(maxPacket=512)
- EP 0x85 IN intr(maxPacket=10)— AT 口的 interrupt,通常不用

规律:OUT 端点递增 0x01~0x05,IN 端点递增 0x81~0x89。

---

## 十、v1 → v2 变更摘要

| 章节 | v1(uicc-go/at) | v2(sms_gateway/modem) |
|---|---|---|
| 选型背景 | 无(直接选 uicc-go) | 新增,指向 docs/07 决策 |
| 二、复制对象 | uicc-go/at 6 文件(1314 行) | sms_gateway/modem 3 文件(1711 行) |
| 二、改造 | 加 NewReader 导出(1 行) | 加 NewFromIO + transport 接口化(~25 行) |
| 三、Read 超时语义 | deadline 链,超时返回 (0,nil) | 短超时轮询(200ms),返回带 Timeout() 的 error |
| 三、deadline 接口 | 需要 readDeadliner/writeDeadliner | **不需要**(modem 包无 deadline 链) |
| 三、可测性 | endpointReader/Writer 接口 | 同 + scriptReader 适配器(ReadContext) |
| 四、端到端验证 | 三路径纠结(CSIM/raw-AT/绕开) | 直接 SendAndWait 发裸 AT + Initialize |
| 七、风险点 1 | TransferCancelled vs errIOTimedOut(未导出) | DeadlineExceeded 实现 Timeout()→isTimeout 识别(已核实) |
| 八、相关文件 | uicc-go/at 源 + at_test.go | sms_gateway/modem 3 源文件 + docs/05-07 |
