# 子计划 00 — Phase 0:QMI 传输模型探针

> 阶段 2 的**前置门**。隶属 `plans/stage2-qmi-dialup.md`(总览)。
> 解除头号风险:EC25 PID 0x0125 的 QMI 信令到底走 bulk 裸 QMUX(模型 A)还是 EP0 控制封装(模型 B)。

---

## 目标

在真实硬件上实测 QMI 传输模型,用事实决定 `internal/qmitransport/` 的实现路径。
**不做任何代码实现**,只跑探针、记录结果。

## 依赖 / 前置

- Zadig 给 MI_04(Iface 4)装 WinUSB(与阶段 1 的 MI_02 独立)
- gousb 环境(阶段 1 已就绪)
- 无代码依赖(探针独立,不依赖 quectel-qmi-go)

## 背景:为什么必须实测

MI_04 是"3端点合一"接口(bulk IN + bulk OUT + interrupt IN),固件两种模型都支持:

| 模型 | TX | RX | EP 0x89 interrupt |
|---|---|---|---|
| **A. bulk 裸 QMUX**(GobiNet) | EP 0x05 bulk OUT | EP 0x88 bulk IN | 不用(可忽略) |
| **B. EP0 控制封装**(cdc-wdm/qmi_wwan) | `dev.Control(0x21,0x00,wIndex=4,payload)` | 等 EP 0x89 RESPONSE_AVAILABLE → `dev.Control(0xA1,0x01,...)` GET | 必需(触发 GET) |

**不能从 sixfab 推断**:sixfab 走 `/dev/cdc-wdm0`,其底层恰恰是模型 B(cdc-wdm 的"透明字节流"是内核封装的假象)。raw USB 直连走 A 还是 B 由模组固件决定,必须探针。

证据:qmi_wwan.c 注释("Some devices combine control+data into one interface with all three endpoints")、quectel-qmi-proxy.c、usbmon 抓包。

## 步骤

### 1. 创建 `cmd/qmiprobe/main.go`(~100 行)

**QMICTL_SYNC_REQ 帧**(CTL service, msg 0x0027,无 TLV,13 字节):
```
01 00 28 00 00 00 00 00 00 27 00 00 00
 IFType LenLE16=40 CtlFlg Svc=CTL ClID=0  [CTL头] [MsgID=0x0027] [Len=0]
```

**两路探测**:
```go
syncReq := []byte{0x01,0x00,0x28,0x00,0x00,0x00,0x00,0x00,0x00,0x27,0x00,0x00,0x00}

// 探测 A: bulk —— EP 0x05 OUT 写, EP 0x88 IN 读(2s timeout)
bulkOut.Write(syncReq)
n, _ := bulkIn.ReadContext(ctx2s, buf)

// 探测 B: EP0 —— SEND_ENCAPSULATED_COMMAND + 等 interrupt + GET_ENCAPSULATED_RESPONSE
dev.Control(0x21, 0x00, 0x0000, 4, syncReq)       // wIndex=4(接口号)
time.Sleep(500ms)                                   // 等 interrupt 0x89 RESPONSE_AVAILABLE
n2, _ := dev.ControlContext(ctx2s, 0xA1, 0x01, 0x0000, 4, respBuf)
```

### 2. 运行 + 记录

```bash
mise exec -- go run ./cmd/qmiprobe
```

**判断标准**:
- bulk A 收到 `01 00 xx 00 01 00...`(SYNC_RESP,IFType+CTL+ServiceType) → **模型 A 成立**
- EP0 B 的 GET_ENCAPSULATED_RESPONSE 返回非空 QMUX → **模型 B 成立**
- 可能两个都通 → **选 A**(实现简单,方向 F 直接复用,interrupt 忽略)

## 交付物 / 完成标志

- [x] 传输模型确定:**模型 B(EP0 控制封装),需先设 DTR**。写入 `AGENTS.md`
- [x] 本计划标注结果,决定子计划 02 的实现分支(模型 B + DTR,见下)

## 实测结果(2026-07-12)

### 模组信息

| 项 | 值 |
---|---|
| 型号 | **QDC507**(非标准 EC25,DJI 定制版) |
| 固件 | QDC507GLEFM21 |
| PID | 0x0125(标准 EC25 QMI 模式) |
| usbnet | 0(QMI 模式) |
| CGATT | 1(PS 已附着) |
| PDP 上下文 | CID=1 "3gnet"(未激活),另有 SOS/3GWAP/WONET/ims/3gpp |
| $QCRMCALL | 支持(返回 OK) |

### 探针结果

