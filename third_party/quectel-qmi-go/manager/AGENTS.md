# third_party/quectel-qmi-go/manager/ — QMI 连接管理器(全功能拨号编排)

> 从 `source/quectel-qmi-go/pkg/manager/` 复制(2026-07-12)。
> 子计划 07 决策:复制全包(非手搓)。

## 来源与许可

- **上游**:`source/quectel-qmi-go/pkg/manager/`
- **许可**:⚠️ 同 qmi 包,上游无 LICENSE 文件(见 `../LICENSE`)
- 复制方式:整包复制(28 个 .go 文件,~13K 行),import 路径改为本地模块

## 作用

全功能 QMI 连接管理器,提供:
- **状态机**:Disconnected → Connecting → Connected → Stopping
- **自动拨号**:CTL GetClientID → WDA SetDataFormat → WDS StartNetwork → 拿 PDH + IP
- **自动重连**:modem-reset + retry 退避 + indication 触发恢复
- **QMAP 多 PDN**:BindMuxDataPort
- **三平台 netcfg**:SetIPAddress/AddDefaultRoute/SetMTU/UpdateDNS
- **SMS/语音/UIM**:全 service 支持(WMS/VOICE/UIM/IMSA/IMSP)

## USB 注入点(核心)

`usb_entry.go` → `NewWithClient(cfg, logger, client)`:
- 利用 manager 已有的 `openClientAndAllocateServicesHook`(manager.go:285)
- Hook 绕过 `/dev/cdc-wdm0` 打开,直接注入预构造的 `*qmi.Client`
- Manager 接管 client 所有权:Stop/失败时 cleanup() 调 client.Close()

```go
transport, _ := qmitransport.Open()
client, _ := qmi.NewClientFromTransport(ctx, transport, qmi.DefaultClientOptions())
mgr := manager.NewWithClient(cfg, nil, client)
mgr.Start()    // CTL/WDA/WDS service 分配
mgr.Connect()  // WDS 拨号,拿 PDH + IP
```

## 依赖

- `qmi`(本地)— QMI 协议栈
- `netcfg`(本地)— 三平台网卡配置(Linux 用 iniwex5/netlink,Windows/macOS 各自实现)
- `device`(本地)— Linux sysfs 设备发现(Windows 不用,hook 绕过)
- `github.com/sirupsen/logrus` + `go.uber.org/zap` — logger(新增 go.mod 依赖)
- `github.com/warthog618/sms` — SMS 编解码(已有)

## 关键文件

| 文件 | 作用 |
|---|---|
| `manager.go` (4549 行) | 核心状态机 + 拨号 + 重连 + service 编排 |
| **`usb_entry.go`** | **新增**:`NewWithClient` USB 注入点 |
| `pool.go` | 多 modem 连接池(不用,但保留) |
| `callbacks.go` | 事件回调(连接/断开/IP 变化) |
| `snapshot.go` | 设备信息快照(IMEI/IMSI/ICCID/信号) |
| `service_recovery.go` | service 超时恢复 |
| `smsc.go` / `sms_trim.go` | SMSC 地址 + PDU trim |
| `uim_readiness.go` / `uim_recovery.go` | SIM 卡就绪检测 + 恢复 |
| `device_query.go` | 设备信息查询(型号/固件/序列号) |
| `logger.go` | logrus/zap Logger 接口 |

## 测试

- `go test ./third_party/quectel-qmi-go/manager/` — 全部通过(离线 mock)
- `device/discovery_static_test.go` — build tag: linux(sysfs 路径含冒号,Windows 不编译)

## 与子计划 05 的关系

子计划 05(WDA+WDS 拨号)改为:用 manager.Start() + manager.Connect() 代替手搓 service 调用。
manager 自动处理 CTL GetClientID、WDA SetDataFormat、WDS StartNetwork 全流程。
