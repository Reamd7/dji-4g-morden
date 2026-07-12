# 子计划 05 — WDA+WDS 手搓拨号集成

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> 用 quectel-qmi-go 的 WDA/WDS service 通过 USB transport 发起拨号,拿到运营商 IP。
> **手搓路径**(不引入 manager,manager 决策见子计划 07)。

---

## 目标

验证完整链路:USB transport → quectel-qmi-go Client → WDA 配置 raw-IP → WDS 拨号 → GetRuntimeSettings 拿 IP。
这是阶段 2 的核心交付:"WDS StartNetworkInterface 成功 + 拿到有效 IP/MTU/DNS"。

## 依赖 / 前置

- **子计划 01**:`qmi.NewClientFromTransport` 可用
- **子计划 02/03**:QMITransport 实现 + 并发安全
- **子计划 00**:传输模型确定(transport 按结果实现)
- 真实 EC25 + WinUSB on MI_04 + SIM ready(PS attach)

## 拨号流程(API 已对照 wds.go/wda.go/manager.go 核实)

```go
// 1. USB transport(子计划 02)
transport, err := qmitransport.Open(0x2C7C, 0x0125, 4, 0x05, 0x88)
if err != nil { return err }
defer transport.Close()

// 2. 注入 quectel-qmi-go(子计划 01,内部 SyncOnOpen + QueryVersionOnOpen)
client, err := qmi.NewClientFromTransport(ctx, transport, qmi.DefaultClientOptions())
if err != nil { return err }
defer client.Close()

// 3. WDA: 配置 raw-IP link protocol
//    SetDataFormat 第二参是 DataFormat 结构体(非常量!);raw-IP = LinkProtocolIP(0x02)
wda, err := qmi.NewWDAService(client)  // 返回 (*WDAService, error),非 client.WDA()
if err != nil { return err }
defer wda.Close()
if err := wda.SetDataFormat(ctx, qmi.DataFormat{LinkProtocol: qmi.LinkProtocolIP}); err != nil {
	return err
}

// 4. WDS: 发起拨号
wds, err := qmi.NewWDSService(client)  // 返回 (*WDSService, error),非 client.WDS()
if err != nil { return err }
defer wds.Close()
pdh, err := wds.StartNetworkInterface(ctx, "cmnet", "", "", 0, qmi.IpFamilyV4)
// pdh = Packet Data Handle,拨号成功(非零)

// 5. 查询分配的 IP —— 第二参是 ipFamily(不是 pdh!),返回 *RuntimeSettings
settings, err := wds.GetRuntimeSettings(ctx, qmi.IpFamilyV4)
// settings 含运营商分配的 IP + DNS + 网关 + MTU
```

## API 核实要点(曾抄错,已修正)

| 调用 | 正确签名 | 来源 |
|---|---|---|
| 创建 service | `qmi.NewWDAService(client)` / `qmi.NewWDSService(client)` → `(*Service, error)` | wda.go:40 / wds.go:271 |
| SetDataFormat | `(ctx, qmi.DataFormat{LinkProtocol: qmi.LinkProtocolIP})` — **结构体,非常量** | wda.go:61,93,265 |
| StartNetworkInterface | `(ctx, apn, user, pass, authType, qmi.IpFamilyV4)` → `(uint32, error)` | wds.go:307 |
| GetRuntimeSettings | `(ctx, qmi.IpFamilyV4)` → `(*RuntimeSettings, error)` — **第二参是 ipFamily 不是 pdh** | wds.go:483 |

## 步骤

1. 创建 `cmd/qmidial/main.go`(或 hardware test),按上流程实现
2. 先验证 CTL Sync(NewClientFromTransport 内部)+ GetClientID(service 创建隐含)
3. WDA SetDataFormat(raw-IP)
4. WDS StartNetworkInterface(cmnet, <运营商> SIM)
5. GetRuntimeSettings 打印 IP/DNS/MTU
6. StopNetworkInterface 清理(pdh)

## 交付物 / 完成标志

- [ ] `NewClientFromTransport` 成功(SyncOnOpen 通过,模组响应 CTL Sync)
- [ ] `NewWDAService` + `SetDataFormat(LinkProtocolIP)` 成功
- [ ] `NewWDSService` + `StartNetworkInterface` 返回非零 PDH
- [ ] `GetRuntimeSettings` 返回有效 IPv4 + DNS + MTU
- [ ] 拨号期间无 segfault(子计划 03 的并发安全生效)

## 风险

| 风险 | 缓解 |
|---|---|
| raw-IP vs QMAP 协商 | WDA SetDataFormat 配置 LinkProtocolIP;若模组返回 QMAP,数据包有 4 字节头(阶段 3 TUN 处理,阶段 2 信令不受影响) |
| APN/鉴权配置 | cmnet(<运营商>);若需鉴权,StartNetworkInterface 的 username/password/authType 参数 |
| 拨号失败(PS attach 未就绪) | 拨号前确认 SIM ready;manager 有 ensureSIMReady,手搓路径要自己查 AT+CGATT/CREG |
| 没有 manager 的重连 | 本计划只做单次拨号验证;重连/断网恢复留待子计划 07 manager 决策 |
