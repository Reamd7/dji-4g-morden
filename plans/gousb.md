# 阶段 1 实施计划:USB 通路打通 + endpoint 探测

> 本文件是**设计文档**,记录阶段 1(gousb + AT 通道)的实施方案、调研结论和待验证事项。
> 创建于 2026-07-11。本文档本身不实现任何代码,具体实现按步骤推进时再落地。

---

## 一、背景与选型

### 1.1 USB 库选型结论

阶段 1 需要用 Go 通过 USB 直连 DJI "百望" 4G 模块(刷成标准 EC25 PID `2C7C:0125`),
替代内核 cdc-wdm + 串口驱动。USB transport 层有三个候选库:

| 方案 | cgo | Windows 安装 | API 完整度 | 评估 |
|---|---|---|---|---|
| **google/gousb(选定)** | 是,绑 libusb | 需手装 libusb + pkg-config | 最完整(claim/bulk/interrupt/control) | 功能首选,本机 mingw64 gcc 已就绪 |
| karalabe/usb | 是,libusb 源码内置 | `go get` 即用 | 偏 HID,不如 gousb | 跨平台部署最省心的备选 |
| 桥接 nusb(Rust) | 是(Rust cdylib) | 要编 Rust + Go 双工具链 | = gousb | 调研结论:不值得 |

**选定 google/gousb。** 本机 `.mise.toml` 已接好 mingw64 gcc 16.1.0,只差一个 libusb。

### 1.2 "桥接 nusb 给 Go" 的调研结论

技术可行(nusb 有 blocking API,可绕开 async/tokio FFI 坑),但**不值得**:

1. **零先例** —— 没有任何项目把 nusb/rusb 桥接给 Go,Go 调 Rust 做 USB/硬件访问的成熟先例也不存在。
2. **FFI 全自造** —— C ABI 设计、buffer 内存所有权、错误码映射、cbindgen、Go 侧 wrapper,全得自己写。
3. **三套工具链 CI** —— cdylib 在 Windows/macOS/Linux 要三套 Rust 编译链,跨平台编译反而比 gousb 更麻烦。
4. **功能重叠** —— gousb 的 claim/bulk/interrupt/control 和 nusb 完全重叠,桥接不带来新功能。

`docs/01` 的结论"Rust 唯一的优势(nusb 零依赖)不足以弥补缺少协议库的劣势"依然成立。

### 1.3 Windows 上 WinUSB 是硬前提(与库无关)

**无论选 gousb / karalabe / nusb,Windows 上都必须用 Zadig 装 WinUSB。** 这是 Windows 操作系统的架构限制:

| 平台 | 是否需要装驱动 | 机制 |
|---|---|---|
| Linux | ❌ 不需要 | usbfs 内核自带,插上就能被用户态访问 |
| macOS | ❌ 不需要 | IOKit 系统内置 |
| **Windows** | ✅ 需要(一次) | 无通用 USB 直通;每个接口必须绑定函数驱动,WinUSB 是唯一通用的用户态可访问选择 |

调用链(Windows):
```
Go 代码 → gousb → libusb → winusb.dll(用户态)→ winusb.sys(内核函数驱动)→ 硬件
                                    ↑
                              Zadig 装的就是这一层
```

nusb 的调用链(Windows):
```
Rust 代码 → nusb(纯 Rust)→ winusb.dll → winusb.sys → 硬件
                                   ↑
                             依然要 Zadig 装 winusb.sys
```

"纯 Rust、零依赖"指**编译时**不依赖 libusb,**运行时**仍需 winusb.sys 已绑定到接口。
Zadig 改动持久(写进 Windows 驱动数据库),重启、拔插都不丢。

---

## 二、实施步骤

### 第 1 步:Zadig 装 WinUSB(手动,操作系统层硬前提)

**前提**:当前 5 个接口已卸载 Quectel 驱动,处于 `Baiwang (Error)` 裸状态。

操作:
1. 下载 Zadig:https://zadig.akeo.ie/
2. 打开 Zadig → **Options → List All Devices**
3. 下拉框里会看到 5 个 `Baiwang (MI_00 ~ MI_04)` 接口
4. 每个接口:右侧目标选 **WinUSB(v6.x)** → **Replace Driver**
5. **优先级**:MI_02(AT 口)和 MI_04(QMI)必装;其余三个建议全装(模拟 macOS 裸环境最纯粹)

