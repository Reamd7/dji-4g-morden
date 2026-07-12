# 升级计划:路线 B —— SG 壳 + smscodec 芯

> 本文件是**设计文档**,记录把 PDU 编解码层从 sms_gateway 手写 `pdu.go`(路线 A)升级到 vohive `smscodec` + warthog618/sms(路线 B)的实施方案。
> 创建于 2026-07-12。前提:阶段 1 已达成(`plans/usb-transport.md` 全部完成,SG 壳跑通真实收发,见上级 `AGENTS.md` 实测记录)。
> 决策依据:`docs/05`(SG 剖析)、`docs/06`(vohive 剖析)、`docs/07`(对比 + 选型,§五路线 B)。

---

## 一、为什么要现在升级

阶段 1 选用**纯 SG**(路线 A)的理由是"最快打通链路、PDU 三缺陷可接受"(`docs/07` §五)。现在链路已通,SG `pdu.go` 的三个缺陷变成真实限制:

| 缺陷 | 影响 | 证据 |
|---|---|---|
| 无 GSM-7 扩展表 | 收含 `^{}[]~\|\\€` 的短信丢字(全变 `?`) | `pdu.go:4-5` 明确放弃;`docs/07` §三 差异 3 |
| 发送只 8-bit ref + 不自动分段 | 发长短信超长直接报错;多段拼接头只能手工塞 | `pdu.go:161`(IEI 0x00 硬编码)、`pdu.go:191`(超长报错);`docs/07` §三 差异 4 |
| 接收无重组器 | `DecodeDeliver` 返回 `ConcatInfo` 给调用方自己拼 | `pdu.go` 无 Reassembler;`docs/06` §七 |

**路线 B 的目标**:保留 SG 的 AT 框架壳(`modem.go` 并发模型 + `sms.go` 的 AT 编排),只把 PDU 编解码"芯"换成 `smscodec`(委托 warthog618/sms,Go 生态标准符合度最高的 TS 23.040 实现)。这恰好补齐 D 类(PDU 编解码)短板,而 A/E 类(AT 语法 + 初始化)保持 SG 的优势不变。

### 升级后的标准符合度(对照 `docs/07` §二评分表)

| 大类 | 升级前(纯 SG) | 升级后(SG 壳 + smscodec 芯) |
|---|---|---|
| A. AT 语法基础 | ✅ | ✅(壳不变) |
| B. SMS 命令族 | ⚠️ 6/9 | ⚠️ 6/9(壳不变;CSCA/CMGR/CSCS 仍缺,非本计划范围) |
| C. URC 监听 | ⚠️ 架构干净 | ⚠️(壳不变) |
| **D. PDU 编解码** | **⚠️ 三缺陷** | **✅ 完整**(扩展表 / 16-bit ref / 自动分段 / 重组 / 国产容错) |
| E. 初始化 | ✅ | ✅(壳不变) |

---

## 二、架构决策

### 2.1 保持不变的部分(SG 壳)

- **`modem.go`** —— 完全不动。readerLoop + pending channel + 回显双保险 + `isTerminator` + `>` 提示符 + 超时 drain,这些是 A/C/E 类优势的来源。
- **`sms.go` 的 AT 编排** —— `Initialize` / `ensureSIMReady` / `ListStored`(+CMGL 解析)/ `DeleteStored` / 设备查询(ICCID/IMEI/Carrier/PhoneNumber/Signal)全部不动。这些只依赖 `SendAndWait`/`SendRaw`,不碰 PDU。
- **`internal/usbtransport/`** —— 不动。`ATTransport` 已经是 `io.ReadWriteCloser`,与本计划无关。

### 2.2 替换的部分(芯)

- **`third_party/sms-gateway/modem/pdu.go`**(574 行手写)→ 删除内部编解码逻辑,改为**薄 facade**(委托 smscodec)。
- **`sms.go:Send`** —— 从"编 1 个 PDU + 单次 CMGS"改为"BuildSubmitTPDUs 编 N 段 + 循环 CMGS"。

