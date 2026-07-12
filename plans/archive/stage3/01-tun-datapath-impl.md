# 子计划 01 — TUN + relay 实现

> 隶属 `plans/stage3-tun-internet.md`(总览)。**阶段 3 核心**(数据中继层)。
> 依赖子计划 00 通过(raw-IP 确认 + WDA 确认 + ZLP 结论)。

---

## 一、目标

新建 `internal/qmidatapath/` 包,实现 TUN 虚拟网卡与 QMI bulk endpoint 之间的双向 raw IP 中继。上游 quectel-qmi-go 没有这层(依赖内核 qmi_wwan),纯用户态必须自己写。

---

## 二、依赖

- **子计划 00 通过**:raw-IP 确认(首字节 IP version=4/6)。如果探针发现是 QMAP,本计划 §六加 strip/add 层。
- **子计划 00 D2 ZLP 结论**:`zlp bool` 传入 Bridge 配置
- 引入 `golang.zx2c4.com/wireguard/tun`

```bash
mise exec -- go get golang.zx2c4.com/wireguard
```

模块 `golang.zx2c4.com/wireguard`,子包 `tun`。Windows 隐式依赖 `golang.zx2c4.com/wintun`(运行时需 wintun.dll)。

---

## 三、TUN API 关键点

```go
type Device interface {
    Read(bufs [][]byte, sizes []int, offset int) (n int, err error)
    Write(bufs [][]byte, offset int) (int, error)
    Name() (string, error)
    MTU() (int, error)
    Events() <-chan Event
    Close() error
    BatchSize() int
}
func CreateTUN(ifname string, mtu int) (Device, error)
```

- **批量接口**:`bufs [][]byte`,`sizes []int`。Read 返回包数 n,长度写 sizes[0..n-1]
- **offset**:macOS utun 需 4 字节 headroom(protocol family 头),用 offset=4;Linux/Windows offset=0。三平台用 4 通用安全
- **Windows BatchSize()=1**:每次只读/写 1 个包,relay 按单包处理(跨平台一致)
- **macOS 命名不受控**:传 "qmi0" 被改成 "utunN",配置时用 `Device.Name()` 实际返回值

---

## 四、包结构

```
internal/qmidatapath/
├── bridge.go          # Bridge 结构体 + Start/Stop 生命周期
├── relay.go           # 双向中继(bulk IN→TUN, TUN→bulk OUT)
├── relay_test.go      # mock 单测(注入 fake BulkReader/Writer + fake TUN)
└── relay_hardware_test.go  # 硬件集成测试(build tag: hardware)
```

---

## 五、实现

### 5.1 接口抽象(可测性,对齐 usbtransport/qmitransport 模式)

```go
package qmidatapath

import "context"

// BulkReader abstracts the gousb IN endpoint (EP 0x88). gousb *InEndpoint satisfies this.
type BulkReader interface {
    ReadContext(ctx context.Context, buf []byte) (int, error)
}

// BulkWriter abstracts the gousb OUT endpoint (EP 0x05). gousb *OutEndpoint satisfies this.
type BulkWriter interface {
    Write(buf []byte) (int, error)
}

// tunDevice abstracts wireguard/tun.Device for testing.
type tunDevice interface {
    Read(bufs [][]byte, sizes []int, offset int) (int, error)
    Write(bufs [][]byte, offset int) (int, error)
    Name() (string, error)
    Close() error
    BatchSize() int
}
```

### 5.2 Bridge 结构体

```go
type Bridge struct {
    tun     tunDevice      // 注入(非内部创建),测试时用 fake TUN
    bulkIn  BulkReader
    bulkOut BulkWriter
    offset  int            // macOS=4, 其他=0
    mtu     int            // 1500
    zlp     bool           // 子计划 00 D2 结果:是否需 ZLP

    cancel  context.CancelFunc
    wg      sync.WaitGroup
    mu      sync.Mutex
    started bool
}

// New creates a Bridge. tun/bulkIn/bulkOut must be pre-opened. offset from
// runtime.GOOS (macOS=4). zlp from subplan 00 D2 probe result.
func New(tun tunDevice, bulkIn BulkReader, bulkOut BulkWriter, offset, mtu int, zlp bool) *Bridge

// Start launches the two relay goroutines (TUN→modem, modem→TUN). Idempotent.
func (b *Bridge) Start(ctx context.Context) error

// Stop cancels the relay context and waits for both goroutines to exit.
// Does NOT close the TUN — caller owns TUN lifecycle.
func (b *Bridge) Stop() error
```

**设计要点**:TUN 作为接口注入而非 Bridge 内部创建——relay 逻辑完全离线可测(不需要管理员权限/Wintun.dll)。调用方负责 `tun.CreateTUN()` + `bridge.Start()` + `bridge.Stop()` + `tun.Close()`。

### 5.3 relay 双向中继(核心 ~100 行)