USB 接口布局(来自 `docs/01` 4.2 节,与 PowerShell 实测一致):

| MI | 功能 | 阶段 1 用途 |
|---|---|---|
| MI_00 | DM 诊断口 | — |
| MI_01 | NMEA GPS | — |
| **MI_02** | **AT 命令口** | **阶段 1 核心** |
| MI_03 | Modem 控制 | — |
| **MI_04** | **QMI 数据通道** | 阶段 2/3 用 |

**验证命令**(装完后我跑):
```powershell
Get-CimInstance -ClassName Win32_PnPSignedDriver | Where-Object { $_.DeviceID -match 'VID_2C7C&PID_0125' } | Select-Object FriendlyName, DeviceID, DriverProviderName | Format-List
```
预期:`DriverProviderName` 全部变为 `Microsoft`(winusb.sys),不再是 Quectel 或空。

---

### 第 2 步:MSYS2 装 libusb + 更新 .mise.toml

gousb 在 Windows 编译需要 `libusb-1.0` + `pkg-config`。MSYS2 mingw64 仓库有现成包。

**安装命令**:
```bash
pacman -S --needed mingw-w64-x86_64-libusb mingw-w64-x86_64-pkg-config
```

产出:
- `C:\msys64\mingw64\lib\pkgconfig\libusb-1.0.pc`(pkg-config 描述文件)
- `libusb-1.0.dll`(运行时)
- `libusb-1.0.dll.a`(链接导入库)

**`.mise.toml` 更新**(补 `PKG_CONFIG_PATH`):
```toml
[tools]
go = "latest"

[env]
_.path = ["C:\\msys64\\mingw64\\bin"]
CC = "C:\\msys64\\mingw64\\bin\\gcc.exe"
PKG_CONFIG_PATH = "C:\\msys64\\mingw64\\lib\\pkgconfig"
```

**验证命令**:
```bash
mise exec -- pkg-config --cflags --libs libusb-1.0   # 应输出 include/lib 路径
mise exec -- go env CGO_ENABLED                       # 应为 1
```

> **静态链接 libusb**(可选,避免分发带 dll):
> ```bash
> export CGO_LDFLAGS="-lusb-1.0 -lwinpthread -static"
> ```
> mingw 静态链接需把 libwinpthread 也静态拉进来。完全静态在 Windows 上较脆弱,**建议默认带 `libusb-1.0.dll` 分发**。

---

### 第 3 步:gousb 探针程序 `cmd/usbprobe`(核心,解除不确定性)

**这是阶段 1 最有价值的一步**,目标是解除 `docs/01` 第 8.1 节的未解事实:
> EC25 模式下 MI_02(AT 口)和 MI_04(QMI)的 USB endpoint 地址未知。
> 文档里只有 DJI 私有模式的地址(Iface 3, EP 0x04/0x86),EC25 模式下可能不同。

**探针程序做的事**:
1. 用 `ctx.OpenDevices` 匹配 VID 0x2C7C PID 0x0125,打印设备描述符
2. 遍历所有 Config → Interface → Endpoint,**dump 每个 MI_xx 的 endpoint 地址、方向(IN/OUT)、传输类型(bulk/interrupt/control)**
3. 输出对照表,确认:
   - MI_02(AT 口)的 bulk IN / bulk OUT 地址
   - MI_04(QMI)的 bulk IN / bulk OUT / interrupt IN 地址
4. (探针稳定后)claim MI_02,往 bulk OUT 写 `AT\r\n`,从 bulk IN 读响应,验证 `OK`
   —— 这一步成功意味着 **USB→AT 通路彻底打通**,阶段 1 最大技术风险消除

**程序结构**:
```
cmd/usbprobe/main.go   # ~80 行:枚举 + dump + 可选 AT 测试
```

