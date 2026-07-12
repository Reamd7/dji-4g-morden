# 子计划 04 — mock 单测(QMITransport 离线测试)

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> 让 QMITransport 的 Read/Write/Close/并发逻辑可离线测试(CI 友好,无硬件)。

---

## 目标

用 mock(`testutil.ScriptPort`)覆盖 QMITransport 的传输逻辑 + 并发安全,
满足 AGENTS.md 覆盖率目标(Transport 适配层 ≥ 80%)。
**不依赖真实 USB**,全部内存 mock。

## 依赖 / 前置

- **子计划 02/03**:QMITransport 实现(含 writeMu/readCtx)
- `internal/testutil/`(ScriptPort mock io.ReadWriteCloser,阶段 1 已有)
- 阶段 1 `usbtransport_test.go`(模式参考)

## 设计:接口抽象 + 适配器

QMITransport 持有 `*gousb.InEndpoint`/`*OutEndpoint`(具体类型),无法直接 mock。
把 I/O 抽成接口(同 usbtransport):

```go
type endpointReader interface {
	ReadContext(ctx context.Context, buf []byte) (int, error)
}
type endpointWriter interface {
	Write(buf []byte) (int, error)
}
```

- **生产**:gousb 的 `*InEndpoint`/`*OutEndpoint` 天然满足
- **测试**:用 `testutil.ScriptPort` 实现的适配器注入

**scriptReader 适配器**(ScriptPort.Read 是阻塞语义,适配成 ReadContext):
```go
type scriptReader struct{ port *testutil.ScriptPort }
func (r *scriptReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
	type result struct{ n int; err error }
	ch := make(chan result, 1)
	go func() { n, err := r.port.Read(buf); ch <- result{n, err} }()
	select {
	case res := <-ch:  return res.n, res.err
	case <-ctx.Done(): return 0, ctx.Err()  // 模拟 gousb 超时/cancel
	}
}
```

## 测试用例

| 测试 | 验证点 |
|---|---|
| `TestReadReturnsData` | 有数据时 Read 返回 QMUX 字节 |
| `TestReadBlocksUntilClose` | 无数据时 Read 阻塞,Close(readCancel)后返回 Canceled |
| `TestWriteFull` | Write 全量写出,记录到 ScriptPort.Written() |
| `TestCloseTerminatesRead` | Close 让在途 Read 返回(子计划 03 的 readCancel 语义) |
| `TestConcurrentReadWrite` | `-race` 下并发 Read+Write 安全 |
| `TestCloseDuringWrite` | **核心**:Close 持 writeMu 时,Write 阻塞→Close readCancel→Unlock→Write 返回。验证无死锁、无 cancel 撞 write |
| `TestQMITransportAsQmiTransport` | 编译期断言 `var _ qmi.Transport = (*QMITransport)(nil)` |
| `TestQMUXFrameRoundTrip` | 注入 mock transport 到 `qmi.NewClientFromTransport`,发 SYNC 收 SYNC_RESP(协议层集成) |

## 步骤

1. `qmitransport_test.go`:endpointReader/Writer 接口 + scriptReader/Writer 适配器
2. 按上表写测试(手写断言,不用 testify,对齐上游风格)
3. `TestQMUXFrameRoundTrip`:mock transport 预载 SYNC_RESP → NewClientFromTransport → client.Sync() → 断言成功
4. 跑 `make test-race`(`-race` 硬性要求)

## 交付物 / 完成标志

- [ ] `qmitransport_test.go` 覆盖上表用例
- [ ] `go test -race ./internal/qmitransport/` 通过
- [ ] 覆盖率 ≥ 80%(Transport 适配层目标)
- [ ] `TestCloseDuringWrite` 验证 writeMu Close 时序无死锁(子计划 03 的核心)

## 风险

| 风险 | 缓解 |
|---|---|
| ScriptPort 不能模拟 cancel 时序 | scriptReader 的 select ctx.Done 模拟;TestCloseDuringWrite 用 controlled timing |
| TestQMUXFrameRoundTrip 需要 QMUX 帧编解码 | 用 quectel-qmi-go 的 Packet.Marshal/UnmarshalPacket 构造 SYNC_REQ/RESP |
| 模型 B 的 control transfer mock | 模型 B 若被选(子计划 00),适配器要 mock dev.Control;本计划先按模型 A |
