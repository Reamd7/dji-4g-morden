# third_party/quectel-qmi-go/ — QMI 协议栈(用户态注入点)

> 从 `source/quectel-qmi-go/` 复制(2026-07-12)。
> 复制计划见 `plans/stage2/01-copy-and-export-injection.md`。
> Phase 0 探针结果见 `plans/stage2/00-phase0-transport-probe.md`。

## 来源与许可

- **上游**:`source/quectel-qmi-go/`(module `github.com/iniwex5/quectel-qmi-go`)
- **许可**:⚠️ **源码无 LICENSE 文件**,上游 README 也未提及。已创建 `LICENSE` NOTICE 文件记录此风险。生产使用前需联系作者(iniwex5)确认许可
- 复制方式:整包复制(`pkg/qmi/` → `qmi/`、`pkg/netcfg/` → `netcfg/`),去掉 `pkg/` 前缀

## 为什么复制(不 replace / 不现写)

- `replace` 不可移植(绝对路径换机器 / CI 断),且会拖入上游 go.mod 的 logrus/zap/netlink 依赖链
- 手写 QMI 全栈(QMUX 分帧 + 9 个 service wrapper + 拨号流程)= ~5000 行,quectel-qmi-go 已测试完整
- 复制后可自由改动(加导出函数、改 build tag),不影响上游

## 核心知识点(必读)

### 1. DTR 是 QMI 通信的前提(Phase 0 关键发现)

**QDC507 模组在收到 DTR 信号前不响应任何 QMI 请求。** 这是 Phase 0 探针一度得出"QMI 不可用"错误结论的根因。

发现路径:读 Linux 内核 `qmi_wwan.c` → `QMI_MATCH_FF_FF_FF(0x2c7c, 0x0125)` 匹配我们的模组 → `qmi_wwan_info_quirk_dtr` → bind 时调 `qmi_wwan_change_dtr(dev, true)`。驱动注释原文:"The device will not respond to QMI requests until we set DTR"。

DTR 控制传输(gousb):
```go
dev.Control(0x21, 0x22, 0x0001, uint16(ifaceNum), nil)
// 0x21 = class/interface/OUT
// 0x22 = USB_CDC_REQ_SET_CONTROL_LINE_STATE
// 0x0001 = DTR on (bit 0)
// wIndex = 接口号 (4 = MI_04)
```

### 2. 传输模型 = B(EP0 控制封装)

模型 A(bulk QMUX)不通,即使设了 DTR。只有模型 B 工作:

| 方向 | 操作 | gousb 等价 |
|---|---|---|
| TX | SEND_ENCAPSULATED_COMMAND | `dev.Control(0x21, 0x00, 0x0000, 4, frame)` |
| RX 通知 | interrupt EP 0x89 RESPONSE_AVAILABLE | `intrIn.ReadContext(ctx, buf)` |
| RX 数据 | GET_ENCAPSULATED_RESPONSE | `dev.Control(0xA1, 0x01, 0x0000, 4, buf)` |
| 前提 | 设 DTR(Open 时一次) | `dev.Control(0x21, 0x22, 0x0001, 4, nil)` |

### 3. transport_export.go 是注入点

`NewClientFromTransport(ctx, conn Transport, opts)` 让外部包注入自定义 USB transport,
绕过默认的 `/dev/cdc-wdm0`(Linux 内核驱动)。这是跨平台用户态 QMI 的关键。

`Transport = qmiTransport` 导出接口别名,需实现 4 个方法:
```go
Read([]byte) (int, error)          // 读一个 QMUX 帧
Write([]byte) (int, error)         // 写一个 QMUX 帧
Close() error                      // 释放
SetReadDeadline(time.Time) error   // 读超时
```

### 4. QueryVersionOnOpen 不存在

计划原文提到 `QueryVersionOnOpen`,但源码中没有此功能。实际初始化只有:
`newClientWithTransport`(启动 3 goroutine)→ `SyncOnOpen`(发 SYNC,非致命)。
`transport_export.go` 已按实际代码复刻,无 QueryVersionOnOpen。

### 5. netlink 不是阻碍

`netcfg/linux.go` 引用 `github.com/iniwex5/netlink`,但:
- build tag `//go:build linux` → Windows/macOS 不编译
- netcfg 是阶段 3(TUN 网络配置)用的 → 阶段 2(QMI 拨号)不碰
- `go build ./...` 在 Windows 上不需要此依赖