### 2.3 公共 API 形状:facade(隐藏 warthog618)

**保留** modem 包现有的聚合类型,作为对外的稳定 API,把 warthog618 依赖藏在包内:

| 类型 | 处理 | 理由 |
|---|---|---|
| `DecodedSMS{Sender,Body,Timestamp,RawPDU,Concat}` | **保留** | 聚合类型,消费者(测试/上层)用得舒服;避免把 warthog618 的 `*tpdu.TPDU` 泄露到公共 API |
| `ConcatInfo{Ref,Part,Total}` | **保留**(facade 内做 `Seq→Part` 映射) | 同上;smscodec 的 `ConcatInfo{IsConcat,Ref,RefBits,Total,Seq}` 多了内部字段,不适合直接对外 |
| `EncodeSubmit(recipient,body,udh)→(hex,tpduLen,err)` | **删除** | smscodec 自动分段,签名(返回单个 PDU)无法诚实保留 |
| `SubmitUDH{Ref,Part,Total}` | **删除** | 自动分段后无需手工拼接头;smscodec 内部决定 ref |

> 这是 facade 不是 shim:`DecodedSMS`/`ConcatInfo` 是合理的聚合类型(非遗留),保留它们让 modem 包的公共 API 稳定、warthog618 不外泄。`EncodeSubmit`/`SubmitUDH` 是被新能力(多段)取代的 API,**按 clean cutover 原则干净删除并迁移所有调用方**,不留别名。

### 2.4 复制范围:verbatim(整包原样复制,~2000 行)

**已决策:整包 verbatim 复制**,匹配 `third_party/sms-gateway/` 的复制先例——一行不改,以后 vohive 更新 smscodec 时直接覆盖。虽然 binary_classifier + wbxml_omacp(~1340 行)当前用不到,但它们无害(仅在 8-bit DCS 分支触发),保留即免费获得未来 OMA CP / WAP Push 解码能力。

| 文件 | 行数 | 说明 |
|---|---|---|
| `pdu.go` | 449 | PDU 编解码入口:DecodeDeliverTPDU / BuildSubmitTPDUs / ConcatInfo / RPDU 函数 |
| `reassembler.go` | 82 | 长短信重组,本计划核心收益之一 |
| `pdu_trim.go` | 149 | 国产模组 GSM-7 spare bit 清零 + PDU 长度裁剪,`DecodeDeliverTPDU` 依赖 |
| `binary_classifier.go` | 463 | 8-bit 二进制短信分类(OMA CP / WAP / MMS / SIM OTA),当前用不到但无害 |
| `wbxml_omacp.go` | 879 | OMA Client Provisioning 的 WBXML 解码,仅 binary_classifier 调用 |
| `*_test.go` | — | 选择性复制离线测试向量(reassembler / pdu 编码 / pdu_trim),作回归基线 |

> pdu.go 里的 RPDU 函数(ParseRPData / BuildRPData / BuildRPAck / BuildRPError 等)是 SIP/HTTP 网关侧的 RP-DATA 处理,AT modem 用不到。verbatim 方案下**保留不删**(删了就不是 verbatim 了),它们无依赖、不膨胀编译产物,留着不影响。

---

## 三、依赖引入

### 3.1 warthog618/sms

```bash
mise exec -- go get github.com/warthog618/sms@v0.3.0
```

- 许可:MIT(`docs/06` §一已确认)
- 版本:对齐 vohive `go.mod`(`source/vohive-collection/vohive/go.mod:24` = v0.3.0)
- 仅 `smscodec` 用到三个子包:`sms`、`sms/encoding/tpdu`、`sms/encoding/ucs2`(见 `pdu.go:10-12`)

### 3.2 当前 go.mod(无 warthog618)

`go.mod` 现有依赖仅 gousb/zerolog/serial/sys。本计划新增 `github.com/warthog618/sms`,并通过 facade 确保**warthog618 不出现在 modem 包的公共 API**(仅 `third_party/smscodec/` 内部 import)。

