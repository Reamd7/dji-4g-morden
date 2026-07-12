# 子计划 01 — TUN + DataTransport + Relay 实现

> 隶属 `plans/stage3-tun-internet.md`(总览)。依赖子计划 00 通过。
> 创建于 2026-07-12。

## 目标

实现 USB bulk EP ↔ TUN 的双向 raw IP 中继层。

## 依赖 / 前置

- **子计划 00 通过**:bulk EP 确认承载 raw IP 数据
- `golang.zx2c4.com/wireguard/tun` 依赖已添加
- Windows: `wintun.dll` 已放置

## 步骤

### 1. 添加 TUN 库依赖

```bash
mise exec -- go get golang.zx2c4.com/wireguard/tun
```

这会拉入:
- `golang.zx2c4.com/wireguard`(tun 包)
- `golang.zx2c4.com/wintun`(Windows only,自动)

### 2. `internal/qmitransport/bulkendpoints.go`(子计划 00 已创建)

如果 00 已创建,此处跳过。否则创建 `OpenBulkEndpoints()` 方法。

### 3. `internal/tunbridge/tunbridge.go`

Bridge 结构体,管理 TUN + relay 生命周期:

```go
package tunbridge

import (
    "context"
    "fmt"
    "sync"

    "golang.zx2c4.com/wireguard/tun"
)

// Bridge relays raw IP packets between a USB bulk endpoint pair and a TUN
// device. Both sides are layer-3 (no ethernet header), so packets pass
// through unchanged.
type Bridge struct {
    tun    tun.Device
    relay  *Relay
    cancel context.CancelFunc
    done   chan struct{}
}

// New creates a TUN device and prepares the relay. The bulk reader/writer
// are injected (testability). MTU is set on the TUN at creation.
func New(tunName string, mtu int, bulkIn BulkReader, bulkOut BulkWriter) (*Bridge, error) {
    dev, err := tun.CreateTUN(tunName, mtu)
    if err != nil {
        return nil, fmt.Errorf("tunbridge: create TUN: %w", err)
    }
    name, err := dev.Name()
    if err != nil {
        dev.Close()
        return nil, fmt.Errorf("tunbridge: get TUN name: %w", err)
    }

    return &Bridge{
        tun:   dev,
        relay: NewRelay(bulkIn, bulkOut, dev),
        done:  make(chan struct{}),
    }, nil
}

// Name returns the actual TUN interface name (may differ from requested,
// e.g. macOS "utun" → "utun7").
func (b *Bridge) Name() (string, error) {
    return b.tun.Name()
}

// Start launches the bidirectional relay goroutines.
func (b *Bridge) Start(parent context.Context) {
    ctx, cancel := context.WithCancel(parent)
    b.cancel = cancel
    go func() {
        defer close(b.done)
        b.relay.Run(ctx)
    }()
}

// Stop signals the relay to stop and waits for goroutines to exit.
// Then closes the TUN device.
func (b *Bridge) Stop() {
    if b.cancel != nil {
        b.cancel()
    }
    <-b.done
    b.tun.Close()
}
```

### 4. `internal/tunbridge/relay.go`

双向中继,两个 goroutine:

```go
package tunbridge

import (
    "context"
    "log"
    "runtime"

    "golang.zx2c4.com/wireguard/tun"
)

// BulkReader abstracts a USB bulk IN endpoint (gousb *InEndpoint satisfies this).
type BulkReader interface {
    ReadContext(ctx context.Context, buf []byte) (int, error)
}

// BulkWriter abstracts a USB bulk OUT endpoint (gousb *OutEndpoint satisfies this).
type BulkWriter interface {
    WriteContext(ctx context.Context, buf []byte) (int, error)
}

// offset for TUN Read/Write. macOS needs >= 4 (AF-family prefix headroom).
// 4 is safe on all platforms.
const tunOffset = 4

// Relay bidirectionally forwards raw IP between USB bulk EPs and a TUN device.
type Relay struct {
    bulkIn  BulkReader
    bulkOut BulkWriter
    tun     tun.Device
}

func NewRelay(bulkIn BulkReader, bulkOut BulkWriter, tun tun.Device) *Relay {
    return &Relay{bulkIn: bulkIn, bulkOut: bulkOut, tun: tun}
}

// Run starts both relay directions and blocks until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
    var wg sync.WaitGroup
    wg.Add(2)

    // TUN → modem (uplink)
    go func() {
        defer wg.Done()
        r.tunToModem(ctx)
    }()

    // modem → TUN (downlink)
    go func() {
        defer wg.Done()
        r.modemToTun(ctx)
    }()

    wg.Wait()
}

// tunToModem reads IP packets from TUN and writes them to bulk OUT EP.
func (r *Relay) tunToModem(ctx context.Context) {
    batch := r.tun.BatchSize()
    bufs := make([][]byte, batch)
    sizes := make([]int, batch)
    for i := range bufs {
        bufs[i] = make([]byte, tunOffset+65535)
    }

    for {
        n, err := r.tun.Read(bufs, sizes, tunOffset)
        if err != nil {
            if ctx.Err() != nil { return }
            log.Printf("tunbridge: TUN read error: %v", err)
            return
        }
        for i := 0; i < n; i++ {
            pkt := bufs[i][tunOffset : tunOffset+sizes[i]]
            if _, err := r.bulkOut.WriteContext(ctx, pkt); err != nil {
                if ctx.Err() != nil { return }
                log.Printf("tunbridge: bulk OUT write error: %v", err)
            }
        }
    }
}

// modemToTun reads raw IP from bulk IN EP and writes to TUN.
func (r *Relay) modemToTun(ctx context.Context) {
    buf := make([]byte, 65535)
    tunBuf := make([][]byte, 1)
    tunBuf[0] = make([]byte, tunOffset+65535)

    for {
        n, err := r.bulkIn.ReadContext(ctx, buf)
        if err != nil {
            if ctx.Err() != nil { return }
            log.Printf("tunbridge: bulk IN read error: %v", err)
            return
        }
        if n == 0 { continue }

        // Validate: first nibble must be IP version (4 or 6)
        version := buf[0] >> 4
        if version != 4 && version != 6 {
            log.Printf("tunbridge: non-IP packet on bulk IN (version=%d), skipping", version)
            continue
        }

        copy(tunBuf[0][tunOffset:], buf[:n])
        if _, err := r.tun.Write(tunBuf[:1], tunOffset); err != nil {
            if ctx.Err() != nil { return }
            log.Printf("tunbridge: TUN write error: %v", err)
        }
    }
}
```

### 5. 设计要点

#### 为什么用 offset=4

macOS utun 内部在每帧前加 4 字节 AF-family prefix(`tun_darwin.go` 在 Read 时 strip,
Write 时 inject)。offset=4 留出空间,三平台通用。

#### 为什么 modemToTun 用单 buffer 而非批量

gousb 的 `ReadContext` 一次返回一个 USB transfer(一个 IP 包)。而 TUN 的 `Write` 接受
`[][]byte` 批量。我们一次写一个包(单元素切片),足够高效——下行带宽受 LTE 限制(≤150Mbps),
不是 USB 限制。

#### 为什么 tunToModem 用批量

TUN 的 `Read` 可能一次返回多个包(batch)。我们对每个包单独调 `WriteContext`(一个 URB per packet)。
未来可以优化为批量 URB,但初始版本不需要。

#### 线程安全

- `tunToModem` 和 `modemToTun` 操作不同的 endpoint(OUT vs IN),无竞争
- `tun.Read` 和 `tun.Write` 可以并发(WireGuard TUN 设计如此)
- Close:`context.Cancel()` → 两个 goroutine 退出 → `wg.Wait()` → `tun.Close()`

## 交付物 / 完成标志

- [ ] `golang.zx2c4.com/wireguard/tun` 依赖在 go.mod
- [ ] `internal/tunbridge/tunbridge.go` — Bridge 结构体
- [ ] `internal/tunbridge/relay.go` — 双向中继 + BulkReader/Writer 接口
- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过

## 风险

| 风险 | 缓解 |
|---|---|
| Wintun.dll 缺失(Windows) | CreateTUN 会返回明确错误。文档要求放 dll |
| TUN 接口名不匹配(macOS) | 用 `dev.Name()` 取实际名,而非构造参数 |
| bulk Read 包粘连 | 初始方案:大 buffer + short packet 检测。如失败改固定 MTU buffer |
| 内存拷贝开销 | relay 有一次 copy(modem→TUN)。LTE 带宽下可忽略。未来可 zero-copy |
