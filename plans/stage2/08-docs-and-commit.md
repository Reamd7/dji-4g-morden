# 子计划 08 — 文档 + 提交(阶段 2 收尾)

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。阶段 2 最后一步:记录实测结果 + 提交。

---

## 目标

把阶段 2 的实测结论(传输模型、拨号结果、issue/001 防护验证)写入项目长期记忆(AGENTS.md + docs),
解除 `docs/01` 的阶段 2 风险,git commit。

## 依赖 / 前置

- **子计划 06**:硬件测试通过(拨号拿到真实 IP)
- **子计划 00**:传输模型确定(写入 AGENTS.md)
- (可选)**子计划 07**:manager 决策已记录

## 步骤

### 1. 更新 `AGENTS.md`

- 实测记录段补"阶段 2 完成":
  - QMI 传输模型确认(A 或 B,来自子计划 00)
  - QMITransport 实现(MI_04 + 方向 F + writeMu Close 时序)
  - issue/001 在 QMI 场景的防护验证(子计划 06 的 TestConcurrentCloseNoCrash)
  - 拨号结果:PDH + IP/DNS/MTU(来自子计划 05/06)
- 目录结构补:`third_party/quectel-qmi-go/`、`internal/qmitransport/`、`cmd/qmiprobe/`

### 2. 新增/更新子目录 AGENTS.md

- `third_party/quectel-qmi-go/AGENTS.md`:复制方案 + transport_export.go 改动 + license 状态
- `internal/qmitransport/AGENTS.md`:transport 设计(模型 A/B + 方向 F + writeMu + issue/001 防护)

### 3. 更新 `docs/01`

- §六阶段 2:标"已完成",补实测结论
- §八风险:传输模型未知(§8.1)、QMI 数据格式(§8.2)的状态更新

### 4. git commit

```bash
git add -A
git commit  # .githooks/pre-commit 跑 go test -race ./internal/...
```

commit message 概述:阶段 2 QMI 拨号完成(传输模型 + transport + 拨号 + IP)。

## 交付物 / 完成标志

- [x] AGENTS.md 补阶段 2 实测记录(拨号成功 + 目录树更新 + 依赖列表)
- [x] third_party/quectel-qmi-go/ + manager/ + internal/qmitransport/ 的 AGENTS.md(3 个记忆点文件)
- [ ] docs/01 阶段 2 风险解除(留阶段 3 一起更新,非阻塞)
- [x] git commit 成功(pre-commit hook 通过)

## 风险

| 风险 | 缓解 |
|---|---|
| pre-commit hook(go test -race)失败 | 子计划 04 的 mock 测试必须先全过 |
| 文档遗漏实测数据 | 子计划 00/05/06 的输出(传输模型/IP/崩溃测试)逐项核对写入 |
