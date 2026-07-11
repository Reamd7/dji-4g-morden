# third_party/sms-gateway/modem/ — AT 命令 + SMS PDU 协议层

本包是 AT 协议层实现,从 sms_gateway 复制(见上级 `../AGENTS.md`)。`internal/usbtransport.ATTransport` 通过 `NewFromIO` 注入本包,完成纯用户态 USB → AT → 短信的全链路。

## 选型依据

为什么选 sms_gateway 而非 vohive/uicc-go?见 `docs/07`(标准符合度逐维度对比)。简述:
- 比 uicc-go/at:**能发裸 AT**(uicc-go 只能 CSIM)、transport 可注入
- 比 vohive/modem:AT 语法更对(ERROR 精确匹配、回显双保险、CMEE=1)、改造量更小(~25 行 vs 5 内部包剥离)
- 已知短板:PDU 三缺陷(GSM-7 扩展表/长短信自动分段/16-bit ref),阶段 1 可接受,后续按 `docs/07` 路线 B 升级到 smscodec

## 文件

| 文件 | 行数 | 职责 |
|---|---|---|
| `modem.go` | 683 | `Modem` 结构体、并发模型、`readerLoop`、`SendAndWait`/`SendRaw`、`NewFromIO`(本副本新增)、ICMP ping |
| `sms.go` | 490 | `Initialize`/`Send`/`ListStored`/`DeleteStored` + 设备查询(ICCID/IMEI/Carrier/PhoneNumber/SignalDBm) |
| `pdu.go` | 574 | SMS PDU 编解码(`EncodeSubmit`/`DecodeDeliver`),手写 GSM-7/UCS-2/UDH/address/SCTS |

## 架构(并发模型)

```
NewFromIO / Open
  └─ go readerLoop()              ← 唯一读 goroutine,拥有 port 的读端

readerLoop (modem.go:525)
  ├─ bufio.NewReader(m.port).ReadString('\n')   ← 内部调 transport.Read(200ms 短轮询)
  ├─ select <-m.closed → return                  ← Close 在这里生效
  ├─ 行分发:
  │    ├─ current.cmd 的回显 → 丢弃(modem.go:599,双保险之一)
  │    ├─ OK/ERROR/+CMS/+CME ERROR → 终结 in-flight call(modem.go:604)
  │    ├─ ">" (CMGS 提示符) → 单行结果返回(modem.go:617,见下方坑)
  │    ├─ 有 in-flight call → collected append
  │    └─ 无 call → dispatchURC(异步通知,如 +CMTI 新短信)
  └─ 超时/ErrNoProgress → continue(空转)

SendAndWait (modem.go:113)
  ├─ m.port.Write(cmd + "\r\n")
  ├─ m.pending <- c
  └─ select { c.done | time.After(timeout) | ctx.Done | m.closed }
```

**关键**:`m.port` 必须有 200ms 短超时轮询语义,否则 readerLoop 永久阻塞在 Read 上,无法响应 Close。USB transport 在 `internal/usbtransport` 用 `context.WithTimeout(200ms)` 实现;串口路径用 `go.bug.st/serial` 的 `SetReadTimeout(200ms)`。

## 主要 API

### 构造
- `Open(port string, baud int) (*Modem, error)` —— 串口路径(本副本保留,USB 方案不用)
- `NewFromIO(port io.ReadWriteCloser) *Modem` —— **本副本新增**,USB/任意 transport 注入口

### 高层(sms.go)
- `Initialize(ctx, pin)` —— 完整初始化:AT/ATE0/CMEE=1/CPIN?/CMGF=0/CNMI=2,1/CPMS="SM"。幂等。
- `Send(ctx, recipient, body, udh)` —— 发短信(CMGS 两步握手,见下方坑)
- `ListStored(ctx)` —— `AT+CMGL=4` 列 SIM 已存短信
- `DeleteStored(ctx, index)` —— `AT+CMGD=<index>`
- `ICCID(ctx)` / `IMEI(ctx)` / `Carrier(ctx)` / `PhoneNumber(ctx)` / `SignalDBm(ctx)` —— 设备查询
- `OnURC(h)` —— 注册 URC 处理器(如 +CMTI 新短信通知)

