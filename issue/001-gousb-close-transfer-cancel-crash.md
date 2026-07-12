# Issue 001 — gousb/libusb close 时 transfer cancel 崩溃(同进程多测试触发)

> 发现于 2026-07-12(smscodec 升级路线 B 硬件验证期间)。
> 非 smscodec 升级引入,是 pre-existing 的 transport/gousb 层问题。
> 复现需真实硬件(EC25 模块 + WinUSB)。

---

## 一、现象

同一个 `go test` 进程内**连续跑多个会 open/close USB 的硬件测试**时,进程在
USB teardown 阶段 **segfault**:

```
signal arrived during external code execution
github.com/google/gousb._Cfunc_libusb_cancel_transfer(0x...)
github.com/google/gousb.libusbImpl.cancel.func1(...)
	C:/.../.gopath/pkg/mod/github.com/google/gousb@v1.1.3/libusb.go:466
...
FAIL	dji-modem-research/internal/usbtransport	0.9s
```

崩溃点固定在 gousb 的 CGO 胶水调用 `libusb_cancel_transfer`。

### 触发条件矩阵

| 场景 | 结果 |
|---|---|
| 单个发送测试单独跑(`-run TestHardwareSMSSend$`) | ✅ PASS |
| 多段发送测试单独跑(`-run TestHardwareSMSSendMultiPart`) | ✅ PASS |
| 只读测试 ×3 同进程(ListStored + ATPing + Initialize) | ✅ PASS(3 个都过) |
| **发送测试 + 另一个发送/读写测试同进程** | ❌ segfault |

关键:**只读测试连跑 3 个不崩,发送测试连跑就崩。** 单独跑任何测试都不崩。

---

## 二、复现步骤

```bash
# 前置:EC25 模块(PID 0125)插着 + WinUSB 驱动 + 可发短信的 SIM

# 1. 确认模组正常(可选,排除模组卡死)
mise exec -- go test -tags=hardware -run 'TestHardwareSMSSend$' ./internal/usbtransport/
# → PASS

# 2. 触发崩溃:两个发送测试同进程跑
DJI_TEST_SMS_RECIPIENT="+8613800000001" \
  mise exec -- go test -tags=hardware \
  -run 'TestHardwareSMSSend|TestHardwareSMSSendMultiPart' ./internal/usbtransport/
# → 第一个测试 Initialize + AT+CMGS 发送后,segfault
```

> 崩溃后模组可能卡在 CMGS PDU 等待态(无响应)。恢复:发 ESC 字节(0x1B)
> 或拔插 USB。参见 `third_party/sms-gateway/modem/AGENTS.md` §"CMGS 卡死后的恢复"。

---

## 三、崩溃时序(从日志还原)

```
Test 1 (TestHardwareSMSSend):
  Initialize: AT → ATE0 → CMEE → CPIN?(READY) → CMGF → CNMI → CPMS  ✅
  AT+CMGS=47 → lines [">"]  ✅ (收到提示符)
  ← (写 PDU + Ctrl-Z,等 OK)

  → signal arrived during external code execution
  → libusb_cancel_transfer
  → FAIL
```

崩溃发生在**第一个测试的发送流程之后**(已收到 `>`,正在等 OK 或正在 teardown)。
gousb 关闭 context 时取消在途的 IN transfer → `libusb_cancel_transfer` segfault。

---

## 四、根因分析

### 直接原因

`ATTransport.Close()`(`internal/usbtransport/usbtransport.go:146`)按序释放:
`iface.Close()` → `cfg.Close()` → `dev.Close()` → `ctx.Close()`。
gousb 的 `ctx.Close()` 会取消所有**仍处于 submitted 状态的 libusb transfer**
(异步,libusb 提交后 Go 侧返回不代表内核侧完成)。在 Windows/WinUSB 上,
对一个尚未完成的 bulk IN transfer 调用 `libusb_cancel_transfer` 会触发 segfault。

### 为什么只读测试不崩、发送测试崩

