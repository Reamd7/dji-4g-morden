# 子计划 00 — PacketSink 接口 + TUN 重构(纯重构,零功能变更)

> 隶属 `plans/stage4-dual-backend.md`(总览)。**Stage 4 第一步**。
> 不依赖任何前置子计划。后续子计划 01(NetstackPacketSink)依赖本计划通过。
>
> **核心原则:纯重构,-tun 行为完全等价,所有现有测试通过。**

---

## 一、目标

把 `internal/qmidatapath/relay.go` 的 `Bridge` 从**私有** `tunDevice` 接口解耦到**导出**的 `PacketSink` 接口。解耦后:
- TUN 模式(`-tun` flag)功能完全不变
- 为子计划 01 的 `NetstackPacketSink` 留出实现入口
- relay 逻辑(双向 raw IP 中继 + ZLP)完全不变

---

## 二、依赖

- **无前置依赖**。relay.go / relay_test.go / cmd/qmidial/main.go 现状已确认(2026-07-13 调研)
- 现有约束沿用:
  - `tunDevice` 接口(私有,relay.go:51):wireguard/tun.Device 的 batch + offset 语义
  - ZLP:`zlp=true`(QDC507 bulk OUT 512 倍数追加 0 字节,subplan 00 D2 实测)
  - offset:macOS=4(utun AF-family headroom),其他=0

---

## 三、现有代码关键点(改动基准)

```go
// internal/qmidatapath/relay.go (现状)

// 私有接口 —— 外部包无法实现,netstack 不能接入
type tunDevice interface {
    Read(bufs [][]byte, sizes []int, offset int) (int, error)
    Write(bufs [][]byte, offset int) (int, error)
    Name() (string, error)
    Close() error
    BatchSize() int
}

type Bridge struct {
    tun     tunDevice     // ← 要替换为 sink PacketSink
    bulkIn  BulkReader
    bulkOut BulkWriter
    offset  int           // ← 要删除(offset 移入 TUNPacketSink)
    mtu     int
    zlp     bool
    // ...
}

func New(tun tunDevice, bulkIn BulkReader, bulkOut BulkWriter, offset, mtu int, zlp bool) *Bridge
```

**两个 relay goroutine**:
- `tunToModem`:用 `tun.BatchSize()` + `bufs [][]byte` batch read,切片 `bufs[i][offset:offset+sizes[i]]`,写 bulkOut,ZLP 在写后追加
- `modemToTun`:`bulkIn.ReadContext` 单包读,`copy(outBuf[offset:], buf[:n])` 加 headroom 后 `tun.Write`

---

## 四、实现

### 4.1 新增 `internal/qmidatapath/sink.go` — PacketSink 接口

```go
// Package qmidatapath ...
package qmidatapath

import "context"

// PacketSink is the host-side endpoint of the relay (the non-USB side).
// It exchanges raw IP packets with the relay Bridge.
//
// Implementations:
//   - TUNPacketSink (wraps wireguard/tun.Device)
//   - NetstackPacketSink (wraps gVisor channel link endpoint, 子计划 01)
//
// Contract:
//   - ReadPacket blocks until a packet is available or ctx is canceled.
//   - Close MUST unblock any pending ReadPacket (return ctx.Err() or io.EOF).
//     Otherwise Bridge.Stop() (which calls wg.Wait) will deadlock.
type PacketSink interface {
    // ReadPacket reads one raw IP packet (host → modem / uplink).
    // pkt is valid until the next ReadPacket call.
    // Returns io.EOF (or ctx.Err()) when the sink is closed.
    ReadPacket(ctx context.Context) (pkt []byte, err error)

    // WritePacket writes one raw IP packet (modem → host / downlink).
    // pkt is a bare IP packet (no TUN prefix, no QMAP header).
    WritePacket(pkt []byte) error

    // Name returns the sink's identifier for logging (e.g. "qmi0", "netstack").
    Name() string

    // Close releases the sink's resources. Must unblock pending ReadPacket.
    Close() error
}
```

### 4.2 新增 `internal/qmidatapath/tun_sink.go` — TUNPacketSink

包装现有 wireguard/tun.Device,把 batch + offset 语义封装在 sink 内部。Bridge 不再知道 offset。