```go
// Flow 1: TUN → modem (上行)
func (b *Bridge) tunToModem(ctx context.Context) {
    defer b.wg.Done()
    batchSize := b.tun.BatchSize()
    if batchSize < 1 { batchSize = 1 }
    bufs := make([][]byte, batchSize)
    sizes := make([]int, batchSize)
    for i := range bufs {
        bufs[i] = make([]byte, b.mtu+b.offset)
    }
    for {
        select {
        case <-ctx.Done(): return
        default:
        }
        n, err := b.tun.Read(bufs, sizes, b.offset)
        if err != nil {
            if ctx.Err() != nil { return }
            log.Printf("qmidatapath: TUN read error: %v", err)
            continue
        }
        for i := 0; i < n; i++ {
            pkt := bufs[i][b.offset : b.offset+sizes[i]]
            if _, err := b.bulkOut.Write(pkt); err != nil {
                if ctx.Err() != nil { return }
                log.Printf("qmidatapath: bulk OUT write error: %v", err)
                continue
            }
            // R5 ZLP: 如果 pkt 长度是 bulk OUT maxPacketSize(512)整数倍,追加 0 字节
            if b.zlp && len(pkt)%512 == 0 {
                b.bulkOut.Write([]byte{})
            }
        }
    }
}

// Flow 2: modem → TUN (下行)
func (b *Bridge) modemToTun(ctx context.Context) {
    defer b.wg.Done()
    buf := make([]byte, 65535)  // R4: 大 buffer,一次 bulk transfer 可含完整 IP 包
    outBuf := make([]byte, b.mtu+b.offset)
    for {
        select {
        case <-ctx.Done(): return
        default:
        }
        n, err := b.bulkIn.ReadContext(ctx, buf)
        if err != nil {
            if ctx.Err() != nil { return }
            log.Printf("qmidatapath: bulk IN read error: %v", err)
            continue
        }
        if n == 0 { continue }
        // raw-IP: 直接转发(buf[:n] 就是裸 IP 包)
        copy(outBuf[b.offset:], buf[:n])
        b.tun.Write([][]byte{outBuf[:b.offset+n]}, b.offset)
    }
}
```

### 5.4 并发安全 + Close 时序

- bulk EP 的 Read/Write 与 QMI 控制面(EP0+interrupt 0x89)**不同 endpoint,无竞争**
- relay goroutine 只操作 bulk EP,QMITransport.ioMu 只保护 EP0 control transfer
- **Close 时序(严格,防 segfault)**:
  1. `Bridge.Stop()` → cancel relay context → `wg.Wait()` 等两个 goroutine 退出
  2. `QMITransport.Close()` → 释放 USB iface
  3. 绝不能反过来——释放 iface 时 relay 还在读写 → segfault(issue/001 类)

---

## 六、QMAP 降级(如果子计划 00 发现非 raw-IP)

如果探针发现首字节是 `0x00-0x07`(mux_id 而非 IP version),是 QMAP 模式。relay 加 strip/add 层:

```go
// QMAP RX 头(4字节大端):[mux_id(1)][flags(1)][pkt_len_be16(2)][IP payload]
// RX (modem→TUN): 剥前 4 字节
qmapPkt := buf[:n]
if qmapPkt[0] <= 0x7f {  // mux_id, QMAP
    pktLen := int(binary.BigEndian.Uint16(qmapPkt[2:4]))
    ipPkt := qmapPkt[4 : 4+pktLen]
    // 转发 ipPkt 到 TUN
}
// TX (TUN→modem): 加 4 字节头
hdr := []byte{muxID, 0x00, 0x00, 0x00}
binary.BigEndian.PutUint16(hdr[2:4], uint16(len(ipPkt)))
bulkOut.Write(append(hdr, ipPkt...))
```

muxID 来自拨号时 WDS BindMuxDataPort 分配(单 PDN 通常 0x81)。本计划默认 raw-IP,QMAP 是降级分支。

---

## 七、完成标志

- [x] `internal/qmidatapath/relay.go` 编译通过(Bridge + relay 合一文件,~200 行)
- [x] mock 单测(`relay_test.go`):8 个测试(双向转发、ZLP、offset、Close 时序、context cancel、race)
- [x] Bridge.Start/Stop 生命周期正确(Stop 等 goroutine 退出)
- [x] raw-IP 直传路径工作(子计划 00 确认 raw-IP)
- [x] QMAP 降级代码在计划 §六有完整 strip/add 参考(raw-IP 确认后不需要)

### 实现说明

- Bridge + relay 合并为单文件 `relay.go`(代码量小,拆两文件无收益)
- Close 时序发现:`tun.Read` 不响应 context,需先 `tun.Close()` 再 `bridge.Stop()`
  (Bridge.Stop 调用 `wg.Wait()` 等待 tunToModem 退出;tunToModem 阻塞在 Read 直到 TUN 关闭)
- `zlp=true` 默认(子计划 00 D2 确认 QDC507 需要 ZLP)

---

## 八、风险

| 风险 | 缓解 |
|---|---|
| bulk Read 包边界(R4):一次 Read 含多包粘连 | raw-IP 下通常每 URB=一包;若粘连按 IP 头 length 字段拆。先实测 |
| TX ZLP(R5):512 倍数包卡 | `b.zlp` 标志从子计划 00 D2 取。gousb 0 字节 Write 行为需实测 |
| Windows BatchSize=1 性能 | 单包循环,4G LTE Cat4(~150Mbps)够用,非瓶颈 |
| Close 竞态 | 严格时序:Stop relay → QMITransport.Close |

---

## 九、相关文件

- `internal/qmitransport/bulkendpoints.go` —— 子计划 00,OpenBulkEndpoints 返回的 `*gousb.InEndpoint`/`*OutEndpoint` 天然满足 BulkReader/Writer
- `internal/qmitransport/qmitransport.go` —— iface 字段(同包 OpenBulkEndpoints 用)
- `internal/usbtransport/usbtransport.go` —— 方向F + ioMu 模式参考(Close 时序设计)