### 底层(modem.go)
- `SendAndWait(ctx, cmd, timeout)` —— 发任意裸 AT,等 OK/ERROR,返回响应行
- `SendRaw(p)` —— 裸写字节(如 CMGS 的 PDU + Ctrl-Z)

### PDU(pdu.go)
- `EncodeSubmit(recipient, body, udh)` —— 编码 SMS-SUBMIT PDU
- `DecodeDeliver(hexPDU)` —— 解码 SMS-DELIVER(接收的短信)

## 已知坑(实施时踩过)

### 1. CMGS ">" 提示符 —— readLine 修正(已修复,2026-07-12)

**症状**:`Send` 调用 `SendAndWait("AT+CMGS=N")` 超时,模块进入 PDU 等待态卡死后续所有 AT。

**根因**:`AT+CMGS=N` 的 `>` 提示符**以空格结尾,不以 `\n`**。模组实际返回 `\r\n> `:
- `\r\n` 被 ReadString 读成空行 → 丢弃
- `> ` 留在 bufio 缓冲区,因后续无 `\n` 永远读不出 → `line == ">"` 判断分支不可达

上游在**传统串口**不暴露此 bug:串口的短读语义(SetReadTimeout 触发部分返回)让 `> ` 能作为部分行返回。**USB CDC-AT bulk endpoint** 上数据只在模组发送时到达,`> ` 之后无数据 → ReadString 永久阻塞。

**修复**(已应用于本副本 `modem.go` 的 `readLine`):
```go
func readLine(r *bufio.Reader) (string, error) {
    line, err := r.ReadString('\n')
    if err == nil { return line, nil }
    if strings.TrimSpace(line) == ">" { return line, nil }  // CMGS 提示符特殊处理
    return line, err
}
```

**教训**:这印证了 `docs/01` 选 userland USB 的价值——完全可控,能定位协议栈底层的平台差异。串口场景隐而不发的 bug,在 USB 场景暴露并修复。

### 2. CMGS 卡死后的恢复

如果 `AT+CMGS=N` 后异常退出(如超时没发 ESC),模组会**永久卡在 PDU 等待态**,后续所有 AT 命令无响应。恢复方法:
- 发送 **ESC 字节(0x1B)** 取消 PDU 输入
- 或拔插 USB 物理复位

测试代码遇到此情况可参考 `cmd/` 下的探针(已删,但思路:`tt.Write([]byte{0x1B})` + 等 500ms)。

### 3. PDU 三缺陷(未修复,阶段 1 可接受)

来自 `docs/07` §D:
- **GSM-7 扩展表缺失**:`^{}[]~|\€` 全变 `?`(`pdu.go:4-5` 明确放弃)。收含这些字符的短信丢字。
- **长短信不自动分段**:超长报错(`pdu.go:191`),靠调用方切。发送只支持 8-bit ref(`pdu.go:161`),无 16-bit。
- **长短信不自动重组**:`DecodeDeliver` 返回 `ConcatInfo` 但不拼,调用方自己组装。

**升级路径**(`docs/07` 路线 B):引入 `pkg/smscodec/`(vohive 的,只依赖 warthog618/sms),替换 EncodeSubmit/DecodeDeliver 调用点 + 加 Reassembler。

## 不要在这里做的事

- ❌ 不要把 transport 逻辑写进本包(transport 是 `internal/usbtransport` 的职责,本包只依赖 `io.ReadWriteCloser`)
- ❌ 不要为了"清理"删 ICMP ping / zerolog / Open(三项都是故意保留的,见上级 AGENTS.md 决策记录)
- ❌ 不要改 PDU 编解码逻辑(要改就按路线 B 整体换 smscodec,别打补丁)

## 相关文档

- `docs/04` —— AT 命令电信标准(TS 27.007/27.005/V.250/23.040/23.038)
- `docs/05` —— 本包的深度剖析(transport 改造点、并发模型、PDU 缺陷)
- `docs/07` —— 与 vohive/uicc-go 的标准符合度对比 + 选型理由
- `plans/usb-transport.md` —— 本包复制 + 改造的实施计划
- `internal/usbtransport/AGENTS.md` —— transport 端的配合设计