**代码骨架**(gousb 新 API,`OpenDeviceWithVIDPID` 已废弃):
```go
package main

import (
	"fmt"
	"log"

	"github.com/google/gousb"
)

const (
	ec25VID = 0x2C7C
	ec25PID = 0x0125
)

func main() {
	ctx := gousb.NewContext()
	defer ctx.Close()

	devs, err := ctx.OpenDevices(func(desc gousb.DeviceDesc) bool {
		return desc.Vendor == gousb.ID(ec25VID) && desc.Product == gousb.ID(ec25PID)
	})
	if err != nil {
		log.Fatalf("OpenDevices: %v", err)
	}
	if len(devs) == 0 {
		log.Fatalf("未找到 EC25 设备 (VID=%04x PID=%04x)", ec25VID, ec25PID)
	}
	dev := devs[0]
	for i := 1; i < len(devs); i++ {
		devs[i].Close()
	}
	defer dev.Close()

	log.Printf("打开设备: %04x:%04x", dev.Desc.Vendor, dev.Desc.Product)
	dev.SetAutoDetach(true) // Windows 上是 no-op,但写上无害

	cfg, err := dev.Config(1)
	if err != nil {
		log.Fatalf("Config(1): %v", err)
	}
	defer cfg.Close()

	// dump 所有 interface 的 endpoint
	for ifaceNum := 0; ifaceNum < 5; ifaceNum++ {
		itf, err := cfg.Interface(ifaceNum, 0)
		if err != nil {
			log.Printf("Interface(%d,0): %v", ifaceNum, err)
			continue
		}
		fmt.Printf("\n=== MI_0%d ===\n", ifaceNum)
		for _, ep := range itf.Setting.Endpoints {
			dir := "OUT"
			if ep.Direction == gousb.EndpointDirectionIn {
				dir = "IN"
			}
			fmt.Printf("  EP 0x%02x  %-3s  %s  maxPacket=%d\n",
				ep.Address, dir, ep.TransferType, ep.MaxPacketSize)
		}
		itf.Close()
	}
}
```

**运行**:
```bash
mise exec -- go run ./cmd/usbprobe
```

**预期产出**:一张 MI_00 ~ MI_04 的 endpoint 表,含每个接口的 bulk IN/OUT + interrupt IN 地址。

---

### 第 4 步:USB transport 包 + AT 层(探针通过后再定细节)

**本步依赖第 3 步探针的真实输出**,以下只列结构,endpoint 地址和具体实现留到探针跑通后再定。

**程序结构(阶段 1 完整布局)**:
```
dji-modem-research/
├── cmd/
│   ├── usbprobe/main.go        # 第 3 步:探针
│   └── sms/main.go             # 第 4 步:收发短信 CLI
├── internal/
│   ├── usbtransport/           # USB bulk → io.ReadWriteCloser 适配
│   │   └── transport.go        # ~150 行
│   └── at/                     # AT 帧 + PDU 编解码
│       ├── at.go               # ~50 行:CRLF 分帧、OK/ERROR 解析、URC 分流
│       └── pdu.go              # 从 sms_gateway 拷贝的纯函数 PDU 编解码
└── main.go                     # 暂保留 hello world
```

**`internal/usbtransport/transport.go` 关键点**:
- 实现 `io.ReadWriteCloser` 接口(给 at 包用)
- **可选**实现 `SetReadDeadline`/`SetWriteDeadline`(gousb 没有原生 deadline)
- deadline 模拟方案:用 timer + select,参考 `uicc-go/at/serial_linux.go` 的 poll 模式
- 超时返回的 error 需让 `os.IsTimeout` 认得(实现 `Timeout() bool`)

**`internal/at/at.go` 关键点**:
- AT 文本命令收发(不是 CSIM/APDU,见第三节)
- 发:`AT+CMGS=...` 等,以 `\r\n` 结尾
- 收:按 `\r\n` 分帧,识别 `OK` / `ERROR` / `+CMS ERROR` / URC(如 `+CMTI:`)
- 短信收发命令:
  - 初始化:`AT+CMGF=0`(PDU 模式)、`AT+CNMI=...`(新短信通知)
  - 发:`AT+CMGS=<len>` → 等 `> ` 提示 → 发 PDU hex → Ctrl+Z
  - 读:`AT+CMGL=4`(列出所有存储短信)
  - 删:`AT+CMGD=<index>`

**`internal/at/pdu.go` 来源**:
从 `source/sms_gateway/agent/internal/modem/pdu.go` **拷贝**(不是 import)。
该文件是纯函数 PDU 编解码,无 transport 依赖,导出:
- `EncodeSubmit(recipient, body string, udh SubmitUDH) (hexPDU string, tpduLen int, err error)` — 编码(发)
- `DecodeDeliver(hexPDU string) (*DecodedSMS, error)` — 解码(收)
- `type DecodedSMS struct{ Sender, Body, Timestamp, RawPDU, Concat }`
- `type SubmitUDH struct{ ... }` — 多段拼接头

