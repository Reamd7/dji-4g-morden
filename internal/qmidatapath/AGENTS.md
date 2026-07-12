# AGENTS.md — internal/qmidatapath

## 包职责

TUN 虚拟网卡 ↔ QMI bulk EP 之间的双向 raw IP 中继。上游 quectel-qmi-go 是纯控制面(依赖内核 qmi_wwan 搬数据),本包在用户态实现数据搬运层。

## 核心结构

- `relay.go`(~230 行):全部代码合并在此文件
  - `BulkReader` / `BulkWriter` / `tunDevice` 接口(可测性:注入 mock)
  - `Bridge` 结构体:New(tun, bulkIn, bulkOut, offset, mtu, zlp) → Start → Stop
  - `tunToModem`:TUN.Read → bulkOut.Write(+ ZLP)
  - `modemToTun`:bulkIn.ReadContext → TUN.Write
  - `Stats()`:TX/RX 包计数器(atomic)

## 关键设计决策

### Bridge 依赖注入(非内部创建 TUN)

TUN 作为 `tunDevice` 接口注入,而非 Bridge 内部 `tun.CreateTUN()`。relay 逻辑完全离线可测——不需要管理员权限/Wintun.dll。

### ZLP 参数化(探针驱动)

`zlp bool` 从子计划 00 D2 探针结果传入。QDC507 的 bulk OUT maxPacketSize=512,当 TX 包长度是 512 整数倍时需追加 0 字节 Write(ZLP)。Linux `qmi_wwan_q.c` 的 `FLAG_SEND_ZLP` 印证。

### raw-IP 直传

WDA SetDataFormat(LinkProtocolIP)后,bulk EP 上的数据 = 裸 IP 包(无以太网头、无 QMAP 头)。TUN 也是 layer-3。relay 直接转发,无需任何头处理。

### Close 时序(防死锁)

`tun.Read` 不响应 context(阻塞直到 TUN 关闭)。必须按以下顺序清理:
1. `tun.Close()` — 解除 tunToModem 的 Read 阻塞
2. `bridge.Stop()` — cancel context + wg.Wait() 等两个 goroutine 退出
3. `QMITransport.Close()` — 释放 USB iface

**反过来会死锁**:Stop 的 wg.Wait 等 tunToModem 退出,tunToModem 阻塞在 Read,Read 等 TUN 关闭。

### offset(macOS headroom)

macOS utun 在每帧前加 4 字节 AF-family prefix。offset=4 留出 headroom,三平台通用(offset=0 on Linux/Windows 也安全)。

## 测试

- `relay_test.go`:13 个 mock 测试,88.7% 覆盖率,`-race` 通过
- `relay_hardware_test.go`:2 个硬件测试(build tag: hardware)

## 不做的事

- 不做 TCP/IP 栈(只搬 raw IP 字节)
- 不做路由配置(manager.configureNetwork 负责)
- 不做 DNS 配置(cmd/qmidial/dns.go 负责)
- 不做 QMAP 头处理(raw-IP 已确认,QMAP 是降级分支,代码在计划 §六)