- **只读测试**:readerLoop 持续短轮询读(200ms),测试结束时 readerLoop 的
  ReadContext 多半刚好在两次轮询之间(无在途 transfer),close 时没有需取消的 transfer。
- **发送测试**:CMGS 两步握手期间(`>` → 写 PDU → 等 OK),readerLoop 正阻塞在
  ReadContext 等最终 OK,此时 USB IN transfer **确有在途**。测试结束 close →
  cancel 在途 transfer → segfault。

### 为什么单独跑发送测试不崩

单独跑时,close 发生在测试进程即将退出、goroutine 已无并发竞争的窗口;
且 libusb transfer 时序恰好不在"submitted 未完成"态。**这是一个时序敏感的
竞态**——连续两个测试放大了在途 transfer 被 cancel 的概率。

### 与 modem.Close() 的交互

`modem.Close()`(原始版本)直接 `m.port.Close()`,**不等 readerLoop 退出**。
即 readerLoop 可能仍阻塞在 ReadContext(在途 transfer),此时 transport Close
触发 cancel → 崩溃。

---

## 五、已尝试的修复(失败,已回退)

### 尝试:modem.Close() 等 readerLoop 退出后再关 port

给 `Modem` 加 `readerDone chan struct{}`,readerLoop `defer close(m.readerDone)`,
`Close()` 在 `m.port.Close()` 前 `<-m.readerDone`。意图:确保 readerLoop 不再
碰 port(无在途 ReadContext)再关 USB。

```go
func (m *Modem) Close() error {
    m.closeOnce.Do(func() {
        close(m.closed)
        <-m.readerDone   // ← 新增:等 readerLoop 退出
        err = m.port.Close()
    })
    return err
}
```

**结果**:反而导致**单独跑发送测试也崩**(`transfer was cancelled` 错误刷屏后 segfault)。

**失败原因**:测试的 defer 是 LIFO 顺序:
```go
tt, m := openInitializedModem(t)
defer m.Close()   // 后执行
defer tt.Close()  // 先执行 ← transport 先被关
```
`tt.Close()`(transport)先于 `m.Close()`(modem)执行。transport 已关后,
`m.Close()` 的 `<-m.readerDone` 等 readerLoop,而 readerLoop 正在读一个
**已关闭的 transport**,拿到 `transfer was cancelled` 死循环错误,且 cancel
路径同样 segfault。

> 要让这个方向成立,需要先保证 `m.Close()` 在 `tt.Close()` 之前执行(调换 defer
> 顺序或让 modem.Close 内部负责停 readerLoop 且不依赖外部 transport 状态)。
> 这涉及测试 helper 和 transport 职责边界的重新设计,超出 smscodec 升级范围。

**当前状态**:`modem.go` 已回退到原始版本(无 readerDone),smscodec 升级功能
不受影响(单独跑各项测试全过)。

---

## 六、待探索的修复方向

| 方向 | 思路 | 难度 | 风险 |
|---|---|---|---|
| **A. 调换测试 defer 顺序** | `defer m.Close()` 先于 `defer tt.Close()`,或 helper 里 `m.Close()` 内联(不等 defer)再 `tt.Close()` | 低 | 仅修测试,不改库;但 root cause 仍在 |
| **B. readerLoop 退出同步(配合 A)** | modem.Close 等 readerLoop 退出 + 测试保证 modem 先关 | 中 | 需协调两层 close 顺序 |
| **C. transport 不在 close 时 cancel transfer** | ATTransport.Close 先等在途 ReadContext 自然超时(200ms)返回,再释放 iface/cfg/ctx | 中 | 规避 libusb cancel 路径;close 慢 200ms |
| **D. 升级 gousb / 换 USB 库** | gousb v1.1.3 可能有已知 WinUSB cancel bug,查 issue tracker;或评估纯 Go USB 库 | 高 | 大改 transport 层 |
| **E. 测试共享单个 transport** | 硬件测试用 `TestMain` 打开一次 transport,所有测试复用,不反复 open/close | 中 | 改测试架构;规避而非修复 |