---

## 三、协议栈注入点调研结论(影响 fork 时机)

### 3.1 核心发现:两个注入点的 transport 接口都是私有 API

| 注入点 | 接口/构造函数 | 可见性 | 外部 module 能否注入 |
|---|---|---|---|
| **uicc-go/at** | `newReader(io.ReadWriteCloser) *Reader` | 私有(小写 n) | ❌ 不能 |
| **quectel-qmi-go** | `qmiTransport` interface + `openRawTransportHook` var | 私有(小写) | ❌ 不能 |

Go 可见性规则:小写标识符跨 package 不可达。测试能改 hook 是因为测试在 `package qmi` 内部。

### 3.2 阶段 1(AT+短信)可以完全绕开 fork

关键洞察:**AT 短信走的是 `AT+CMGS/CMGL` 文本命令,不走 CSIM/APDU**。
- `uicc-go/at` 包**只有 CSIM/APDU 能力**(`Transmit` → 内部转 `AT+CSIM=`),**没有** `CMGS`/`CMGL` 文本命令能力
- 所以阶段 1 不需要 uicc-go 的 at 包,**自己写 ~50 行 AT 帧就够**
- sms_gateway 的 `pdu.go` 是纯函数,**直接拷贝即可**,不依赖 transport
- sms_gateway 的 `modem.go` 用 `serial.Port`(具体类型,非 `io.ReadWriteCloser` 接口),**硬适配反而麻烦**,所以不直接复用 modem 层

**结论:阶段 1 全程不需要 fork 任何协议库。** 只需要:
1. 自己写 ~50 行 AT 帧(`internal/at/at.go`)
2. 从 sms_gateway 拷贝 PDU 编解码(`internal/at/pdu.go`,纯函数)
3. 自己写 USB transport(~150 行)

### 3.3 fork 时机(fork 留到阶段 1.5 / 阶段 2)

**阶段 1.5(SIM 卡 APDU 操作,读 ICCID/IMSI 等)**:
- fork `uicc-go`,加一个导出的构造函数:
  ```go
  // uicc-go/at/at.go 新增(一行)
  func NewReader(port io.ReadWriteCloser) *Reader { return newReader(port) }
  ```
- `go.mod` 里 `replace github.com/damonto/uicc-go => ./fork路径`

**阶段 2(QMI 通道 + 拨号)**:
- fork `quectel-qmi-go`(module `github.com/iniwex5/quectel-qmi-go`)
- 在 `pkg/qmi/client.go` 加导出函数 `NewClientWithTransport`,或导出 `type Transport = qmiTransport`
- 注意 QMI 的 `readLoop`(client.go:549)依赖 `SetReadDeadline` 做**轮询式心跳**(默认 `ReadDeadline=100ms`),所以 QMI transport 的 `SetReadDeadline` **是必须的**,且 `Read` 在 deadline 到时必须返回 `os.IsTimeout` 为真的 error

---

## 四、gousb Windows API 用法要点(备忘)

### 4.1 新 API(当前版本)

- **`OpenDeviceWithVIDPID` 已废弃**,用 `ctx.OpenDevices(func(desc) bool)` + 手动 VID/PID 匹配
- 打开设备后:`device.Config(n)` → `config.Interface(n, alt)` → `endpoint.Read/Write`
- `SetConfiguration` 在 Windows 上支持有限;若已是目标 config,`device.Config(n)` 智能跳过

### 4.2 Claim / Endpoint

- `*Interface` 实现了 `io.Closer`,**必须 `defer itf.Close()`**
- 按地址拿 endpoint:`itf.EndpointByAddress(0x86)`(返回 `Endpoint`,需类型断言到 `*InEndpoint`)
- 按编号+方向:`itf.InEndpoint(6)` / `itf.OutEndpoint(4)`
- endpoint 地址 = number | direction bit(bit7=1 表示 IN,如 0x86 = EP6 IN)

### 4.3 读写

- **简化 API**:`InEndpoint.Read(buf)` / `OutEndpoint.Write(data)`,内部自动提交 transfer
- **超时**:简化 API 用默认超时;自定义超时用 Transfer API(`ep.NewTransfer` 或 `ep.NewStream`),设 `Transfer.Timeout`
- **超时坑**:超时时 `err != nil` 但 `n` 可能 > 0(部分数据已到),不要丢弃已读字节

### 4.4 Interrupt endpoint

