# 子计划 05 — 文档 + 提交(阶段 3 收尾)

> 隶属 `plans/03/00-overview.md`。阶段 3 最后一步:记录实测结果 + 提交。
> 依赖子计划 04(测试通过)。创建于 2026-07-12。

---

## 一、目标

把阶段 3 的实测结论(数据格式、relay 验证、上网结果、ZLP 结论)写入项目长期记忆(AGENTS.md + 子目录 AGENTS.md + docs),解除 `docs/01` 的阶段 3 风险,git commit。

---

## 二、更新文档

### 2.1 主 `AGENTS.md`

实测记录段补"阶段 3 完成":
- **数据格式确认**(子计划 01):bulk EP 承载 raw-IP / QMAP(实测结果);WDA SetDataFormat 在 QDC507 上成功/失败
- **relay 实现**(子计划 02):`internal/qmidatapath/`,双向 goroutine,raw-IP 直传 / QMAP 剥头
- **ZLP 结论**(子计划 01 D2):512 倍数包是否需手动 ZLP
- **上网验证**(子计划 04):ping 114.114.114.114 走 4G 成功(实测延迟/丢包)
- **三阶段全部完成**:纯用户态 USB → AT+短信 → QMI 拨号 → TUN 上网,零内核驱动

目录结构段补:
- `internal/qmidatapath/`(新包)
- `internal/qmitransport/bulkendpoints.go`(新文件)
- `cmd/bulkprobe/`(新工具)
- `wintun.dll`(Windows 分发,如入库)
- `plans/03/`(本计划集)

依赖段补:
- `golang.zx2c4.com/wireguard`(TUN)
- `golang.zx2c4.com/wintun`(Windows,隐式)

### 2.2 子目录 AGENTS.md(新建)

- `internal/qmidatapath/AGENTS.md`:包职责(TUN↔bulk relay)、Bridge 生命周期、raw-IP 直传、ZLP、Close 时序、不做的事
- `internal/qmitransport/AGENTS.md`(更新):加 bulkendpoints.go 说明(OpenBulkEndpoints,EP 0x88/0x05,与 ioMu 关系)

### 2.3 `docs/01` 风险解除

- §8.2 QMI 数据格式(raw-IP vs QMAP):标记 ✅ 已解除(实测结果)
- §六阶段 3:标记完成

---

## 三、提交

git commit(会触发 `.githooks/pre-commit` 跑 `go test -race ./internal/...`)。

提交信息模板:
```
stage3: TUN virtual NIC + actual internet (Stage 3 milestone)

阶段 3 完成:QMI bulk EP ↔ TUN 双向 raw IP 中继,实现真实上网。

internal/qmitransport/bulkendpoints.go: OpenBulkEndpoints (EP 0x88/0x05)
internal/qmidatapath/: Bridge + 双向 relay (TUN↔bulk)
cmd/bulkprobe/: 阶段 3 门控探针 (WDA + raw-IP 确认)
cmd/qmidial -tun: 端到端上网

实测:数据格式=raw-IP,WDA=OK,relay 双向,ping 114.114.114.114 走 4G 成功
零内核驱动,三阶段全部完成。
```

---

## 四、完成标志

- [ ] AGENTS.md 实测记录 + 目录结构 + 依赖更新
- [ ] internal/qmidatapath/AGENTS.md 新建
- [ ] docs/01 §8.2 风险解除
- [ ] git commit(pre-commit race 通过)
- [ ] **阶段 3 全部完成:纯用户态 USB → 上网链路打通**

---

## 五、项目整体里程碑

阶段 3 完成后,`docs/01` 的三阶段路线图全部达成:

| 阶段 | 内容 | 状态 |
|---|---|---|
| 1 | AT 通道 + 短信 | ✅(USB transport + smscodec + AT 命令全集) |
| 2 | QMI 通道 + 拨号 | ✅(model B + DTR + manager + IPv6 双栈) |
| 3 | TUN 虚拟网卡 + 上网 | ✅(本计划) |

**核心目标达成**:纯用户态、跨平台(Win/macOS/Linux)、不依赖 Quectel 厂商驱动的 DJI 百望 4G 模组完整能力(短信 + 上网)。零内核驱动,Windows 上全程跑通。