```go
package qmidatapath

import (
    "context"

    "golang.zx2c4.com/wireguard/tun"
)

// TUNPacketSink wraps a wireguard/tun.Device as a PacketSink.
// It handles the TUN-specific offset (macOS utun 4-byte protocol-family prefix)
// internally so the Bridge is offset-agnostic.
//
// Note: this drops the TUN batch-read optimization (BatchSize() up to N packets
// per Read call). TUNPacketSink reads one packet at a time. This is a no-op on
// Windows (BatchSize()==1 there anyway) and negligible on Linux/macOS at 4G
// bandwidths (子计划 04 micro-batching can restore batch writes if needed).
type TUNPacketSink struct {
    dev    tun.Device
    offset int       // macOS=4, others=0
    mtu    int

    // Single-buffer reuse — Read/Write are single-threaded within the Bridge
    // (sinkToModem reads, modemToSink writes, different goroutines but never
    // concurrent on the same buffer).
    readBuf  []byte
    writeBuf []byte
}

func NewTUNPacketSink(dev tun.Device, offset, mtu int) *TUNPacketSink {
    return &TUNPacketSink{
        dev:      dev,
        offset:   offset,
        mtu:      mtu,
        readBuf:  make([]byte, mtu+offset),
        writeBuf: make([]byte, mtu+offset),
    }
}

// ReadPacket reads one packet from the TUN device (host → modem / uplink).
// Blocks until a packet is available or the TUN is closed.
func (s *TUNPacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
    // tun.Read does NOT honor context — it blocks until the TUN is closed.
    // This is the known TUN limitation (见 internal/qmidatapath/AGENTS.md
    // "Close 时序"). Bridge.Stop() works around it by requiring the caller
    // to Close the TUN first, which unblocks Read.
    bufs := [][]byte{s.readBuf}
    sizes := make([]int, 1)
    n, err := s.dev.Read(bufs, sizes, s.offset)
    if err != nil {
        return nil, err
    }
    if n == 0 {
        return nil, nil
    }
    return s.readBuf[s.offset : s.offset+sizes[0]], nil
}

// WritePacket writes one packet to the TUN device (modem → host / downlink).
func (s *TUNPacketSink) WritePacket(pkt []byte) error {
    copy(s.writeBuf[s.offset:], pkt)
    _, err := s.dev.Write([][]byte{s.writeBuf[:s.offset+len(pkt)]}, s.offset)
    return err
}

func (s *TUNPacketSink) Name() string {
    name, err := s.dev.Name()
    if err != nil {
        return "tun(?)"  // never blocks logging
    }
    return name
}

func (s *TUNPacketSink) Close() error {
    return s.dev.Close()
}
```

### 4.3 改 `internal/qmidatapath/relay.go` — Bridge 解耦

**删除**:
- `tunDevice` 接口(被 PacketSink 取代)
- `Bridge.offset` 字段(offset 移入 TUNPacketSink)
- `New()` 的 `offset` 参数

**改动**:

```go
// Bridge 结构体
type Bridge struct {
    sink    PacketSink   // ← 替换 tun tunDevice
    bulkIn  BulkReader
    bulkOut BulkWriter
    mtu     int
    zlp     bool         // 保留(USB bulk OUT 物理约束,与后端无关)

    cancel  context.CancelFunc
    wg      sync.WaitGroup
    mu      sync.Mutex
    started bool

    txPackets atomic.Int64
    txBytes   atomic.Int64
    rxPackets atomic.Int64
    rxBytes   atomic.Int64
}

// New 签名变更(去掉 offset 参数)
func New(sink PacketSink, bulkIn BulkReader, bulkOut BulkWriter, mtu int, zlp bool) *Bridge {
    return &Bridge{
        sink:    sink,
        bulkIn:  bulkIn,
        bulkOut: bulkOut,
        mtu:     mtu,
        zlp:     zlp,
    }
}
```

**goroutine 改名 + 逻辑**(ZLP 逻辑完整保留):