- gousb 对 bulk 和 interrupt endpoint **一视同仁** —— `InEndpoint.Read(buf)` 根据 endpoint descriptor 的 `bmAttributes` 自动选 interrupt 或 bulk transfer
- 读 interrupt IN(URC/通知)的代码和读 bulk IN **完全一样**

### 4.5 并发

- `InEndpoint` 和 `OutEndpoint` 是独立 transfer 对象,可**在不同 goroutine 并发**(一个读、一个写)
- **不能**两个 goroutine 同时 `Read` 同一个 InEndpoint(简化 API 下会 panic/错乱)
- 高并发读用 `NewStream`

### 4.6 Windows 上的坑

- `SetAutoDetach` 在 WinUSB 后端是 **no-op**(Linux 概念);必须 Zadig 换 WinUSB
- 接口被厂商驱动占用时 claim 失败(`LIBUSB_ERROR_NOT_FOUND` / `LIBUSB_ERROR_ACCESS`)→ Zadig 解决
- `device.Close()` / `cfg.Close()` / `itf.Close()` 三层都要 close,顺序:interface → config → device

---

## 五、不确定性与待验证事项

### 5.1 待探针解除的事实(第 3 步产出)

- [ ] MI_02(AT 口)的 bulk IN / bulk OUT endpoint 地址
- [ ] MI_04(QMI)的 bulk IN / bulk OUT / interrupt IN endpoint 地址
- [ ] Zadig 装 WinUSB 后,gousb 能否成功 claim MI_02
- [ ] claim MI_02 后,`AT\r\n` → `OK` 是否返回(USB→AT 通路验证)

### 5.2 留到后续步骤再定

- [ ] USB transport 的 deadline 实现细节(取决于探针验证的 gousb 超时行为)
- [ ] `internal/at/at.go` 的具体分帧/URC 分流逻辑(取决于探针读到的真实响应格式)
- [ ] 静态 vs 动态链接 libusb 的最终决策(取决于分发需求)

### 5.3 已知风险(来自 `docs/01` 第八节)

- **8.2 QMI 数据格式**:阶段 2 涉及,QMI 数据通道可能是 raw-IP 或 QMAP 封装,需 WDA SetDataFormat 正确配置
- **8.3 Windows USB 接口争用**:方案 A 和方案 B 不能同时用同一接口(Zadig 已解决,装了 WinUSB 就不再有厂商驱动)
- **8.4 macOS IOKit USB 权限**:命令行程序通常没问题,沙箱环境有限制
- **8.5 性能**:用户态 USB 比内核驱动多一次用户态↔内核态拷贝,但 LTE Cat 4 带宽远低于 USB 2.0,实测应无明显差异

---

## 六、参考来源

### gousb
- [github.com/google/gousb](https://github.com/google/gousb) — 官方仓库
- [pkg.go.dev/github.com/google/gousb](https://pkg.go.dev/github.com/google/gousb) — API 参考
- [gousb Issue #100: Windows/MSYS2 安装](https://github.com/google/gousb/issues/100)
- [gousb Issue #112: WinUSB drivers](https://github.com/google/gousb/issues/112)

### 协议栈注入点(本地源码)
- `source/vohive-collection/uicc-go/at/at.go` — `Reader`、`newReader`(私有)、`Transmit`
- `source/vohive-collection/quectel-qmi-go/pkg/qmi/transport.go` — `qmiTransport`(私有)、`openRawTransportHook`
- `source/vohive-collection/quectel-qmi-go/pkg/qmi/client.go` — `NewClientWithOptions`、`newClientWithTransport`(私有)
- `source/sms_gateway/agent/internal/modem/pdu.go` — PDU 编解码纯函数(待拷贝)
- `source/sms_gateway/agent/internal/modem/sms.go` — AT+CMGS/CMGL 参考

### nusb 桥接调研(为何不选)
- [kevinmehall/nusb](https://github.com/kevinmehall/nusb) — 有 blocking API,但 FFI 无先例
- [tokio-rs/tokio Discussion #5840](https://github.com/tokio-rs/tokio/discussions/5840) — async Rust FFI 边界坑

### EC25 USB 描述符
- [EC2x Series USB Interface Descriptor Introduction (Quectel PDF)](https://auroraevernet.ru/upload/iblock/170/r6z29mob4ocaqlwmdbma70pk5avgdx0s.pdf)