等阶段 3 在 Linux 上用 netcfg 时再处理。如果 `iniwex5/netlink` 拉不到,可改用 `golang.org/x/sys/unix` 做 netlink。

## 文件清单

### qmi/ (34 文件 + 1 新增)

| 文件 | 作用 |
|---|---|
| `client.go` | Client + readLoop/writerLoop/indicationLoop + NewClientWithOptions + SyncOnOpen |
| `transport.go` | `qmiTransport` 接口定义(未导出)+ `openRawTransport`(打开 /dev/cdc-wdm0) |
| **`transport_export.go`** | **新增**:导出 `Transport` 别名 + `NewClientFromTransport` 注入点 |
| `frame.go` | QMUX/CTL/Service 帧编解码 + `Packet.Marshal()`/`UnmarshalPacket()` |
| `wds.go` | WDS 服务:拨号/断开/runtime settings/profile/流量统计 |
| `wda.go` | WDA 服务:Raw-IP / 数据格式配置 |
| `dms.go` | DMS 服务:设备信息/序列号/PIN/ICCID/IMSI |
| `nas.go` | NAS 服务:驻网状态/信号/搜网/小区 |
| `uim.go` | UIM 服务:卡状态/PIN/APDU/文件读写(依赖 `warthog618/sms` 的 gsm7) |
| `wms.go` | WMS 服务:短信发送/读取/列举/删除/路由 |
| `voice.go` | VOICE 服务:拨号/接听/DTMF/USSD |
| `ims.go` / `imsa.go` / `imsp.go` | IMS 相关 |
| `errors.go` | QMI 错误码定义 |
| `proxy_transport_linux.go` | Linux QMI proxy 传输(build tag: linux) |
| `proxy_transport_unsupported.go` | 非 Linux 的 stub(build tag: !linux) |
| `*_test.go` | 各 service 的离线测试 |

### netcfg/ (8 文件)

| 文件 | 作用 | 平台 |
|---|---|---|
| `netcfg.go` | NetworkConfigurator 接口定义 | 全平台 |
| `configurator.go` | 通用配置逻辑 | 全平台 |
| `windows.go` / `factory_windows.go` | Windows 实现 | windows |
| `darwin.go` / `factory_darwin.go` | macOS 实现 | darwin |
| `linux.go` / `factory_linux.go` | Linux 实现(依赖 iniwex5/netlink) | linux |

## 依赖边界(已核实)

- `qmi/`:仅标准库 + `github.com/warthog618/sms/encoding/gsm7`(uim.go 用,已在项目 go.mod)
- `netcfg/`:仅 Linux 需要 `github.com/iniwex5/netlink`(未加入 go.mod,见上 §5)
- **零** vohive 内部包交叉引用(复制后无需改任何 import)

## 下一步(子计划 02)

实现 `internal/qmitransport/QMITransport`,实现 `qmi.Transport` 接口:

1. `Open()`:gousb claim MI_04 → 设 DTR → 启动 interrupt 读取 goroutine
2. `Write()`:封装为 `dev.Control(0x21, 0x00, 0x0000, 4, frame)`
3. `Read()`:等 interrupt 0x89 → `dev.Control(0xA1, 0x01, 0x0000, 4, buf)` → 返回 QMUX 帧
4. `Close()`:清 DTR → 释放 interface
5. `SetReadDeadline()`:设置 interrupt 读取超时

参考:`references/linux-driver/quectel-cm/QmiWwanCM.c` — 展示 write→SEND_ENCAPSULATED、read→interrupt+GET 的完整流程。

注入方式:
```go
transport, _ := qmitransport.Open(vid, pid, ifaceQMI)
client, _ := qmi.NewClientFromTransport(ctx, transport, qmi.DefaultClientOptions())
// client 现在可以用 WDS/WDA/NAS 等 service
```

## 相关文件

- `plans/stage2/00-phase0-transport-probe.md` — Phase 0 探针结果(模型 B + DTR)
- `plans/stage2/01-copy-and-export-injection.md` — 本包复制计划
- `plans/stage2/02-qmitransport-impl.md` — QMITransport 实现计划(下一步)
- `cmd/qmiprobe/main.go` — Phase 0 探针(含 DTR + 模型 B 验证代码)
- `references/linux-driver/` — Linux 驱动参考源码(qmi_wwan_q.c + quectel-cm)
- `source/quectel-qmi-go/` — 上游源码(复制来源)
