# 子计划 06 — 硬件集成测试

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> 用 build tag `hardware` 隔离的设备集成测试,验证完整 QMI 链路 + 拨号。
> 默认 `go test ./...` 不跑(无设备环境不红),`-tags=hardware` 才跑。

---

## 目标

在真实 EC25 + WinUSB 上验证:transport 能收发 QMI、Client 能工作、拨号能成、并发安全(issue/001 不重现)。

## 依赖 / 前置

- **子计划 05**:拨号流程实现(cmd/qmidial 或 test)
- 真实 DJI 百望刷 EC25 PID 0x0125 + WinUSB on MI_04 + SIM(<运营商>,PS attach)
- 阶段 1 的 hardware test 模式参考(`sms_hardware_test.go`)

## 测试用例(`internal/qmitransport/qmi_hardware_test.go`)

```go
//go:build hardware

package qmitransport
```

| 测试 | 验证点 |
|---|---|
| `TestQMITransportOpen` | claim MI_04,打开 endpoint,确认无错误 |
| `TestQMICLTSync` | NewClientFromTransport 内部 SyncOnOpen 成功(CTL SYNC_RESP) |
| `TestQMIGetClientID` | AllocateClientID(WDS) → 拿到非零 client ID |
| `TestWDSStartNetwork` | WDA SetDataFormat + WDS StartNetworkInterface → 非零 PDH + GetRuntimeSettings 有效 IP |
| `TestConcurrentCloseNoCrash` | **核心**:并发 SendRequest + Close 反复重跑,0 segfault(子计划 03 验证) |

## 运行

```bash
# 硬件测试(需 EC25 + WinUSB on MI_04 + SIM)
DJI_TEST_APN=cmnet mise exec -- go test -tags=hardware -v -run TestHardwareQMI ./internal/qmitransport/

# 竞态检测(并发代码硬性要求)
mise exec -- go test -tags=hardware -race ./internal/qmitransport/
```

## 步骤

1. `qmi_hardware_test.go`(`//go:build hardware`)
2. 按上表实现,用 `t.Skip` 处理无设备/无 SIM 场景
3. `TestWDSStartNetwork` 用环境变量 `DJI_TEST_APN`(默认 cmnet)
4. `TestConcurrentCloseNoCrash`:goroutine 持续 SendRequest,主 goroutine Close,反复 10 轮

## 交付物 / 完成标志

- [ ] `qmi_hardware_test.go` 覆盖上表用例
- [ ] `-tags=hardware` 全部 PASS(含 -race)
- [ ] `TestConcurrentCloseNoCrash` 0 segfault(issue/001 在 QMI 场景不重现)
- [ ] 拨号拿到真实 IP(打印到测试日志)

## 风险

| 风险 | 缓解 |
|---|---|
| 测试改变模组状态(拨号/PDP 激活) | 测试后 StopNetworkInterface 清理;TestWDSStartNetwork 用 t.Cleanup |
| SIM 未 PS attach | 测试前 Skip 检查;或先跑阶段 1 的 AT 确认 CGATT/CREG |
| WinUSB 装错 Iface | 子计划 00 前置已确认;hardware test 失败先查 Zadig |
