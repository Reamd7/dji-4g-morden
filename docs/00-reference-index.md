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
