# third_party/sms-gateway/ — 从 sms_gateway 复制的 AT 协议层

本目录是第三方代码副本,**不是本项目原创**。从上级 `vohive-release/source/sms_gateway/` 复制而来。

## 来源与许可

- **上游**:`source/sms_gateway/`(AGPL-3.0)
- **复制范围**:`agent/internal/modem/` 的 3 个生产文件(modem.go / sms.go / pdu.go)
- **LICENSE**:AGPL-3.0 全文随复制(见本目录 `LICENSE`)
- **合规约束**:AGPL-3.0 要求网络服务也必须开源源码。本项目当前为研究性质,若将来分发/部署需注意 AGPL 传染性

## 为什么复制而非 replace / go.mod 依赖

详见 `docs/05` 第八节 + `plans/usb-transport.md` §2.1。简述:
- **可移植**:无绝对路径依赖(换机器/移动目录/CI 都断)
- **可改**:需要对源码做 USB transport 适配(加 `NewFromIO` + `port` 字段接口化),replace 改的是上游源码不如复制改副本
- **依赖最小**:只复制 modem 子包,不拖 sms_gateway 的 backend HTTP / eSIM / OTA / config 等无关代码

## 改动记录(相对上游)

**仅 modem.go 改动 2 处**,sms.go / pdu.go 零改动:

1. `Modem.port` 字段类型:`serial.Port` → `io.ReadWriteCloser`
   - `serial.Port` 是 `io.ReadWriteCloser` 的超集(含 Read/Write/Close + SetMode 等串口专属方法),所以 Open 路径(走 `serial.Open`)继续工作
2. 加 `NewFromIO(io.ReadWriteCloser) *Modem` 构造函数
   - 跳过 `serial.Open`,直接注入任意 transport(如 USB endpoint)
   - 调用方负责确保 Read 有短超时轮询语义(见 `internal/usbtransport`)
3. **readLine 修正**(CMGS 提示符 bug,2026-07-12 发现):`readLine` 在部分读(无 `\n`)时若 TrimSpace 后是 `>`,当一行返回。上游在 USB CDC-AT 场景有此 bug,详见 `modem/AGENTS.md`

**保留不动的**(决策记录,见 `plans/usb-transport.md` 三个"保留"选项):
- ICMP ping 代码(`modem.go` ~210 行)—— 阶段 1 不用,留着不碍事
- zerolog 日志 —— 不替换成 stdlib,保持与上游一致
- `Open` 串口路径 —— 保留兼容,虽然 USB 方案不用

## 目录结构

```
third_party/sms-gateway/
├── LICENSE           # AGPL-3.0
├── AGENTS.md         # 本文件
└── modem/            # AT 协议层包(见 modem/AGENTS.md)
    ├── modem.go      # Modem 结构体 + readerLoop + SendAndWait + NewFromIO(改动)
    ├── sms.go        # Initialize/Send/ListStored/DeleteStored + ICCID/IMEI/Carrier 查询
    └── pdu.go        # SMS PDU 编解码(GSM-7/UCS-2/UDH,手写自包含)
```

## 同步上游

将来若 sms_gateway 上游有更新(如 PDU 编解码修复、新 AT 命令):
1. `diff source/sms_gateway/agent/internal/modem/<file> third_party/sms-gateway/modem/<file>`
2. 手动 port 变更到副本(注意保留本项目的改动:NewFromIO + port 接口化 + readLine 修正)
3. 跑 `make test-race` 确认不回归
4. 跑 hardware test 确认硬件行为不回归

## 不复制的内容

sms_gateway 里这些子目录**不复制**(与 USB modem 方案无关):
- `agent/internal/runner/` —— HTTP backend 编排层
- `agent/internal/esim/` —— eUICC 管理
- `agent/internal/client/`、`config/`、`state/`、`ota/` —— agent 框架
- `agent/cmd/` —— SMS gateway 服务入口