**推荐切入点(经 §九 源码调研后修订)**:首选 **F**(长生命期读 context,消除每 200ms 一次的 cancel),从根源降低 cancel 频率。**A**(调换 defer)作为零成本验证辅助。

---

## 七、影响评估

- **生产代码**:无影响。生产场景下 modem 长期运行,不会频繁 open/close USB,
  close 时的竞态在生产中几乎不触发。
- **测试**:硬件测试需单独跑(不要同进程混跑多个发送测试)。CI 跑的是离线测试
  (无硬件),不受影响。
- **smscodec 升级(路线 B)**:无影响。升级功能(收件解码 / 单段发送 / 多段发送)
  已分别单独验证通过。

---

## 八、相关文件

- `internal/usbtransport/usbtransport.go` — `ATTransport.Close()`(:146)、`Read()`(:117)
- `third_party/sms-gateway/modem/modem.go` — `Modem.Close()`(:103)、`readerLoop()`(:544)
- `internal/usbtransport/sms_hardware_test.go` — 触发崩溃的测试(发送测试 defer 顺序)
- `plans/upgrade-smscodec.md` — smscodec 升级计划(本 issue 在其硬件验证期间发现)
- gousb v1.1.3 `libusb.go:466` — 崩溃点 `libusb_cancel_transfer` CGO 调用

---

## 九、源码调研结论(2026-07-12)

webSearch 工具故障(Startpage provider 崩溃),改用源码核实 + GitHub issue 直读。

### 9.1 真正的 cancel 来源:每 200ms 轮询,不是 close

通读 gousb v1.1.3 源码后,根因比 §四 的 close-path 假设更深。

`usbtransport.Read` 用 `ReadContext(ctx200ms)`。gousb 的 `usbTransfer.wait(ctx)`
(`transfer.go:63-83`):

```go
select {
case <-ctx.Done():
    t.ctx.libusb.cancel(t.xfer)   // ← ctx 200ms 到期就 cancel
    <-t.done
case <-t.done:
}
```

**每次 200ms 读超时都调用 `libusb_cancel_transfer`**(`libusb.go:466`)。
modem 空闲时 readerLoop 每 200ms 轮询 → **~5 次 cancel/submit 循环每秒**。
这才是 WinUSB 上的真正压力源,close 只是压死骆驼的最后一根 cancel。

### 9.2 usbtransport.go 的注释是错的(已核实)

`usbtransport.go:114-116` 注释声称 ReadContext 返回 `context.DeadlineExceeded`
(有 `Timeout()`)。**实际并非如此**:`wait()` cancel 后 `data()` 返回
`TransferCancelled`(`error.go:86`,`LIBUSB_TRANSFER_CANCELLED`),它只有
`Error()`/`String()`,**没有 `Timeout()` 方法**。

后果:modem `readerLoop` 的 `isTimeout(err)` 对每 200ms 的 cancel 返回 false,
这些错误落到通用错误分支(log + continue)。功能上能跑,但每秒刷 5 条
"transfer was cancelled" 日志,且每次都是真实的 `libusb_cancel_transfer` 调用。

### 9.3 与 go.bug.st/serial 的本质差异

| | go.bug.st/serial `SetReadTimeout(200ms)` | 本项目 `ReadContext(ctx200ms)` |
|---|---|---|
| 超时机制 | OS 驱动层(COM 口超时) | gousb `libusb_cancel_transfer` |
| 是否取消 transfer | ❌ 不取消(驱动层超时) | ✅ 每次都取消(abort WinUSB pipe) |
| 200ms 空闲时 | 零成本返回 | 5 次/秒的 submit+cancel 循环 |

usbtransport 用 context-cancel 模拟串口超时,把"零成本驱动超时"变成
"每秒 5 次 pipe abort",这是崩溃温床。

### 9.4 gousb issue #137 —— CGO transfer segfault 是已知类别