---

## 四、实施步骤

### Step 1 — 复制 smscodec 到 `third_party/smscodec/`

整包 verbatim 复制(§2.4 已决策),一行不改:

```
third_party/smscodec/
├── LICENSE              # warthog618 MIT + vohive source-available(随源声明)
├── AGENTS.md            # 说明来源 + 依赖 + verbatim 不改源 的约定
├── pdu.go               # 原样(含 RPDU 函数,保留不删)
├── reassembler.go       # 原样
├── pdu_trim.go          # 原样
├── binary_classifier.go # 原样(当前用不到,无害)
├── wbxml_omacp.go       # 原样(当前用不到,无害)
└── *_test.go            # 选择性复制(reassembler / pdu 编码 / pdu_trim 的离线向量)
```

包名保持 `smscodec`(import 路径 `dji-modem-research/third_party/smscodec`)。源文件零修改——`package smscodec` 原样,只改 import 路径指向本仓(若有内部互相 import,本来就同包无需改)。

**复制后必做**:`mise exec -- go build ./third_party/smscodec/...` 确认编译通过。唯一可能的断点:pdu.go/binary_classifier.go/wbxml_omacp.go 的 import 都是 `github.com/warthog618/sms` + 标准库(已核实,见 §2.4 表 + `docs/06` §一),Step 2 拉 warthog618 后即满足。

### Step 2 — 加 warthog618/sms 依赖

```bash
mise exec -- go get github.com/warthog618/sms@v0.3.0
mise exec -- go mod tidy
```

tidy 后确认 `go.mod` 含 warthog618,且无 vohive 内部包(config/apduarbiter/simaid/logger)被拖入。

### Step 3 — 重写 `third_party/sms-gateway/modem/pdu.go` 为 facade

**删除**:全部手写编解码(`EncodeSubmit` / `DecodeDeliver` 实现 / `encodeAddress` / `decodeAddress` / `encodeGSM7` / `decodeGSM7FromBitOffset` / `packGSM7Shifted` / `encodeUCS2` / `decodeUCS2` / `decodeSCTS` / `parseUDH` / `decodeUserData` / `needsUCS2` / `gsm7Set` ... 共 ~500 行)。

**保留 + 改写**为 ~100 行 facade:

```go
package modem

import (
	"encoding/hex"
	"fmt"
	"time"

	"dji-modem-research/third_party/smscodec"
)

// DecodedSMS —— 保持原聚合类型不变(见 §2.3 facade 决策)。
type DecodedSMS struct {
	Sender    string
	Body      string
	Timestamp time.Time
	RawPDU    string
	Concat    *ConcatInfo
}

// ConcatInfo —— 保持对外形状;smscodec 返回 Seq,facade 映射成 Part。
type ConcatInfo struct {
	Ref   int
	Part  int
	Total int
}

// DecodeDeliver 解析 AT+CMGL/+CMGR 返回的 hex PDU(含 SCA 前缀)。
// 先剥 SCA 头(首字节 = SCA 八位组长度,跳过 1+len),再把裸 TPDU 委托给
// smscodec.DecodeDeliverTPDU。
func DecodeDeliver(hexPDU string) (*DecodedSMS, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(hexPDU))
	if err != nil {
		return nil, fmt.Errorf("pdu hex: %w", err)
	}
	// 剥 SCA 头(sms.Unmarshal 要裸 TPDU,不要完整 PDU)
	if len(raw) > 0 {
		scaLen := int(raw[0])
		if len(raw) > scaLen+1 {
			raw = raw[scaLen+1:]
		}
	}
	sender, text, ts, concat, err := smscodec.DecodeDeliverTPDU(raw)
	if err != nil {
		return nil, err
	}
	d := &DecodedSMS{
		Sender: sender, Body: text, Timestamp: ts,
		RawPDU: strings.ToUpper(strings.TrimSpace(hexPDU)),
	}
	if concat.IsConcat {
		d.Concat = &ConcatInfo{Ref: concat.Ref, Part: concat.Seq, Total: concat.Total}
	}
	return d, nil
}
```

