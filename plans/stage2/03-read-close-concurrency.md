# 子计划 03 — Read 超时 + Close 并发安全(issue/001 防护)

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> **QMI 独有风险**:quectel-qmi-go 有独立 writerLoop,方向 F 的 Close cancel 会撞 write → segfault。
> 本计划专门处理这个,是子计划 02 的并发安全加固。

---

## 目标

让 QMITransport 的 Read/Close 在 QMI 的并发模型(readLoop + writerLoop 独立 goroutine)下安全,
不触发 issue/001 的 `libusb_cancel_transfer` segfault。

## 依赖 / 前置

- **子计划 02**:QMITransport 基本实现(能收发)
- `issue/001-gousb-close-transfer-cancel-crash.md`(崩溃判别实验)
- 阶段 1 `internal/usbtransport/`(方向 F 实现,但 AT 场景无 writerLoop)

## 背景:issue/001 真相

**崩溃条件**(issue/001:364):"发送期间 read-side cancel 撞近期 write transfer 并发"触发 segfault。
不是纯频率,是 **cancel 与 write 并发的窗口**。

- **方向 F**(阶段 1 AT transport,usbtransport.go:5-9):Read 用长生命期 context(`WithCancel` 无超时),
  运行期 0 次 cancel,只有 Close cancel 一次。**AT 场景安全**——因为 Write 在调用者线程,Close 时无并发 write。
- **2000ms 长 deadline**:issue/001 判别实验的缓解方案(降频),非根治。
- **方向 F 根治**:运行期 0 cancel,比 2000ms 更治本。

**QMI 的问题**:quectel-qmi-go 有**独立 writerLoop goroutine**(client.go:639-654),
Close 时它可能正在 `writeAll` → `bulkOut.Write`。client.go Close 顺序(client.go:399-409):
`close(closeCh)` → `conn.Close()`(→ transport.Close → readCancel)→ `wg.Wait()`。
**readCancel 在 writerLoop 退出之前执行**,撞 write = issue/001 崩溃条件。

## 设计:SetReadDeadline=no-op + 方向 F + writeMu Close 时序

### Read(方向 F,长阻塞)

```go
func (t *QMITransport) Read(buf []byte) (int, error) {
	return t.bulkIn.ReadContext(t.readCtx, buf)  // readCtx = WithCancel(Background), 无超时
}
```
- 运行期 0 次 cancel(读到数据就返回)
- SetReadDeadline = no-op(readLoop client.go:549 调它但不生效)

**退出路径分析**:Close 时 readCancel → Read 返回 `context.Canceled`。
readLoop(client.go:553)`os.IsTimeout(context.Canceled)` = **false**(Canceled 不实现 net.Error)→
不走 continue,走 client.go:556-560 `select <-closeCh: return`(closeCh 已关)正常退出。✓

### Close(writeMu 协调,核心修正)

```go
func (t *QMITransport) Close() error {
	t.writeMu.Lock()         // ① 等在途 Write 完成(writerLoop 持有 writeMu 时阻塞到这里)
	t.readCancel()           // ② 此时无并发 write,安全 cancel readLoop 的 Read
	// ③ 释放 USB handles(iface → cfg → dev)
	t.writeMu.Unlock()
	return ...
}

func (t *QMITransport) Write(buf []byte) (int, error) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	return t.bulkOut.Write(buf)
}
```

**为什么安全**:Close 的 readCancel(②)在 writeMu.Lock(①)之后执行,
此时 writerLoop 的 Write 要么已完成(已 Unlock)、要么在等 writeMu(被 Close 持有)。
无论如何,readCancel 时没有正在执行的 bulkOut.Write → 不撞 → 不崩。

## 步骤

1. 在子计划 02 的 QMITransport 上加 `readCtx`/`readCancel`/`writeMu`
2. Read 改长阻塞(readCtx);Write 加 writeMu;Close 用 writeMu 协调
3. SetReadDeadline 返回 nil(no-op)
4. 硬件压测:并发 SendRequest(触发 writerLoop)+ Close,反复重跑,确认不崩

## 交付物 / 完成标志

- [x] ~~Read 长阻塞(方向 F),运行期 0 cancel~~ → 模型 B:中断 goroutine 长阻塞(零 cancel),Read 用 select+timer
- [x] ~~Close 用 writeMu 协调~~ → 升级为 **`ioMu`**(序列化所有 dev.Control,不只 Write)
- [x] `go test -race` 通过(50 轮并发 Read+Write+Close)
- [x] 硬件压测:10 轮并发 Read+Write+Close,0 segfault(issue/001 窗口已关闭)

## 实现说明(模型 B 适配)

计划原文针对模型 A(bulk)。模型 B 的并发风险不同但同类:
gousb v1.1.3 无 `Device.ControlContext` → 控制传输无法 cancel →
Close 必须用互斥锁等所有 in-flight `dev.Control` 完成后才能释放 handle。
用单个 `ioMu` 替代计划的 `writeMu`,保护范围更广(Read GET + Write SEND + Close DTR/释放)。

## 风险

| 风险 | 缓解 |
|---|---|
| writeMu.Lock 在 Close 时阻塞 writerLoop → 死锁? | writerLoop 不会反过来等 Close(它只等 writeCh 或 closeCh)。但实现时验证:Close 持 writeMu 期间 writerLoop 卡在 Lock,Close 释放 USB 后 Unlock,writerLoop 下次循环看到 closeCh 退出。需确认无循环等待 |
| 模型 B(EP0)无 bulk IN cancel,本计划不适用 | 本计划针对模型 A。模型 B 的 Read 是 control transfer,无 issue/001 风险,Close 策略不同(见子计划 02 模型 B) |
| os.IsTimeout(Canceled)=false 假设 | 标准库语义确认(Canceled 非 net.Error);硬件测试验证 readLoop 正常退出 |
