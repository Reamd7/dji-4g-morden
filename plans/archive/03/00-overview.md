# 阶段 3 实施计划:TUN 虚拟网卡 + 实际上网(总览)

> 基于 `docs/01` §六阶段 3 路线图。阶段 1(AT+短信)、阶段 2(QMI 拨号)已完成。
> 创建于 2026-07-12。本计划基于本轮独立调研(两份 Explore 报告 + 用户 3 项决策)重新组织,
> 与历史 `plans/stage3/` 并存(后者不再维护)。

---

## 一、目标

把 QMI 数据通道的 raw IP 包中继到 TUN 虚拟网卡,实现真实上网(系统 ping/curl 走 4G)。

阶段 2 拿到了运营商 IP `10.147.0.1/27`(WDS StartNetwork 成功),但这个 IP 只存在于 QMI 控制层——没有实际的数据通路。阶段 3 要建立 **USB bulk EP ↔ TUN** 的双向 raw IP 中继。

**成功标准**:TUN 网卡创建 + IP/路由/DNS 配置 + 双向 relay 跑通 + 系统层 ping/curl 通过 4G。

---

## 二、核心挑战

### 1. 上游是纯控制面,数据中继从零写

`quectel-qmi-go` 的 manager 包**完全不碰 IP 数据通路**——它只做:信令(CTL/WDS/WDA/NAS...)、拿 IP(GetRuntimeSettings)、配网卡(netcfg)。标准模式下,IP 数据由内核 `qmi_wwan` 驱动在 `wwan0` 网卡和 USB bulk endpoint 之间转发。

我们是纯用户态(无内核驱动),**数据中继层必须自己写 ~250-300 行**(双向 goroutine:bulk IN→TUN / TUN→bulk OUT)。整个 Go 生态没有现成的"纯用户态 QMI 数据通路"参考,需自建。

### 2. 阶段 2 遗留:从未协商 WDA

**关键事实(本轮调研发现)**:manager 的 `shouldAllocateWDA()` 要求 `cfg.Device.NetInterface != ""`。阶段 2 的 qmidial **没有设置 NetInterface**(纯用户态无内核 wwan0),所以:
- WDA service 从未分配
- `enableRawIP()` / `wda.SetDataFormat()` 从未下发
- **数据格式(raw-IP vs QMAP)完全未知**

阶段 2 拨号成功只证明控制面 OK。阶段 3 第一步必须补 WDA 协商 + 实测数据格式(子计划 01 门控)。

### 3. MI_04 控制面与数据面共存

MI_04 是"3端点合一 + EP0"接口,阶段 2 用 EP0 control + interrupt 0x89 跑信令(模型 B)。IP 数据走 **bulk EP 0x88 IN / 0x05 OUT**(标准 qmi_wwan 行为)。两个面用不同 endpoint,**同一 iface claim,无竞争,可并行**。

### 4. raw-IP 是目标格式

WDA SetDataFormat 协商成 `LinkProtocol=0x02`(raw-IP)+ UL/DL aggregation=disabled(关 QMAP)后,bulk EP 上的数据 = **裸 IP 包**(无以太网头、无 QMUX 封装、无 QMAP 头)。TUN 库也是 layer-3(IP only)。两者格式完全一致——**直接中继即可,无需任何头处理**。

---

## 三、已确认决策(本轮 3 项)

| 决策 | 选择 | 理由 |
|---|---|---|
| 数据格式(raw-IP vs QMAP) | **先探针实测**(子计划 01 门控) | 阶段2从未协商 WDA,格式未知。同阶段1/2探针先行思路,不猜 |
| TUN↔bulk 桥接位置 | **新建独立 datapath 包**(`internal/qmidatapath/`) | 职责分离:qmitransport=信令(EP0),qmidatapath=数据(bulk)。~250行新包 |
| TUN 库 | **wireguard/tun**(`golang.zx2c4.com/wireguard/tun`) | 跨平台三平台,layer-3 与 raw-IP 直匹配,Windows Wintun 成熟 |

---

## 四、数据通路架构

```
┌──────────────┐   raw IP    ┌──────────────────┐   raw IP    ┌──────────────┐
│ Host network │ ──────────▶ │  TUN Device      │ ──────────▶ │ Modem USB    │
│  stack       │  TUN.Read   │  (wireguard/tun) │  bulk OUT   │ EP 0x05 OUT  │
│              │ ◀────────── │                  │ ◀────────── │ EP 0x88 IN   │
│              │  TUN.Write  │                  │  bulk IN    │              │
└──────────────┘             └──────────────────┘             └──────────────┘
```

- **Flow 1 (TUN → modem)**: `tun.Read()` → 裸 IP 包 → `bulkOut.Write()`
- **Flow 2 (modem → TUN)**: `bulkIn.Read()` → 裸 IP 包 → `tun.Write()`
- raw-IP 模式下两方向格式完全一致,无头处理

---

## 五、五大风险