gousb #137(2025-12,SIGSEGV in `_Cfunc_submit`):维护者 zagrodzki 确认 gousb 的
CGO transfer 路径会出现 segfault,且 **segfault 不可恢复**(进程必死)。虽是
`submit` 而非 `cancel` 崩溃,但属同类:libusb transfer 结构在并发/时序压力下进入
不一致态,CGO 调用解引用坏指针。

### 9.5 gousb v1.1.3 是最新版,无上游修复

`go list -m -versions` = `v1.1.0 v1.1.1 v1.1.2 v1.1.3`。v1.1.3 已是最新。
另有 `runtime.SetFinalizer`(`transfer.go:158`)在 GC 时也调 cancel()+wait()+free(),
是又一个 cancel 来源(GC 时序不可控)。

### 9.6 修订后的根因链

```
usbtransport.Read 用 ReadContext(ctx200ms)
  → gousb wait() 每 200ms 调 libusb_cancel_transfer  (~5 次/秒)
  → WinUSB 频繁 abort pipe + submit 循环
  → 偶发竞态:libusb transfer 结构不一致
  → libusb_cancel_transfer 解引用坏指针 → SIGSEGV(不可恢复)
```

close 路径只是高概率窗口(有在途 transfer 时 close),**根源是 200ms-poll 的 cancel
频率**。只读测试不崩是因运行短(cancel 循环少);发送测试时间长 + CMGS 改变读时序,
命中概率上升。

---

## 十、修订后的修复方向(基于 §九)

| 方向 | 思路 | 治本? | 说明 |
|---|---|---|---|
| **F. 长生命期读 context(首选)** | Read 用 close 时才 cancel 的长 context(而非 200ms),提交一次 transfer 阻塞到有数据或 Close。cancel 频率从 ~5/秒降到 1 次/关闭 | ✅ | 对齐 go.bug.st/serial 真实语义。需 transport.Close 主动 cancel 读 context(长 context 下读不返回,需靠 close 触发) |
| **G. ReadStream(持久多 transfer)** | gousb `InEndpoint.ReadStream()` 用 N 个持久 transfer 循环复用,close 时一次性 flush | ✅ | gousb 官方推荐持续读取方案;改造量比 F 大 |
| **C. close 时等在途读返回** | Close 先 cancel 读 context + 等读返回,再释放 iface/cfg/ctx | ⚠️ 缓解 | 减少 close 竞态,但不降运行期 cancel 频率 |
| **A. 调换测试 defer 顺序** | m.Close() 先于 tt.Close() | ❌ 仅测试 | 零成本验证辅助 |
| **D. 换 USB 库** | 评估纯 Go USB 库(避开 libusb CGO) | ✅ 代价大 | 大改 transport 层 |

**推荐 F**:把 `usbtransport.Read` 从"200ms 短轮询 + 每轮 cancel"改成
"长阻塞读 + close 触发 cancel",让 USB transport 超时语义真正对齐
go.bug.st/serial(而非用 cancel 模拟)。同时修掉 §9.2 的注释错误和 isTimeout 不匹配。

---

## 十一、凭据与佐证诚实评估(2026-07-12)

> 本节回答"有什么凭据?别人遇到过吗?"——区分**已证实的事实**、**合理的推理**、
> 和**未证实的假设**。

### 已证实的硬事实(源码 + 本机崩溃 trace)

| 事实 | 凭据 |
|---|---|
| gousb `wait()` 在 ctx 到期时调 `libusb_cancel_transfer` | `transfer.go:69-76` 源码,逐行读过 |
| 我们的 `ReadContext(ctx200ms)` → 空闲时每 200ms 一次 cancel | `usbtransport.go:125` + 上面源码,逻辑必然 |
| 崩溃栈顶是 `libusb_cancel_transfer`(CGO) | 本机实测 trace(§三),多次复现 |
| gousb v1.1.3 是最新版 | `go list -m -versions` 输出 |
| `usbtransport.go:114-116` 注释与实际返回值不符 | 源码核实:实际返回 `TransferCancelled` 非 `DeadlineExceeded` |