**关键发现:必须先发 DTR**(CDC `SetControlLineState`),否则模组不响应任何 QMI。
源自 Linux `qmi_wwan.c`(drivers/net/usb/qmi_wwan.c):
`QMI_MATCH_FF_FF_FF(0x2c7c, 0x0125)` → `qmi_wwan_info_quirk_dtr` → `qmi_wwan_change_dtr(dev, true)`。
驱动注释:"The device will not respond to QMI requests until we set DTR"。

DTR 控制传输:`dev.Control(0x21, 0x22, 0x0001, 4, nil)`
(bmRequestType=0x21 class/interface/OUT,bRequest=0x22 SET_CONTROL_LINE_STATE,wValue=0x0001 DTR on,wIndex=4)

| 接口 | 模型 | DTR | TX 路径 | 结果 |
|---|---|---|---|---|
| MI_04 | A bulk | ❌ 未设 | EP 0x05 OUT → EP 0x88/0x89 IN | ❌ 无响应 |
| MI_04 | B control | ❌ 未设 | SEND_ENCAPSULATED → GET | ❌ GET 超时 |
| MI_04 | A bulk | ✅ 已设 | EP 0x05 OUT → EP 0x88/0x89 IN | ❌ 仍无响应 |
| MI_04 | A bulk (512B pad) | ✅ 已设 | EP 0x05 OUT padded | ❌ 无响应 |
| **MI_04** | **B control** | **✅ 已设** | **SEND → intr 0x89 → GET** | **✅ SYNC_RESP 19B, result=SUCCESS** |
| MI_00 | A bulk | ❌ 未设 | EP 0x01 OUT → EP 0x81 IN | ❌ 无响应(DM/DIAG 口) |

### SYNC_RESP 解析

设 DTR 后模型 B 返回的 19 字节:
```
01 12 00 80 00 00 01 01 27 00 07 00 02 04 00 00 00 00 00
```
- QMUX: IFType=01 | Len=0x0012(18) | CtlFlg=0x80(service) | SvcType=00(CTL) | ClID=00
- CTL: CtlFlg=01(response) | TxID=01(match) | MsgID=0x0027(SYNC) | Len=0x0007
- TLV: Type=0x02(result) | Len=4 | Value=0x00000000(SUCCESS)

Interrupt EP 0x89 同时收到 RESPONSE_AVAILABLE:`a1 01 00 00 04 00 00 00`(8B)

### SYNC 帧验证

计划原文的 SYNC 帧字节(`01 00 28 00 ...`,13 字节)有误:Length 字段 `00 28` LE16 = 0x2800(错)。
实际帧按 quectel-qmi-go `Packet.Marshal()`(frame.go)构造,12 字节:
`01 0B 00 00 00 00 00 01 27 00 00 00`(Length=0x000B=11=frameSize-1)。

### 结论

**传输模型 = B(EP0 控制封装),接口 = MI_04,前提 = 设 DTR。**

| 项 | 值 |
|---|---|
| TX | `dev.Control(0x21, 0x00, 0x0000, 4, qmiFrame)` — SEND_ENCAPSULATED_COMMAND |
| RX | 读 interrupt EP 0x89(RESPONSE_AVAILABLE)→ `dev.Control(0xA1, 0x01, 0x0000, 4, buf)` — GET_ENCAPSULATED_RESPONSE |
| 前提 | `dev.Control(0x21, 0x22, 0x0001, 4, nil)` — 设 DTR(CDC SetControlLineState),Open 时发一次 |
| Bulk(模型 A) | 不通,即使设了 DTR |
| Interrupt EP 0x89 | 用于 RESPONSE_AVAILABLE 通知(模型 B 必需) |

### 对子计划 02 的影响

子计划 02(QMITransport 实现)**解除阻塞**。按模型 B + DTR 实现:
1. `Open()`:claim MI_04 → 设 DTR → 启动 interrupt 读取 goroutine
2. `Write([]byte)`:封装为 SEND_ENCAPSULATED_COMMAND control transfer
3. `Read([]byte)`:等 interrupt RESPONSE_AVAILABLE → GET_ENCAPSULATED_RESPONSE → 返回 QMUX 帧
4. `Close()`:清 DTR → 释放 interface

**与 AT transport(方向 F)的差异**:QMITransport 的 Read 是"interrupt 触发 → control GET"两步,
不是长阻塞 bulk 读。但对外仍实现 `qmiTransport` 接口(Read/Write/Close/SetReadDeadline)。

## 风险

| 风险 | 缓解 |
|---|---|
| ~~两个都不通~~ | **已解决**:根因是缺少 DTR,设 DTR 后模型 B 通 |
| 模型 B 的 interrupt 时序 | 已验证:interrupt 在 SEND 后 ~200ms 内收到,GET 立即返回数据 |
| DTR 时序 | 设 DTR 后等 500ms 让 QMI 服务唤醒,已验证足够 |