> **SCA 关键结论(实施时核实,2026-07-12)**:`sms.Unmarshal`(smscodec 内部调用)要的是**裸 TPDU**,不是完整 PDU——直接喂含 SCA 前缀的 PDU 会导致 SCTS 字段错位、`bcd: invalid octet` 报错。**必须先剥 SCA**(首字节 = SCA 八位组长度,TPDU 从 `1+scaLen` 开始)。vohive 的 `manager.go:1664-1670` 正是这么做的("跳过 SMSC 头部")。SG 原 `DecodeDeliver` 也先剥 SCA。本计划初稿误以为 sms.Unmarshal 消费完整 PDU——已纠正,DecodeDeliver 内含 SCA 剥离。

**删除 `EncodeSubmit` / `SubmitUDH` 类型**(被 Step 4 的多段发送取代)。

### Step 4 — 重写 `sms.go:Send`(多段循环)

当前(`sms.go:456-490`):编 1 个 PDU → 单次 CMGS。改为:

```go
// Send 编码并发送短信。长短信由 smscodec 自动分段(>160 GSM-7 / >70 UCS-2),
// 每段独立走一次完整 CMGS 两步握手。
func (m *Modem) Send(ctx context.Context, recipient, body string) error {
	tpdus, _, err := smscodec.BuildSubmitTPDUs(recipient, body)
	if err != nil {
		return fmt.Errorf("encode submit: %w", err)
	}
	for i, tpdu := range tpdus {
		// smscodec 返回裸 TPDU(不含 SMSC);AT+CMGS 需要 SCA 前缀,
		// 00 = "用 SIM 里存的 SMSC"(与原 SG EncodeSubmit 一致)。
		hexPDU := "00" + hex.EncodeToString(tpdu)
		tpduLen := len(tpdu)
		if err := m.sendOneSegment(ctx, hexPDU, tpduLen); err != nil {
			return fmt.Errorf("segment %d/%d: %w", i+1, len(tpdus), err)
		}
		// 段间间隔,防模组队列溢出(借鉴 vohive SendSMSWithOptions 的 500ms)。
		if i < len(tpdus)-1 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// sendOneSegment 执行单段 CMGS 两步握手(原 Send 的 body,抽出复用)。
func (m *Modem) sendOneSegment(ctx context.Context, hexPDU string, tpduLen int) error {
	if _, err := m.SendAndWait(ctx, fmt.Sprintf("AT+CMGS=%d", tpduLen), 5*time.Second); err != nil {
		return fmt.Errorf("CMGS init: %w", err)
	}
	c := &call{cmd: "", done: make(chan callResult, 1), timeout: 30 * time.Second}
	m.mu.Lock()
	if err := m.SendRaw([]byte(hexPDU)); err != nil { m.mu.Unlock(); return err }
	if err := m.SendRaw([]byte{0x1A}); err != nil { m.mu.Unlock(); return err }
	m.pending <- c
	m.mu.Unlock()
	select {
	case res := <-c.done:
		return res.err
	case <-time.After(30 * time.Second):
		return errors.New("CMGS body: timeout waiting for OK")
	case <-ctx.Done():
		return ctx.Err()
	case <-m.closed:
		return errors.New("modem closed")
	}
}
```

**关键差异**:
- 签名:`Send(ctx, recipient, body, udh SubmitUDH)` → `Send(ctx, recipient, body)`(删 `udh`,自动分段)
- 编码:`EncodeSubmit`(单段)→ `BuildSubmitTPDUs`(多段)+ 手工 `00` SCA 前缀
- 循环:每段一次完整 CMGS 握手;段间 500ms 间隔
- 抽出 `sendOneSegment` 复用原两步握手逻辑

### Step 5 — 更新调用方

