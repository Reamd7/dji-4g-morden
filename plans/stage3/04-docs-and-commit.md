# 子计划 04 — 文档 + 提交(阶段 3 收尾)

> 隶属 `plans/stage3-tun-internet.md`(总览)。阶段 3 最后一步。
> 创建于 2026-07-12。

## 目标

把阶段 3 的实测结论写入项目长期记忆(AGENTS.md + docs),git commit。

## 依赖 / 前置

- **子计划 03 完成**:测试全过,端到端 ping + DNS 验证通过

## 步骤

### 1. 更新 `AGENTS.md`

#### 实测记录段补"阶段 3 完成":

- TUN 库:`golang.zx2c4.com/wireguard/tun`
- bulk EP 数据格式确认:raw IP(版本 nibble 4/6)
- WDA SetDataFormat 在 QDC507 上成功(LinkProtocol=0x02)
- relay 双向工作:bulk IN 0x88 → TUN / TUN → bulk OUT 0x05
- 端到端:ping + DNS 通过
- Wintun.dll 集成(Windows)
- 网络配置:netcfg 在 TUN 接口上设 IP/路由/DNS

#### 目录结构补:

- `internal/tunbridge/` — TUN + relay
- `cmd/bulkprobe/` — Phase 0 数据探针
- `wintun.dll`(gitignored 或文档说明放置位置)

#### 依赖列表补:

- `golang.zx2c4.com/wireguard/tun`

### 2. 更新 `internal/tunbridge/AGENTS.md`

记忆点:
- relay 设计(offset=4,批量 vs 单包,IP version 校验)
- BulkReader/BulkWriter 接口(测试注入)
- Bridge 生命周期(New → SetBulkEndpoints → Start → Stop)
- 并发安全(relay goroutines vs QMITransport ioMu,不同 endpoint 无竞争)

### 3. 更新 `docs/01`

- §六阶段 3:标"已完成",补实测结论
- §八风险 §8.2 QMI 数据格式:状态更新为"已解除"

### 4. 更新 `internal/qmitransport/AGENTS.md`

补 `OpenBulkEndpoints()` 方法说明:
- bulk IN EP 0x88 / bulk OUT EP 0x05
- 与 QMI 控制面(EP0+interrupt)共享 MI_04 claim,不同 endpoint 无竞争

### 5. git commit

```bash
git add -A
git commit  # pre-commit hook 跑 go test -race ./internal/...
```

commit message 概述:阶段 3 TUN 虚拟网卡 + 实际上网完成(bulk EP relay + TUN + 端到端验证)。

## 交付物 / 完成标志

- [ ] AGENTS.md 补阶段 3 实测记录
- [ ] internal/tunbridge/AGENTS.md 创建
- [ ] docs/01 阶段 3 标记完成 + §8.2 风险解除
- [ ] internal/qmitransport/AGENTS.md 补 OpenBulkEndpoints
- [ ] git commit 成功(pre-commit hook 通过)

## 风险

| 风险 | 缓解 |
|---|---|
| pre-commit hook 失败 | 子计划 03 的 mock 测试必须先全过 |
| 文档遗漏实测数据 | 子计划 00/02/03 的输出(bulk EP 确认/ping 结果/崩溃测试)逐项核对 |
