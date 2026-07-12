# 子计划 07 — manager 复用决策

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> **状态:✅ 已决策 — 复制 manager 全包。**

---

## 决策结果

**复制 `pkg/manager/`(~13K 行)+ `pkg/device/`(462 行)到 `third_party/quectel-qmi-go/`。**

用户决定:引入 manager 获得全功能拨号编排(自动 CTL/WDA/WDS、重连、modem-reset、QMAP、netcfg),
而非手搓 WDA+WDS。理由:manager 提供的重连/退避/service 恢复在阶段 3(TUN 上网)有价值,
且 netcfg 三平台网卡配置可省阶段 3 部分工作。

## 接入路径

新增 `manager/usb_entry.go` → `NewWithClient(cfg, logger, client)`:
- 利用 manager 已有的 `openClientAndAllocateServicesHook` 字段(manager.go:285)
- Hook 直接设置 `m.client = preConstructedClient` → 调 `allocateServices(ctx)`
- 完全绕过 `openClientAndAllocateServices` 中的 `/dev/cdc-wdm0` 打开 + Linux sysfs 代码
- Manager 接管 client 所有权:Stop/失败时 cleanup() 会 client.Close()

调用方式:
```go
transport, _ := qmitransport.Open()
client, _ := qmi.NewClientFromTransport(ctx, transport, qmi.DefaultClientOptions())
mgr, _ := manager.NewWithClient(cfg, logger, client)
mgr.Start()  // 分配 CTL/WDA/WDS 等 service
mgr.Connect()  // WDS 拨号,拿 PDH + IP
```

## 依赖变更

新增 go.mod 依赖:
- `github.com/sirupsen/logrus` v1.9.4(manager logger)
- `go.uber.org/zap` v1.28.0(manager logger)
- `go.uber.org/multierr` v1.10.0(zap 依赖)

## 风险与缓解

| 风险 | 缓解 |
|---|---|
| Linux 设备发现耦合 | `openClientAndAllocateServicesHook` 绕过;device 包仅 Linux sysfs 测试(build tag: linux) |
| netcfg Linux 依赖 | netcfg/linux.go build tag: linux;Windows/macOS 用各自实现;cleanup 中的 netcfg 调用失败不影响拨号 |
| 重连时 client 已关闭 | Hook 重用同一 client;USB 断连恢复需阶段 3 实现(重新 Open + NewClientFromTransport) |
| logrus/zap 新增依赖 | 可接受;后续可换 zerolog 统一(阶段 1 已用 zerolog) |