| 文件 | 改动 |
|---|---|
| `internal/usbtransport/sms_hardware_test.go:131` | `m.Send(ctx, recipient, body, modem.SubmitUDH{})` → `m.Send(ctx, recipient, body)` |
| `internal/usbtransport/sms_hardware_test.go:97` | **不变**(`modem.DecodeDeliver(sm.PDU)` facade 保留同名同签名) |

全仓 grep 确认仅这两处 + `sms.go:Send` 内部用到被删 API(`EncodeSubmit`/`SubmitUDH`)——已核实(`docs/05-07` + 本仓扫描)。

### Step 6 — 清理

- 删除 `third_party/sms-gateway/modem/pdu.go` 里所有手写编解码内部函数(facade 只留类型 + DecodeDeliver + 可能的 buildSubmitPDUs 内部 helper)
- 删除 `import "unicode/utf16"` / `encoding/hex`(facade 不再直接用)/ `strings`(若 facade 用到则保留)
- `third_party/sms-gateway/modem/AGENTS.md`:把"3. PDU 三缺陷(未修复)"改为"已升级 smscodec,三缺陷消除",指向本计划

---

## 五、Reassembler(可选,长短信接收重组)

`smscodec.Reassembler`(`reassembler.go`)是状态ful 的分片缓存:

```go
func (r *Reassembler) Add(sender string, concat ConcatInfo, content string) (complete bool, fullContent string)
```

- `concat.IsConcat == false` → 直接返回(单条短信)
- 多段 → 按 `sender_ref` 缓存,收齐 `Total` 段后按 `Seq` 排序拼接

**接入点**:`DecodeDeliver` facade 当前只返回 `ConcatInfo`(单段视角)。重组是**消费者侧**状态机(需要跨多条短信累积),不适合塞进无状态的 `DecodeDeliver`。两条接入路径:

1. **阶段 1 存量读取**(ListStored 一次性读全部):消费者自行 `NewReassembler()` + 遍历 `DecodeDeliver` 结果逐条 `Add`。本计划在测试里演示。
2. **后续 +CMTI 实时接收**:把 `*smscodec.Reassembler` 作为 `Modem` 字段,在 +CMTI URC 处理回调里 `Add`。属阶段 2(URC 编排),**非本计划范围**,但 Reassembler 已就位。

> 阶段 1 SIM 上的 3 条实测短信都是单段,Reassembler 暂不触发。本计划保留能力,接入留到需要时。

---

## 六、验证

### 6.1 离线单测(无硬件,CI 友好)

按 `AGENTS.md` 测试约定(标准库 testing / table-driven / 同包同目录),为 facade + smscodec 复制件写离线测试:

| 测试 | 内容 | 向量来源 |
|---|---|---|
| `TestDecodeDeliver_ChineseUCS2` | 解码中文 UCS-2 短信,sender/body/timestamp 正确 | 阶段 1 实测的 3 条 PDU(从 `sms_hardware_test.go` 实测日志提取) |
| `TestDecodeDeliver_GSM7Extension` | 解码含扩展字符 `{}[]~\|` 的短信,**不再丢字**(升级前变 `?`) | 标准 GSM-7 扩展表测试向量(TS 23.038 §6.2.1) |
| `TestBuildSubmit_MultiPart` | 编码 >160 字符长短信,返回 ≥2 段;每段 TPDU 长度正确 | 自构:161 字符 ASCII |
| `TestBuildSubmit_UCS2Auto` | 中文 body 自动选 UCS-2(无需手工指定) | 自构:"测试" |
| `TestBuildSubmit_SCAPrefix` | 输出 hexPDU 以 `00` 开头(SCA 占位),tpduLen = 裸 TPDU 长度 | 断言 hexPDU[:2]=="00" |
| `TestReassembler_TwoPart` | 两段分片 Add 后 complete=true,拼接顺序正确 | smscodec 原有 `reassembler_test.go` 向量 |

`third_party/smscodec/` 自带的 `reassembler_test.go` / `pdu_encoding_test.go` / `pdu_trim_test.go` 选择性复制(去掉依赖被裁函数的用例),作为回归基线。

