# internal/usbtransport/ — USB bulk endpoint → io.ReadWriteCloser

本目录是**阶段 1 的核心交付物**:把 DJI 百望模组 MI_02(AT 命令口)的 USB bulk endpoint 包装成 `io.ReadWriteCloser`,喂给 `third_party/sms-gateway/modem.NewFromIO`,实现纯用户态 AT 通道。

## 包的职责

```
gousb (libusb/WinUSB)
    ↓ USB bulk transfer
ATTransport (本包)
    ↓ io.ReadWriteCloser + 长阻塞读(Close 时才 cancel)语义
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

### Read 的长阻塞读(方向 F,修复 WinUSB cancel 崩溃)

`Read` 用一个**长生命期 context**(`readCtx`,`context.WithCancel(Background)` 无超时)调
`in.ReadContext`,阻塞到有数据或 Close——**运行期 0 次 `libusb_cancel_transfer`**。
只有 `Close` 调 `readCancel()` 才 cancel 一次。

**为什么不短轮询**:原设计 `ReadContext(ctx200ms)` 会让 gousb 每 200ms 调一次
`libusb_cancel_transfer`(~5 次/秒),在 WinUSB 上偶发 segfault(发送时 read-cancel
撞上 write transfer 并发)。详见 `issue/001-gousb-close-transfer-cancel-crash.md`。
判别实验证实:10× 降频(2000ms)消除崩溃;方向 F 把发送期间 cancel 降到 0,根治。

**Close 配合**:`Close` 先 `readCancel()`(让在途 Read 返回)再释放 USB handles,
避免"释放 USB 时有在途 transfer"的竞态。`context.Canceled` 实现 `Timeout()==true`,
modem.readerLoop 的 `isTimeout` 识别后 continue,下一轮检查 `m.closed` 退出。

**readLine 配套改造**(modem 包):原 `readLine` 靠"读超时返回错误"检测 CMGS `>` 提示符
(`>` 不以 `\n` 结尾)。长阻塞读下 Read 不超时 → `ReadString('\n')` 永远等不到 `\n` →
`>` 卡死。改为逐字节 `r.Read`,每字节检查 `\n`(完整行)或 buffer 已是 `>`(提示符),
不再依赖超时。这让 readLine 同时对串口(短超时)和 USB(长阻塞)工作。

### endpointReader / endpointWriter 接口抽象(可测性)

gousb 的 `*InEndpoint`/`*OutEndpoint` 是具体结构体,无法 mock。本包把 I/O 抽成两个接口:
```go
type endpointReader interface { ReadContext(ctx context.Context, buf []byte) (int, error) }
type endpointWriter interface { Write(buf []byte) (int, error) }
```
- **生产**:gousb 的 `*InEndpoint`/`*OutEndpoint` 天然满足,编译期断言 `_ endpointReader = (*gousb.InEndpoint)(nil)`
- **测试**:`scriptReader`/`scriptWriter` 适配 `testutil.ScriptPort`,让 Read 的长阻塞逻辑完全离线测试

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
- ❌ 不要恢复 200ms 短轮询 Read(会重新触发 WinUSB cancel 崩溃,见 `issue/001`)

## 下一步演进

- 阶段 2 会参考本包的接口抽象模式,写 MI_04 的 QMI transport(EP 0x05 OUT / 0x88 IN / 0x89 intr)
- macOS/Linux 验证:本包用同一套 gousb API,跨平台应可直接编译(macOS 无需 Zadig,Linux 可选走 /dev/cdc-wdm 更简单)