| # | 风险 | 级别 | 缓解 |
|---|---|---|---|
| R1 | bulk EP 是否真的承载 IP 数据(从未在 QDC507 验证) | **MEDIUM-HIGH** | 子计划 01 探针:WDA+拨号后读 bulk IN 0x88,检查首字节 IP version |
| R2 | WDA SetDataFormat 在 QDC507(DJI 定制固件)是否成功 | LOW-MEDIUM | 子计划 01 先测 WDA,失败降级 QMAP(需剥 4 字节头) |
| R3 | Wintun.dll 集成(Windows) | LOW | 随 exe 同目录分发(~40KB),需管理员权限 |
| R4 | gousb bulk Read 的包边界(IP 包跨多 USB transfer) | LOW-MEDIUM | 65535 buffer + short packet 检测;libusb 单次 transfer 可承载任意长度(host 侧重组) |
| R5 | TX 方向 ZLP(512 倍数包不发 ZLP 可能卡) | MEDIUM | Linux 驱动设 FLAG_SEND_ZLP。初始不处理,卡住则 len%512==0 时追加 0 字节 Write |

---

## 六、代码结构(新增)

```
internal/
├── qmitransport/
│   ├── qmitransport.go           # 现有(信令,EP0+intr 0x89)
│   ├── bulkendpoints.go          # 新增:OpenBulkEndpoints() 返回 EP 0x88/0x05
│   └── ...(现有测试)
├── qmidatapath/                  # 新增 package(数据中继)
│   ├── bridge.go                 # Bridge 结构体 + Start/Stop 生命周期
│   ├── relay.go                  # 双向中继(bulk IN→TUN, TUN→bulk OUT)
│   ├── relay_test.go             # mock 单测(注入 mock BulkReader/Writer + fake TUN)
│   └── relay_hardware_test.go    # 硬件集成测试(build tag: hardware)
└── ...(现有 usbtransport/testutil/usbdesc)
cmd/
├── qmidial/                      # 现有,扩展:加 -tun 标志启动 TUN + relay
└── bulkprobe/                    # 新增:阶段 3 门控探针
```

预计新增代码量:**~250-300 行**(relay ~100 + bulkendpoints ~30 + bridge ~50 + 探针 ~50 + 测试)。

---

## 七、子计划索引

| # | 子计划 | 依赖 | 状态 | 文件 |
|---|---|---|---|---|
| 01 | bulk EP 数据探针(门控) | 阶段 2 完成 | 待实现 | `01-bulk-ep-data-probe.md` |
| 02 | TUN + relay 实现 | 01 通过 | 待实现 | `02-tun-relay-impl.md` |
| 03 | qmidial 集成 + 网络配置 | 02 | 待实现 | `03-integration.md` |
| 04 | 测试(mock + 硬件) | 03 | 待实现 | `04-tests.md` |
| 05 | 文档 + 提交(收尾) | 04 | 待实现 | `05-docs-and-commit.md` |

### 依赖关系

```
01[bulk EP 探针] ──raw IP 确认 + WDA 确认──▶ 02[TUN+relay]
                                                 │
                                                 ▼
                                          03[qmidial 集成] ──▶ 04[测试] ──▶ 05[收尾]
```

**01 是门控**:bulk EP 不承载 IP / WDA 协商失败 → 方案需调整(QMAP? 802.3?)。02-05 等 01 通过后才有意义。

---

## 八、关键设计结论

### TUN 库 API(wireguard/tun)

- `golang.zx2c4.com/wireguard/tun`,`CreateTUN(name string, mtu int) (Device, error)`
- `Device.Read(bufs [][]byte, sizes []int, offset int) (n int, err error)` —— 批量,offset=macOS 4字节 headroom
- `Device.Write(bufs [][]byte, offset int) (int, error)`
- Windows:BatchSize()=1,单包处理;Linux/macOS 可批量
- macOS:utun 命名不受控(传名被改 utunN,用 Device.Name() 取实际名)

### WDA 分配(阶段 2 遗留修复)

设 `cfg.Device.NetInterface = tunName`:
- `shouldAllocateWDA()` → true → 分配 WDA
- `enableRawIP()` → `wda.SetDataFormat(LinkProtocolIP, agg=disabled)` → modem 切 raw-IP
- `configureNetwork()` → netcfg 在 TUN 接口设 IP/路由/MTU

### netcfg 可直接复用

`SetIPAddress` / `AddDefaultRoute` / `SetMTU` / `BringUp` 三平台都能配 TUN 接口名(Linux netlink / Windows netsh / macOS ifconfig 都按名找,TUN 创建后即生效)。**唯独 `UpdateDNS` 不足**(Windows error stub / macOS no-op),需自建(子计划 03)。

### 并发安全

bulk EP 的 Read/Write 与 QMI 控制面(EP0+interrupt)**不同 endpoint,无竞争**。relay goroutine 只操作 bulk EP,QMITransport 的 ioMu 只保护 EP0 control transfer。Close 时:先 cancel relay context,再 QMITransport.Close()。

---

## 九、平台前置

| 平台 | 前置操作 |
|---|---|
| Windows | 下载 `wintun.dll`(amd64)放 exe 旁;以管理员运行 |
| macOS | 以 root 运行(sudo) |
| Linux | 以 root 或 CAP_NET_ADMIN 运行 |

---

## 十、实测数据参考(来自 AGENTS.md)

DJI 百望 EC25 模式(PID 0125)MI_04 QMI 通道:
- EP **0x05** OUT bulk(maxPacket=512)—— 阶段 3 数据 TX
- EP **0x88** IN bulk(maxPacket=512)—— 阶段 3 数据 RX
- EP 0x89 IN interrupt(maxPacket=8)—— 阶段 2 信令(响应通知)

阶段 2 拨号结果(控制面):IPv4 `10.147.0.1/27`、GW `10.147.0.2`、DNS `114.114.114.114, 223.5.5.5`、MTU 1500、IPv6 双栈。
