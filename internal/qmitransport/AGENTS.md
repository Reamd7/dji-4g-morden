# internal/qmitransport/ — QMI USB transport (model B: EP0 control encapsulation)

> 实现 `plans/stage2/02-qmitransport-impl.md` + `03-read-close-concurrency.md`。
> Phase 0 探针结果见 `plans/stage2/00-phase0-transport-probe.md`。

## 作用

把 DJI 百望模组的 MI_04(QMI 接口)包装成 `qmi.Transport` 接口,
注入 `qmi.NewClientFromTransport()`,让 QMI 协议栈完全在用户态跑。

## 核心设计

### 传输模型 = B(EP0 控制封装 + DTR)

模型 A(bulk)不通(Phase 0 实测)。只有模型 B 工作:

| 方向 | USB 操作 | gousb 调用 |
|---|---|---|
| **DTR**(前提) | CDC SetControlLineState | `dev.Control(0x21, 0x22, 0x0001, 4, nil)` |
| **TX** | SEND_ENCAPSULATED_COMMAND | `dev.Control(0x21, 0x00, 0x0000, 4, frame)` |
| **RX 通知** | interrupt EP 0x89 RESPONSE_AVAILABLE | `intrIn.ReadContext(readCtx, buf)` |
| **RX 数据** | GET_ENCAPSULATED_RESPONSE | `dev.Control(0xA1, 0x01, 0x0000, 4, buf)` |

### 中断 goroutine(方向 F,issue/001 安全)

```
interruptLoop:
  长阻塞 intrIn.ReadContext(readCtx, 8B)
    ↓ 收到 RESPONSE_AVAILABLE
  signal notifyCh (buffered=1)
    ↓
Read():
  select { notifyCh → ioMu → dev.Control(GET) | timer(deadline) → errTimeout | readCtx.Done() → errClosed }
```

- **零 cancel_transfer**:readCtx 只在 Close 时 cancel 一次,正常运行期不 cancel
- **deadline 纯 Go 实现**:用 timer + channel select,不在 USB 层设超时
- `errTimeout` 实现 `Timeout() bool` → `os.IsTimeout` 返回 true → readLoop 正常轮询 closeCh

### 并发安全(子计划 03,ioMu 序列化)

**核心问题**:QMI client 的 Close 顺序是 `close(closeCh) → conn.Close() → wg.Wait()`
(client.go:335-337)。`conn.Close()` 在 `wg.Wait()` **之前**执行,意味着 writerLoop
和 readLoop 可能还在跑。gousb v1.1.3 无 `Device.ControlContext` — 控制传输无法被
context cancel。如果 Close 释放 USB handle 时有 in-flight `dev.Control`,segfault。

**修复**:单个 `ioMu` 互斥锁序列化**所有** `dev.Control` 调用 + handle 释放:

```
Read:    select notifyCh → ioMu.Lock → closed? → ctrl.Control(GET) → Unlock
Write:   ioMu.Lock → closed? → ctrl.Control(SEND) → Unlock
Close:   ioMu.Lock → closed=true → readCancel → <-intrDone → DTR clear → release handles → Unlock
```

Close 持有 ioMu 的整个期间,任何 in-flight Read/Write 要么已完成(已 Unlock),要么
在等 ioMu(拿到后看到 closed=true 就返回)。中断 goroutine 不用 ioMu(只做
`intrIn.ReadContext` + channel send),所以 `readCancel` + `<-intrDone` 不会死锁。

**硬件压测**:10 轮并发 Read+Write+Close,0 segfault(issue/001 窗口已关闭)。

### 与 AT transport(usbtransport.go)的对比

| | ATTransport (MI_02) | QMITransport (MI_04) |
|---|---|---|
| 模型 | bulk IN/OUT 长阻塞读 | EP0 控制封装(interrupt + GET) |
| Read | `in.ReadContext(ctx, buf)` 直接返回字节 | `select notifyCh → ioMu → dev.Control(GET)` |
| Write | `out.Write(buf)` bulk OUT | `ioMu → dev.Control(SEND)` |
| Close 并发 | 无 writerLoop(AT 单线程写) | **有 writerLoop**(QMI 独立 writerLoop goroutine) |
| 并发保护 | `mu`(closed 标记) | **`ioMu`**(序列化所有 dev.Control + handle 释放) |
| DTR | 不需要 | **必须**(否则模组不响应) |

## 接口

```go
// 满足 qmi.Transport (编译期断言)
func Open() (*QMITransport, error)                              // VID 0x2C7C, PID 0x0125
func OpenWithVIDPID(vid, pid uint16) (*QMITransport, error)
func (t *QMITransport) Read(buf []byte) (int, error)           // 阻塞,等 interrupt + GET
func (t *QMITransport) Write(buf []byte) (int, error)          // SEND_ENCAPSULATED
func (t *QMITransport) Close() error                            // ioMu → closed → cancel → DTR → release
func (t *QMITransport) SetReadDeadline(t time.Time) error       // 存 deadline 给 Read timer
```

## 可测性

`controlDevice` 和 `interruptReader` 接口抽象了 gousb 的 `*Device.Control` 和
`*InEndpoint.ReadContext`,mock 测试不需要真 USB。

gousb v1.1.3 **没有 `Device.ControlContext`** — 控制传输无 context cancel。
但 GET 只在 interrupt 通知后才发(数据已就绪),设备立即响应,不会长阻塞。
并发安全靠 `ioMu` 互斥锁(不是 context cancel)。

## 测试

| 文件 | 类型 | 命令 |
|---|---|---|
| `qmitransport_test.go` | 离线 mock(11 测试) | `go test -race ./internal/qmitransport/` |
| `qmitransport_hardware_test.go` | 硬件(build tag: hardware) | `go test -tags=hardware ./internal/qmitransport/` |

离线测试覆盖率:Transport 适配层平均 ~93%(Read 95.5%/Write 100%/SetReadDeadline 100%/errTimeout 100%/interruptLoop 90%/Close 63%)。总 54.9% 因 Open/openInternal 硬件代码 0%(USB 物理层不计)。

离线测试清单:
- `TestQMUXFrameRoundTrip` — mock 注入 qmi.NewClientFromTransport,SYNC 往返(reactiveControlDevice)
- `TestConcurrentReadWriteClose` — 50 轮并发 Read+Write+Close(-race 无告警)
- `TestReadBlocksUntilClose` — Read 阻塞 → Close 解除
- `TestReadGetsResponse` / `TestReadTimeout` / `TestReadGETError` — Read 三路径
- `TestWriteFull` / `TestWriteAfterClose` / `TestWriteSENDError` — Write 三路径
- `TestReadAfterClose` / `TestErrTimeoutInterface` — 边界

硬件烟测:SYNC → SYNC_RESP + 10 轮并发 Close 压测(0 segfault)。

## 下一步

- **子计划 03**:✅ 完成(ioMu 并发安全 + 硬件压测)
- **子计划 04**:✅ 完成(11 个离线 mock 测试,覆盖率 ~93%)
- **子计划 05**:接入 qmi.NewClientFromTransport + WDA/WDS 拨号
