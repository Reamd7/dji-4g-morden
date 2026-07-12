# 子计划 07 — manager 复用决策(待定)

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。
> **状态:待决策,不阻塞子计划 00-06。** 阶段 2 先走手搓路径(子计划 05)验证链路,
> 本计划在链路打通后评估是否引入 manager 获得重连/QMAP/netcfg 能力。

---

## 决策问题

quectel-qmi-go 的 `pkg/manager/` 提供全功能拨号编排(自动 CTL/WDA/WDS、重连、modem-reset、QMAP、netcfg)。
阶段 2 是否复制并接入 manager,还是维持手搓 WDA+WDS(子计划 05)?

## 两条路径对比

| | 手搓 WDA+WDS(子计划 05 当前) | 复制 manager |
|---|---|---|
| 阶段 2 目标(拿 IP) | ✅ 够用 | ✅ 够用 |
| 自动重连/断网恢复 | ❌ 要自己写 | ✅ manager 内置(modem-reset + retry 退避 + indication 触发) |
| QMAP 多 PDN | ❌ | ✅ manager 内置(BindMuxDataPort) |
| 网卡配置(netcfg) | ❌ | ✅ 三平台 SetIPAddress/AddDefaultRoute/SetMTU/UpdateDNS |
| 移植性 | ✅ 好(无 Linux 依赖) | ⚠️ manager 耦合 Linux 设备发现(manager.go:3017 用 ControlPath 字符串) |
| 接入复杂度 | 低(直接调 service) | 中(manager 要改接入路径接受预构造 client) |

## manager 接入路径(若选复制)

manager 唯一开设备的点:`manager.go:3017` 调 `qmi.NewClientWithOptions(... ControlPath ...)` 走 hook。
子计划 01 导出 `NewClientFromTransport` 后,manager 接入有两种方式:

1. **替换 hook**:把 `openRawTransportHook` 指向 USB transport 构造。manager 不动,自动受益。
   但 hook 是包级 var,并发/全局状态问题。
2. **预构造 client**:给 manager 加"接受预构造 *Client"的构造路径(改 `openClientAndAllocateServices`)。
   改动小,隔离干净。

## 决策依据(待子计划 05 完成后评估)

- [ ] 子计划 05 手搓拨号是否顺利?(顺利 → manager 价值降低;坎坷 → manager 值得引入)
- [ ] 阶段 3(TUN 上网)是否需要重连/断网恢复?(需要 → manager 的重连有价值)
- [ ] netcfg 三平台网卡配置是否阶段 3 需要?(manager 的 netcfg 能省阶段 3 工作)
- [ ] manager 的 Linux 设备发现耦合能否隔离?(只复制需要的部分?)

## 步骤(决策后)

1. 子计划 05 完成后,按上依据评估
2. 若选复制:`cp -r .../pkg/manager/ third_party/quectel-qmi-go/manager/`,改接入路径(方式 2)
3. 若选手搓:本计划关闭,重连/QMAP/netcfg 按需在阶段 3 自己实现
4. 若选"部分复用":只取 manager 的重连逻辑,不取 netcfg(阶段 3 用 TUN 不需要 netcfg)

## 交付物 / 完成标志

- [ ] 决策文档(本文件更新):复制 / 手搓 / 部分复用 + 理由
- [ ] 若复制 manager:接入路径改好,manager 能用 USB client 拨号
- [ ] 若手搓:本计划标"不引入 manager",关闭

## 风险

| 风险 | 缓解 |
|---|---|
| manager 的 Linux 耦合难以隔离 | 评估时先看 manager.go:3017 的设备发现链路;可能只复制拨号编排不复制 device 包 |
| 引入 manager 增加复杂度 | 阶段 2 目标是"拿 IP",manager 是阶段 3 的能力;不急于阶段 2 |
