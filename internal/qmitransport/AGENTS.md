# internal/qmitransport/ — QMI USB transport (model B: EP0 control encapsulation)

> 实现 `plans/stage2/02-qmitransport-impl.md`。
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
  select { notifyCh → dev.Control(GET) | timer(deadline) → errTimeout | readCtx.Done() → closed }
```

- **零 cancel_transfer**:readCtx 只在 Close 时 cancel 一次,正常运行期不 cancel
- **deadline 纯 Go 实现**:用 timer + channel select,不在 USB 层设超时
- `errTimeout` 实现 `Timeout() bool` → `os.IsTimeout` 返回 true → readLoop 正常轮询 closeCh

### 与 AT transport(usbtransport.go)的对比

| | ATTransport (MI_02) | QMITransport (MI_04) |
|---|---|---|
| 模型 | bulk IN/OUT 长阻塞读 | EP0 控制封装(interrupt + GET) |
| Read | `in.ReadContext(ctx, buf)` 直接返回字节 | `select notifyCh → dev.Control(GET)` 两步 |
| Write | `out.Write(buf)` bulk OUT | `dev.Control(SEND)` |
| DTR | 不需要 | **必须**(否则模组不响应) |
| cancel 风险 | 低(只有 bulk IN transfer) | 低(只有 interrupt IN transfer) |

## 接口

```go
// 满足 qmi.Transport (编译期断言)
func Open() (*QMITransport, error)                              // VID 0x2C7C, PID 0x0125
func OpenWithVIDPID(vid, pid uint16) (*QMITransport, error)
func (t *QMITransport) Read(buf []byte) (int, error)           // 阻塞,等 interrupt + GET
func (t *QMITransport) Write(buf []byte) (int, error)          // SEND_ENCAPSULATED
func (t *QMITransport) Close() error                            // cancel goroutine → 清 DTR → 释放
func (t *QMITransport) SetReadDeadline(t time.Time) error       // 存 deadline 给 Read timer
```

## 可测性

`controlDevice` 和 `interruptReader` 接口抽象了 gousb 的 `*Device.Control` 和
`*InEndpoint.ReadContext`,mock 测试不需要真 USB(子计划 04)。

gousb v1.1.3 **没有 `Device.ControlContext`** — 控制传输无 context cancel。
但 GET 只在 interrupt 通知后才发(数据已就绪),设备立即响应,不会长阻塞。

## 测试

| 文件 | 类型 | 命令 |
|---|---|---|
| `qmitransport_hardware_test.go` | 硬件(build tag: hardware) | `go test -tags=hardware ./internal/qmitransport/` |

烟测:`TestHardwareSyncExchange` 发 SYNC → 收 SYNC_RESP,验证完整 model B 链路。

## 下一步

- **子计划 03**:Read/Close 并发安全审计(当前已遵循方向 F,但需 formal review)
- **子计划 04**:mock 单测(mock controlDevice + interruptReader)
- **子计划 05**:接入 qmi.NewClientFromTransport + WDA/WDS 拨号
