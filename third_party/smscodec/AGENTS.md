# third_party/smscodec/ — SMS PDU 编解码(warthog618/sms 委托 + 国产容错)

> 从 `source/vohive-collection/vohive/pkg/smscodec/` **verbatim 复制**(2026-07-12),一行不改。
> 升级计划见 `plans/upgrade-smscodec.md`(路线 B:SG 壳 + smscodec 芯)。

## 来源与许可

- **上游**:`source/vohive-collection/vohive/pkg/smscodec/`
- **vohive 许可**:PolyForm Noncommercial 1.0.0(见本目录 `LICENSE`)
- **warthog618/sms 许可**:MIT Copyright (c) 2018 Kent Gibson(运行时依赖,见 `go.mod`)
- 复制方式与 `third_party/sms-gateway/modem/` 一致:整包 verbatim,便于上游更新时直接覆盖

## 为什么复制(不 replace / 不现写)

- `replace` 不可移植(绝对路径换机器 / CI 断),且会拖入 vohive 主仓的 config/apduarbiter/simaid/logger 内部包链
- 手写 TS 23.040 全栈(GSM-7 扩展表 / 16-bit ref UDH / 自动分段 / 重组)= 重新发明 warthog618/sms(MIT,Go 生态最完整实现)
- smscodec 在 warthog618 之上加了**国产模组容错**(pdu_trim.go:GSM-7 spare bit 清零 + PDU 长度裁剪),这是 vohive 实战积累、sms_gateway 完全没有的能力

## 文件清单

| 文件 | 作用 | 当前是否用到 |
|---|---|---|
| `pdu.go` | PDU 编解码入口:`DecodeDeliverTPDU`(收)/`BuildSubmitTPDUs`(发,自动分段)/`ConcatInfo` + RPDU 函数(SIP/HTTP 网关侧) | ✅ 用 decode/encode;RPDU 函数不用但保留(verbatim) |
| `reassembler.go` | 长短信分片重组(`Reassembler.Add`) | ✅ 能力就位,接入留阶段 2 |
| `pdu_trim.go` | 国产模组容错(spare bit 清零 + 长度裁剪),`DecodeDeliverTPDU` 内部调用 | ✅ 随 decode 自动生效 |
| `binary_classifier.go` | 8-bit 二进制短信分类(OMA CP / WAP / MMS / SIM OTA) | ⚠️ 当前不验证;仅 8-bit DCS 触发,无害 |
| `wbxml_omacp.go` | OMA Client Provisioning 的 WBXML 解码 | ⚠️ 同上,仅 binary_classifier 调用 |
| `*_test.go` | reassembler / pdu 编码 / pdu_trim 的离线回归向量 | ✅ `go test` 基线 |

## 依赖边界(已核实)

- 仅 `github.com/warthog618/sms`(+ `/encoding/tpdu`、`/encoding/ucs2`)和标准库
- **零** vohive 内部包依赖(`grep iniwex5/vohive` 无命中)——这是能整包 verbatim 复制的前提

## 不要在这里做的事

- ❌ 不要改源码(verbatim;要改就先在 `plans/` 记录理由,并评估是否破坏"上游覆盖更新"便利)
- ❌ 不要删 RPDU / binary_classifier / wbxml_omacp(verbatim 方案的明确决策,见 `plans/upgrade-smscodec.md` §八)
- ❌ 不要在本包写 transport / AT 逻辑(本包是纯 PDU 编解码,AT 层在 `third_party/sms-gateway/modem/`)

## 相关文档

- `plans/upgrade-smscodec.md` —— 复制 + 接入的实施计划(路线 B)
- `docs/06-vohive-modem-analysis.md` §七 —— smscodec 标准符合度逐维度评估
- `docs/07-at-implementation-comparison.md` §五 —— 路线 B 选型理由
- `third_party/sms-gateway/modem/AGENTS.md` —— 下游消费者(SG 壳,通过 facade 调用本包)
