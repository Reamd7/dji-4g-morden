# 参考文档索引

> 本目录（`dji-modem-research/docs/`）是纯调研报告。实际部署教程、脚本、二进制资产、源码仓库都在上级目录 `vohive-release/`。
>
> 路径关系：`vohive-release/dji-modem-research/docs/` → 上级是 `vohive-release/`

---

## 上级目录（vohive-release/）文档

| 文档 | 相对路径 | 内容 |
|---|---|---|
| 全栈教程 | `../TUTORIAL.md` | 13 章完整教程（刷机→NAS→Windows→短信→排障→源码分析），2000+ 行 |
| NAS 避坑手册 | `../SMS_GATEWAY_FNOS_PITFALLS.md` | 16 个实测坑 + 部署流程 + 排障速查 |
| 刷模块教程 | `../DJI_Baiwang_flash_EC25_tutorial.md` | DJI → EC25 刷写（备份+刷写+恢复+排障） |
| Windows 教程 | `../DJI_Baiwang_Windows_4G_Tutorial.md` | Windows 4G 上网 + 收发短信 |
| 目录总纲 | `../outline.md` | 文件索引速查 |

## 上级目录（vohive-release/）资产

### dji-4g-vohive-mac/

VoHive 二进制（3 架构）+ 离线包 + 安装脚本 + 配置参考 + qmi_wwan 重建说明。

### windows-4g/

Windows 短信工具（`send_sms.js` / `read_sms.js`）+ Quectel 驱动安装包 + npm 依赖。

### source/

5 个源码仓库 + 深度分析：

| 目录 | 说明 |
|---|---|
| `source/vohive-collection/` | VoHive 全套依赖源码快照（quectel-qmi-go + uicc-go + euicc-go + netlink + qqbot + swu-go + vohive 主仓） |
| `source/vohive/` | VoHive 主仓（source-available，4 个私有依赖） |
| `source/vowifi-go/` | vowifi-go 开源复刻（boa-z） |
| `source/sms_gateway/` | sms-gateway 源码（AGPL-3.0 真开源） |
| `source/dji-cellular-as-modem/` | RNDIS 免驱方案 + USB 拓扑研究 |
| `source/quectel-qmi-go/` | quectel-qmi-go 独立副本（和 collection 里的一样） |
| `source/SimAdmin/` | Rust 模组管理平台（绑 ModemManager D-Bus，不可跨平台复用） |

---

## 本目录（dji-modem-research/docs/）文档

| 文档 | 内容 |
|---|---|
| `00-reference-index.md`（本文件） | 参考文档索引，指向上级目录 |
| `01-userland-usb-modem-feasibility.md` | 不装驱动的用户态 USB modem 方案可行性研究 |
| `02-dji-modem-hardware-and-flashing.md` | DJI 模块硬件分析与刷写研究 |
| `03-source-code-analysis.md` | 源码深度分析（协议栈复用性 + 跨平台评估） |
| `04-at-command-standards.md` | uicc-go `at/` 包 AT 命令电信标准规范索引（TS 27.007/27.005、V.250、7816-4 等） |
| `05-sms-gateway-modem-analysis.md` | sms_gateway/modem 包实现剖析（AT 通道 + PDU 编解码 + transport 改造点 + 复用评估） |
| `06-vohive-modem-analysis.md` | vohive/modem Manager + smscodec 包实现剖析（AT 通道 + PDU + URC + 复用评估） |
| `07-at-implementation-comparison.md` | 两个 AT 实现的电信标准符合度逐维度对比 + 选型建议 |
| `08-ec25-at-commands-index.md` | Quectel EC25&EC21 AT Commands Manual V1.2 全册逐命令索引 + 三阶段相关性标注 + modem 包实现状态对照 |
| `Quectel_EC25EC21_AT_Commands_Manual_V1.2.pdf` | 官方 AT 命令手册原件（2017-11-14, 231 页）。docs/08 是其索引 |
| `09-ec20-at-commands-index.md` | Quectel EC20 AT Commands Manual V1.1 全册索引 + 与 EC25 V1.2 差异对照(EC20 是子集,仅多 airplanecontrol) |
| `EC20.pdf` | EC20 官方 AT 命令手册原件(2015-07-14, 191 页)。docs/09 是其索引 |
| `10-at-commands-alignment.md` | AT 命令全集对齐状态：133 条逐命令状态(✅/🔲/🔧/⬜) + 上游溯源(SG/VH 对照) + A-E 大类评分 + 待实现优先级分组。统一对齐追踪文档 |