```go
// sinkToModem: host → modem (uplink)
func (b *Bridge) sinkToModem(ctx context.Context) {
    defer b.wg.Done()
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }
        pkt, err := b.sink.ReadPacket(ctx)
        if err != nil {
            if ctx.Err() != nil {
                return
            }
            log.Printf("qmidatapath: sink read error: %v", err)
            continue
        }
        if len(pkt) == 0 {
            continue
        }
        b.txPackets.Add(1)
        b.txBytes.Add(int64(len(pkt)))
        if _, err := b.bulkOut.Write(pkt); err != nil {
            if ctx.Err() != nil {
                return
            }
            log.Printf("qmidatapath: bulk OUT write error: %v", err)
            continue
        }
        // ZLP: if pkt length is a multiple of bulk OUT maxPacketSize (512),
        // the modem may buffer it waiting for more data. Append a 0-byte
        // Write (Zero Length Packet) to signal end-of-transfer.
        // —— 保留,USB 物理约束,与后端无关
        if b.zlp && len(pkt)%bulkMaxPacketSize == 0 {
            b.bulkOut.Write([]byte{})
        }
    }
}

// modemToSink: modem → host (downlink)
func (b *Bridge) modemToSink(ctx context.Context) {
    defer b.wg.Done()
    buf := make([]byte, 65535)  // 单包读,最大 IP 包大小
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }
        n, err := b.bulkIn.ReadContext(ctx, buf)
        if err != nil {
            if ctx.Err() != nil {
                return
            }
            log.Printf("qmidatapath: bulk IN read error: %v", err)
            continue
        }
        if n == 0 {
            continue
        }
        b.rxPackets.Add(1)
        b.rxBytes.Add(int64(n))
        if err := b.sink.WritePacket(buf[:n]); err != nil {
            if ctx.Err() != nil {
                return
            }
            log.Printf("qmidatapath: sink write error: %v", err)
        }
    }
}

// Start 改 goroutine 名
func (b *Bridge) Start(parent context.Context) error {
    // ... 不变 ...
    go b.sinkToModem(ctx)
    go b.modemToSink(ctx)
    // ... 不变 ...
}
```

**包注释**:更新 ASCII 图把 "TUN Device" 改为 "PacketSink (TUN/netstack)"。

### 4.4 改 `internal/qmidatapath/relay_test.go` — mock 迁移

**删除** `fakeTUN`(实现 tunDevice)。

**新增** `fakePacketSink`(实现 PacketSink):

```go
// fakePacketSink implements PacketSink for testing.
// ReadPacket pops from rx channel; WritePacket pushes to tx channel.
// Close unblocks pending ReadPacket via the done channel.
type fakePacketSink struct {
    rx   chan []byte   // ReadPacket pops from here
    tx   chan []byte   // WritePacket pushes here
    done chan struct{}
    closeOnce sync.Once
}

func newFakePacketSink() *fakePacketSink {
    return &fakePacketSink{
        rx:   make(chan []byte, 16),
        tx:   make(chan []byte, 16),
        done: make(chan struct{}),
    }
}

func (f *fakePacketSink) ReadPacket(ctx context.Context) ([]byte, error) {
    select {
    case p := <-f.rx:
        return p, nil
    case <-f.done:
        return nil, fmt.Errorf("sink closed")
    case <-ctx.Done():
        return nil, ctx.Err()
    }
}

func (f *fakePacketSink) WritePacket(pkt []byte) error {
    select {
    case f.tx <- append([]byte(nil), pkt...):
        return nil
    case <-f.done:
        return fmt.Errorf("sink closed")
    }
}

func (f *fakePacketSink) Name() string { return "fake0" }

func (f *fakePacketSink) Close() error {
    f.closeOnce.Do(func() { close(f.done) })
    return nil
}
```

**测试用例迁移**(13 个 → 全部保留语义):
- `TestRelayModemToTUN` → `TestRelayModemToSink`(bulk IN → sink.WritePacket)
- `TestRelayTUNToModem` → `TestRelaySinkToModem`(sink.ReadPacket → bulk OUT)
- `TestRelayRawIPPassthrough` — 不变(字节透传)
- `TestRelayZLP/ZLPEnabled/Disabled` — 不变(ZLP 逻辑在 sinkToModem 保留)
- `TestRelayOffsetMacOS` — **删除**(offset 移入 TUNPacketSink,relay 不再有 offset 概念);新增 `TestTUNPacketSinkOffset` 在 tun_sink_test.go 里测 offset 包装
- `TestBridgeStopWaitsGoroutines` — 不变
- `TestBridgeCloseOrdering` — 保留(Close 时序约束依然适用)
- `TestRelayContextCancel` — 不变
- `TestConcurrentRelayRace` — 不变
- `TestRelayStats` — 不变
- `TestStartIdempotent` / `TestStopWithoutStart` — 不变
- `TestRelayBulkWriteError` — 不变

