# 子计划 02 — QMITransport 实现(按 Phase 0 结果)

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> 实现 `internal/qmitransport/`,满足 `qmi.Transport` 接口。**结构体取决于子计划 00 的结果。**

---

## 目标

实现 USB bulk/EP0 → `qmi.Transport`(Read/Write/Close/SetReadDeadline)适配层,
让 quectel-qmi-go 的 readLoop/writerLoop 能通过它收发 QMUX 帧。

## 依赖 / 前置

- **子计划 00**(Phase 0):传输模型确定(A 或 B),决定结构体分支
- **子计划 01**:`qmi.Transport` 接口已导出
- 阶段 1 的 `internal/usbtransport/`(模式参考:Open/Close/长阻塞读)

## 关键事实(已核实)

1. **QMUX 分帧由 quectel-qmi-go 负责**。readLoop(client.go:536-637)用 16KB buf 读原始字节 →
   累积 pending → 找 `0x01` 同步字节 → 读 `pending[1:3]` LE16 长度算 `frameLen=1+len` → 切帧 → UnmarshalPacket。
   **transport.Read 只管吐裸 QMUX 字节,不分帧**。

2. **transport 接口**:`Read([]byte)(int,error)` / `Write([]byte)(int,error)` / `Close()error` / `SetReadDeadline(time.Time)error`。

## 实现(模型 A — bulk,倾向)

若 Phase 0 选 A,与 `usbtransport` 对称:

```go
type QMITransport struct {
	ctx      *gousb.Context
	dev      *gousb.Device
	cfg      *gousb.Config
	iface    *gousb.Interface
	bulkOut  *gousb.OutEndpoint  // EP 0x05
	bulkIn   *gousb.InEndpoint   // EP 0x88
	// EP 0x89 interrupt 忽略(模型 A 不用)
	readCtx    context.Context    // 长生命期(子计划 03 详述)
	readCancel context.CancelFunc
	writeMu    sync.Mutex         // 串行化 Write + Close 时序(子计划 03 详述)
	closed     bool
}

func Open(vid, pid uint16, ifaceNum, epOut, epIn int) (*QMITransport, error)
// Open(0x2C7C, 0x0125, 4, 0x05, 0x88) — claim MI_04, open 2 bulk endpoints

func (t *QMITransport) Read(buf []byte) (int, error)
// bulkIn.ReadContext(t.readCtx) — 长阻塞,运行期 0 cancel(方向 F)

func (t *QMITransport) Write(buf []byte) (int, error)
// t.writeMu.Lock(); defer Unlock(); t.bulkOut.Write(buf)

func (t *QMITransport) SetReadDeadline(time.Time) error { return nil }  // no-op(子计划 03 详述)
```

## 实现(模型 B — EP0 封装,若 Phase 0 选 B)

完全不同的 API,无 bulk 长阻塞读:

```go
type QMITransport struct {
	dev   *gousb.Device
	iface int  // 4
	intr  *gousb.InEndpoint  // EP 0x89(必需!RESPONSE_AVAILABLE 通知)
}
// Write: dev.Control(0x21, 0x00, 0, 4, payload)   SEND_ENCAPSULATED_COMMAND
// Read:  等 intr.ReadContext(8字节通知) → dev.ControlContext(0xA1,0x01,0,4,buf) GET_ENCAPSULATED_RESPONSE
//        封装成"阻塞 Read 返回 QMUX 字节流"语义给 readLoop
```

模型 B 的 Read 要把"interrupt 通知 + control GET"封装成 readLoop 期望的"阻塞 Read 返回字节"。
issue/001 的 cancel 崩溃在模型 B 下不适用(无 bulk IN transfer 要 cancel,只有 control transfer)。

## 步骤

1. 按 Phase 0 结果选结构体分支(A 或 B)
2. 实现 Open/Read/Write/Close/SetReadDeadline
3. **Read/Close 的并发安全单独由子计划 03 处理**(方向 F + writeMu),本计划先实现"能收发"
4. 编译期接口断言:`var _ qmi.Transport = (*QMITransport)(nil)`

## 交付物 / 完成标志

- [ ] `internal/qmitransport/qmitransport.go` 实现(模型 A 或 B)
- [ ] 满足 `qmi.Transport` 接口(编译期断言)
- [ ] Open 能 claim MI_04 + 打开 endpoint
- [ ] Read/Write 能收发裸 QMUX 字节(硬件烟测:发 SYNC 收 SYNC_RESP)
- [ ] **并发安全(Close 时序)移交子计划 03**

## 风险

| 风险 | 缓解 |
|---|---|
| 模型 B 的 Read 封装复杂(interrupt + GET 协调) | Phase 0 若选 B,单独设计 Read 状态机;模型 B 无 issue/001 风险 |
| 接口断言失败(SetReadDeadline 签名) | 编译期 `var _ qmi.Transport` 断言捕获 |
| 并发安全未处理 | 本计划只做"能收发";Close 并发安全见子计划 03 |