### 6.2 硬件集成测试(`-tags=hardware`)

复用现有 `sms_hardware_test.go`,升级后跑:
- `TestHardwareSMSListStoredAndDecode` —— 3 条中文短信解码(回归,facade 同签名)
- `TestHardwareSMSSend` —— 发送(签名变了,去掉 SubmitUDH 实参)
- **新增** `TestHardwareSMSMultiPartSend`(可选):发一条 >160 字符长短信到 `DJI_TEST_SMS_RECIPIENT`,验证多段 CMGS 循环

### 6.3 验证命令

```bash
# 离线(新增 facade/smscodec 测试)
mise exec -- go test -race ./third_party/smscodec/... ./third_party/sms-gateway/modem/...
mise exec -- go test -race ./internal/usbtransport/        # mock 测试(DecodeDeliver facade)

# 硬件(需 EC25 + WinUSB)
mise exec -- go test -tags=hardware -run 'SMS' -v ./internal/usbtransport/
```

---

## 七、风险与回退

| 风险 | 缓解 |
|---|---|
| ~~warthog618 `sms.Unmarshal` 的 SCA 消费行为与预期不符(decode 乱码)~~ **(已解决)** | 实施时核实:`sms.Unmarshal` 要裸 TPDU,DecodeDeliver facade 内已剥 SCA 头(见 Step 3 SCA 结论)。离线测试 `TestDecodeDeliver_GSM7Basic` 通过 |
| `BuildSubmitTPDUs` 输出裸 TPDU,手工 `00` 前缀与某固件不兼容 | 硬件 Send 测试先发单段 ASCII(等价原行为);通过后再测多段 |
| 段间 500ms 间隔对 DJI 百望不够 / 太长 | 先验证单段;多段失败时调间隔(参考 vohive 500ms) |
| `go mod tidy` 意外拖入 vohive 内部包 | tidy 后检查 go.sum,确认仅 warthog618 链 |

**回退**:本计划只动 `pdu.go`(改为 facade)+ `sms.go:Send` + 1 处测试。若升级出问题,`git revert` 单次提交即回到路线 A(SG 手写 pdu.go 完整保留在 git 历史里)。建议 Step 1-6 在**单个分支 / 单次提交序列**完成,保持回退原子性。

---

## 八、决策记录

**复制范围:verbatim 全量**(已决策,2026-07-12)

选定整包 verbatim 复制(~2000 行),理由:匹配 `third_party/sms-gateway/` 一行不改的复制先例;binary_classifier/wbxml_omacp 无害(仅 8-bit DCS 触发)且免费获得未来 OMA CP 能力;以后 vohive 更新直接覆盖。§2.4 / Step 1 已据此落地。

---

## 九、不在本计划范围

- **CSCA / CMGR / CSCS 命令**(B 类 SMS 命令族剩余 3 个)——壳的 AT 编排能力,与 PDU 芯无关,后续单独补
- **+CMTI 实时接收 + 自动重组编排**——属阶段 2(URC 系统),Reassembler 已就位但接入留后
- **OMA CP / WAP Push / 二进制短信解码**——verbatim 已随包带入代码,但本计划不写测试验证(文本短信够用);需要时 binary_classifier + wbxml_omacp 已就位
- **国产模组 PDU 容错的实战调参**——pdu_trim.go 原样带入,但对 DJI 百望(EC25)是否需要 spare bit 清零待实测

---

## 十、相关文件

- `docs/05` —— SG modem 包剖析(PDU 三缺陷 §7.4)
- `docs/06` —— vohive smscodec 包剖析(§七 PDU 编解码标准符合度)
- `docs/07` —— 对比 + 选型(§五路线 B 定义)
- `plans/usb-transport.md` —— 阶段 1 实施计划(本计划的前置,SG 壳接入)
- `third_party/sms-gateway/modem/AGENTS.md` —— 当前 SG 壳的状态记录(PDU 三缺陷 §3)