**新增 `internal/qmidatapath/tun_sink_test.go`**:测 TUNPacketSink 的 offset 包装(macOS 4 字节前缀 + 单包读)。

**`stopBridgeAndSink` helper**:
```go
func stopBridgeAndSink(bridge *Bridge, sink *fakePacketSink) {
    sink.Close()   // 解除 sinkToModem 的 ReadPacket 阻塞
    bridge.Stop()  // cancel ctx + wg.Wait()
}
```

### 4.5 改 `cmd/qmidial/main.go` — New() 参数

```go
// Before (line 191):
bridge = qmidatapath.New(tunDev, bulkIn, bulkOut, offset, 1500, true)

// After:
sink := qmidatapath.NewTUNPacketSink(tunDev, offset, 1500)
bridge = qmidatapath.New(sink, bulkIn, bulkOut, 1500, true)
```

清理时序(line 287-300)保持不变(`tunDev.Close()` 仍由 cmd/qmidial 直接持有,因为 TUN 生命周期归 main 管;**或改为 sink.Close()——二选一,保持与现有 TUN 归属一致即可**)。

> **决策**:`tunDev` 仍由 `cmd/qmidial/main.go` 持有和关闭(因为 main 还要用 `tunName` 做 DNS 配置、路由诊断)。`sink.Close()` 不调,main 直接调 `tunDev.Close()`。这样最小改动。

---

## 五、验证

```bash
# 1. mock 测试(纯逻辑,CI 友好)
mise exec -- go test -race ./internal/qmidatapath/

# 2. 硬件 TUN relay 不变
mise exec -- go test -tags=hardware ./internal/qmidatapath/

# 3. 端到端 TUN 上网(Windows,需 admin + wintun.dll)
mise exec -- go build -o qmidial.exe ./cmd/qmidial
./qmidial.exe -dial -tun
# 期望:curl HTTP 200、ping、nslookup 全部与重构前一致

# 4. macOS 回归(无需 sudo,attest 路径)
mise exec -- go test -tags=hardware ./internal/qmidatapath/
```

**Phase 1 通过判据**:
- `-race` 全绿(13 个测试语义保留)
- `qmidial.exe -dial -tun` 行为与重构前完全一致(同样拿到 IP、curl/ping/nslookup 同样结果)
- relay.go / relay_test.go 中 `tunDevice` / `tun` / `offset` 字样全部消失(grep 验证)

---

## 六、涉及文件

| 文件 | 改动 |
|---|---|
| `internal/qmidatapath/sink.go` | **新增** — PacketSink 接口 |
| `internal/qmidatapath/tun_sink.go` | **新增** — TUNPacketSink 实现 |
| `internal/qmidatapath/tun_sink_test.go` | **新增** — offset 包装测试 |
| `internal/qmidatapath/relay.go` | 改 — tunDevice→PacketSink,删 offset,ZLP 保留,goroutine 改名 |
| `internal/qmidatapath/relay_test.go` | 改 — fakeTUN→fakePacketSink,删 OffsetMacOS,加 tun_sink 测试 |
| `cmd/qmidial/main.go` | 改 — 1 处 New() 调用(加 NewTUNPacketSink) |

---

## 七、风险

**低**。纯重构,接口注入模式已有(BulkReader/BulkWriter),mock 模式已有(fakeBulkReader/Writer)。

唯一注意点:`tun.Read` 不响应 context 的死锁陷阱依然存在(在 TUNPacketSink.ReadPacket 内部),靠 cmd/qmidial 的 Close 时序(`tunDev.Close()` 解除阻塞)解决。**netstack sink(子计划 01)不存在这个问题**,因为 channel close 立即让 ReadContext 返回 nil。