这些是**确定的**,不是推测。

### 别人遇到过的同类问题(外部佐证)

**找到的:**
- **gousb #137**(2025-12):SIGSEGV in `_Cfunc_submit`(不是 cancel,是 submit;且 reporter 在 Linux)。
  维护者 zagrodzki 承认"通过正确使用 libusb API 应该能避免 segfault,但并非不可能遇到——内存错误、
  CPU 错误、并发问题、libusb bug 都可能发生",且 **segfault 不可恢复**。这证实了"gousb CGO transfer
  路径存在 segfault 可能性"这一**类别**,但**不是我们这个具体场景**(cancel on Windows)的直接报告。

**没找到的(诚实说明):**
- 在 gousb issue tracker(共 8 个 open + 历史已关闭)里,**没有**人报告过
  "ReadContext 频繁超时 → cancel → Windows 崩溃"这个具体问题。gousb 的 issue 本来就少。
- 在 libusb issue tracker 里搜 "cancel transfer windows crash",**没有**直接命中。
  找到的 WinUSB 相关 issue(#1841 #1835)是 `AUTO_CLEAR_STALL` 延迟问题,**与 cancel 崩溃无关**。
- 没有找到文档或报告说"WinUSB 上频繁 `libusb_cancel_transfer` 会导致崩溃"。

**结论:外部佐证薄弱。** 我能证明"cancel 每 200ms 发生"(源码)和"崩溃在 cancel 里"(trace),
但**没有找到别人报告同样的现象**,也**没有找到"频繁 cancel 会让 WinUSB 崩溃"的权威说法**。

### 推理链的薄弱环节(诚实)

我的根因假设是:
> 200ms 频繁 cancel → WinUSB abort pipe 竞态 → transfer 结构不一致 → segfault

**这个链条有一个未填补的漏洞**:如果"频繁 cancel"是根因,那 3 个只读测试(也每 200ms cancel,
跑了几十次 cancel 循环)为什么没崩?发送测试才崩。这说明**单纯 cancel 频率可能不是充分条件**,
可能还需要别的触发因素(比如:发送时 read+write transfer 并发、close 时在途 transfer 被
finalizer cancel、或某个特定时序)。

换句话说:**我确定了崩溃发生在 cancel 里,但"为什么发送场景才触发"还没完全解释清。**
把 cancel 频率从 ~5/秒降到 1 次/关闭(方向 F)**可能**修好,但**这是假设,没实测验证过**。

### 建议的下一步:用实验补齐凭据

在投入实现方向 F 之前,先做**判别性实验**确认根因:

1. **统计 cancel 频率与崩溃的相关性**:把 `readPollInterval` 从 200ms 改成 2000ms(10× 降频),
   重跑"两个发送测试同进程"。若不崩了 → 强烈支持"cancel 频率是主因"。若还崩 → 频率不是主因,
   得看 read+write 并发或 close 时序。
2. **隔离 read vs write**:发送时用单独 goroutine 不做 read,看是否还崩 → 判断是否 read+write 并发导致。
3. **查 libusb debug 日志**:`gousb.Context` 支持 `Debug` 回调,开启后看崩溃前最后一次 cancel
   的 transfer 状态,定位是 finalizer cancel 还是 wait cancel 还是 close cancel。

在 §十二 判别实验前,方向 F 是"最可能的修复";**§十二 实验已证实 10× 降频消除崩溃**,方向 F(降至发送期间 0 次 cancel)升级为"实验支撑的有效修复"。

---

## 十二、判别实验结果(2026-07-12,已实测)

**实验**:把 `readPollInterval` 从 200ms 改成 2000ms(cancel 频率降 10×),重跑
"两个发送测试同进程"(原本必崩的场景)。

| 配置 | logged cancel 次数 | `signal arrived` | 结果 |
---|---|---|---|
| **200ms(原值,对照组)** | 2 | ✅ 1 次(崩) | `FAIL`(TestHardwareSMSSendMultiPart 没跑起来) |
| **2000ms(10× 降频,实验组)** | 0 | ❌ 0 | 两测试都 `PASS`(2.60s + 5.57s) |

### 结论

**cancel 频率/时序是关键变量——10× 降频消除了崩溃。** 这从"源码推理"升级为
"实验证实":方向 F(降低 cancel 频率)是有效的修复方向。

### 精化后的机制推测(非纯频率)

注意:200ms 对照组只 logged 2 次 cancel 就崩了,不是"几百次 cancel 累积"才崩。
这说明**不是 cancel 总量问题,而是某个 cancel 撞上特定时序窗口**:

- 发送流程(`>` → 写 PDU+CtrlZ → 等 OK)中,readerLoop 在等 OK 时若 200ms 超时,
  会 cancel 一次 IN transfer;此刻 OUT 端点刚写过 PDU/CtrlZ,可能存在 read-cancel
  与 write 完成/提交的竞态。
- 2000ms 时,OK 在超时前返回,等 OK 的读**不触发 cancel**,避开了那个窗口。

所以更准确的表述:**发送期间一次 read-side cancel 与近期 write transfer 的并发,
是触发 segfault 的条件。** 频率本身不直接致崩,但频率越低,撞上并发窗口的概率越低。

### 对方向 F 的验证意义

方向 F(长生命期读,close 时才 cancel)会把发送期间的 read-cancel 降到 **0**
(读到数据就返回,不 cancel;只有 close 才 cancel 一次)。实验已证明"发送期间
无 cancel = 不崩",所以 **方向 F 应当有效**,且比单纯加大 poll interval 更治本
(完全不依赖频率,而是消除发送期间的 cancel)。

> **已完成**(§十三):方向 F + readLine 改造已实施,全部硬件测试同进程稳定通过。

---

## 十三、已修复(2026-07-12,方向 F + readLine 改造)

**状态:已修复,全部硬件测试(6 个)同进程稳定通过,连续多轮无崩溃。**

### 实施的改动

**1. transport 层方向 F(长生命期读)** — `internal/usbtransport/usbtransport.go`
- `ATTransport` 新增 `readCtx`/`readCancel` 字段(`context.WithCancel(Background)`,无超时)
- `Read` 从 `ReadContext(ctx200ms)` 改为 `ReadContext(t.readCtx)`——阻塞到有数据或 Close,**运行期 0 次 cancel**
- `Close` 先 `readCancel()`(让在途 Read 返回)再释放 USB handles——避免"释放 USB 时有在途 transfer"
- 删除 `readPollInterval` 常量(不再需要短轮询)

**2. readLine 改造(方向 F 的必要配套)** — `third_party/sms-gateway/modem/modem.go`
- 原 `readLine` 用 `bufio.ReadString('\n')`,靠"读超时返回错误"来检测 CMGS `>` 提示符(`>` 不以 `\n` 结尾)
- 方向 F 下 Read 不再超时 → `ReadString` 永远等不到 `\n` 也收不到超时错误 → `>` 卡死
- 改为**逐字节 `r.Read`**:每读一个字节就检查是否 `\n`(完整行)或 buffer 已是 `>`(提示符),不再依赖超时
- 这让 readLine **同时对串口(短超时)和 USB(长阻塞)工作**

### 验证结果

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 两个发送测试同进程 | ❌ 必崩(libusb_cancel_transfer segfault) | ✅ PASS(0 crash, 0 cancel) |
| 全部 6 硬件 + 10 离线测试同进程 | ❌ 崩 | ✅ 全 PASS(2.6s) |
| 连续多轮重跑 | ❌ 崩 | ✅ 稳定 |

### 为什么方向 F 有效(机制确认)

实验(§十二)证明:发送期间一次 read-side cancel 撞上近期 write transfer 的并发是触发条件。
方向 F 让发送期间的 read-cancel 降到 **0**(读到数据就返回,只有 Close 才 cancel 一次),
从根本上消除了那个并发窗口。readLine 改造确保长阻塞读下 `>` 提示符仍能被检测。
