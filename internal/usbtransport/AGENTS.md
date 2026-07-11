# internal/usbtransport/ — USB bulk endpoint → io.ReadWriteCloser

本目录是**阶段 1 的核心交付物**:把 DJI 百望模组 MI_02(AT 命令口)的 USB bulk endpoint 包装成 `io.ReadWriteCloser`,喂给 `third_party/sms-gateway/modem.NewFromIO`,实现纯用户态 AT 通道。

## 包的职责

```
gousb (libusb/WinUSB)
    ↓ USB bulk transfer
ATTransport (本包)
    ↓ io.ReadWriteCloser + 200ms 短轮询 Read 语义
modem.NewFromIO (third_party/sms-gateway/modem)
    ↓ AT 命令收发 + PDU 编解码
短信收发 / 设备信息查询
```

本包**只做 transport 适配**,不含任何 AT 协议逻辑(那部分在 modem 包)。

## 文件

| 文件 | 作用 |
|---|---|
| `usbtransport.go` | `ATTransport` 结构体 + `Open`/`Read`/`Write`/`Close`。生产代码。 |
| `usbtransport_test.go` | mock 单测(无硬件)。用 `testutil.ScriptPort` + `scriptReader` 适配器注入假 endpoint,测 Read 超时/Write/Close/并发 + modem.NewFromIO 端到端集成。跑在 `make test-race`。 |
| `usbtransport_hardware_test.go` | AT 通路硬件集成测试(build tag `hardware`)。`TestHardwareATPing` + `TestHardwareModemInitializeAndCSQ`。 |
| `sms_hardware_test.go` | 短信收发硬件集成测试(build tag `hardware`)。设备信息查询 / ListStored+DecodeDeliver / Send(需 `DJI_TEST_SMS_RECIPIENT`)。 |

## 关键设计

### Read 的 200ms 短超时轮询(核心,与串口 deadline 链不同)

`Read` 每次 `context.WithTimeout(200ms)` 包 `in.ReadContext`:
- **无数据时**:200ms 后返回 `context.DeadlineExceeded`
- `context.DeadlineExceeded` **实现了 `Timeout() bool`**(返回 true)
- modem.readerLoop 的 `isTimeout`(`modem.go:652`)用 `interface{ Timeout() bool }` 断言识别它 → 当 "nothing to do" → continue
- 这样 readerLoop 每 200ms 唤醒一次,能周期性检查 `m.closed` channel 响应 Close

**为什么不走 deadline 链**:sms_gateway/modem 包根本**没有 deadline 链**(不像 uicc-go/at 的 `readDeadliner`)。超时由 readerLoop 的 200ms 短轮询控制。这是选 sms_gateway 而非 uicc-go 的副作用(见 `docs/07`)。

**gousb 超时返回值事实**:文档说返回 `TransferCancelled`,但 `cmd/attest` 实测返回 `context.DeadlineExceeded`。后者实现 `Timeout()`,所以链路通。

### endpointReader / endpointWriter 接口抽象(可测性)

gousb 的 `*InEndpoint`/`*OutEndpoint` 是具体结构体,无法 mock。本包把 I/O 抽成两个接口:
```go
type endpointReader interface { ReadContext(ctx context.Context, buf []byte) (int, error) }
type endpointWriter interface { Write(buf []byte) (int, error) }
```
- **生产**:gousb 的 `*InEndpoint`/`*OutEndpoint` 天然满足,编译期断言 `_ endpointReader = (*gousb.InEndpoint)(nil)`
- **测试**:`scriptReader`/`scriptWriter` 适配 `testutil.ScriptPort`,让 Read 的超时逻辑完全离线测试

### Open 的资源管理

`Open` 创建 `gousb.Context` → `Device` → `Config` → `Interface` → endpoints,任何一步失败都清理已申请的资源。`Close` 反序释放(ctx 最后关)。`ATTransport` 持有 `*gousb.Context` 字段(早版本漏了,已修)。

## 跑测试

```bash
# mock 单测(CI 友好,无硬件,-race 硬性要求)
make test-race

# 硬件集成测试(需 EC25 PID 0125 + WinUSB on MI_02)
make test-hardware
mise exec -- go test -tags=hardware -v ./internal/usbtransport/

# 发短信测试(需真实号码,会花钱)
DJI_TEST_SMS_RECIPIENT="+8613xxxxxxxxx" mise exec -- go test -tags=hardware -v -run TestHardwareSMSSend ./internal/usbtransport/
```

## 实测确认的 endpoint 地址(AGENTS.md 主文件有完整表)

MI_02 AT 口:`epOut=0x03`、`epIn=0x84`。硬编码在 hardware test 的 `hwEpOut`/`hwEpIn` 常量。

## 不要在这里做的事

- ❌ 不要加 AT 命令逻辑(属于 modem 包)
- ❌ 不要加 QMI 通道(阶段 2,会是新的 `internal/qmitransport/` 或类似)
- ❌ 不要加 deadline 链(modem 包不需要,见上)

## 下一步演进

- 阶段 2 会参考本包的接口抽象模式,写 MI_04 的 QMI transport(EP 0x05 OUT / 0x88 IN / 0x89 intr)
- macOS/Linux 验证:本包用同一套 gousb API,跨平台应可直接编译(macOS 无需 Zadig,Linux 可选走 /dev/cdc-wdm 更简单)
