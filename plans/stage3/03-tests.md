# 子计划 03 — 测试(mock + 硬件)

> 隶属 `plans/stage3-tun-internet.md`(总览)。依赖子计划 01(Bridge)+ 02(集成)。

---

## 一、目标

为阶段 3 数据通路层建立测试体系,对齐项目现有测试分层(AGENTS.md「测试方案」):
- mock 单测(CI 友好,无硬件,`-race` 硬性要求)
- 硬件集成测试(build tag `hardware`,需真实 EC25)

---

## 二、mock 单测(`internal/qmidatapath/relay_test.go`,无硬件)

用 fake BulkReader/Writer + fake tunDevice(实现接口),完全离线测试 relay 逻辑。
**TUN 注入设计(子计划 01)使 relay 逻辑完全离线可测**——不需要管理员权限/Wintun.dll。

### 2.1 fake 实现

```go
// fakeBulkReader 模拟 gousb InEndpoint,从预设 channel 吐数据
type fakeBulkReader struct { pkts chan []byte }
func (f *fakeBulkReader) ReadContext(ctx context.Context, buf []byte) (int, error) {
    select {
    case p := <-f.pkts: n := copy(buf, p); return n, nil
    case <-ctx.Done(): return 0, ctx.Err()
    }
}

// fakeBulkWriter 记录所有写入
type fakeBulkWriter struct {
    mu      sync.Mutex
    written [][]byte
}
func (f *fakeBulkWriter) Write(buf []byte) (int, error) {
    f.mu.Lock(); defer f.mu.Unlock()
    f.written = append(f.written, append([]byte(nil), buf...))
    return len(buf), nil
}

// fakeTUN 实现 tunDevice 接口,双向 channel
type fakeTUN struct {
    rx        chan []byte  // Read 从此读
    tx        chan []byte  // Write 写到此
    batchSize int
}
func (f *fakeTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
    select {
    case p := <-f.rx:
        copy(bufs[0][offset:], p)
        sizes[0] = len(p)
        return 1, nil
    case <-ctx.Done(): return 0, ctx.Err()
    }
}
func (f *fakeTUN) Write(bufs [][]byte, offset int) (int, error) {
    for _, b := range bufs {
        f.tx <- append([]byte(nil), b[offset:]...)
    }
    return len(bufs), nil
}
func (f *fakeTUN) BatchSize() int { return f.batchSize }
func (f *fakeTUN) Name() (string, error) { return "fake0", nil }
func (f *fakeTUN) Close() error { return nil }
```

### 2.2 测试用例(9 个)

| 测试 | 验证点 |
|---|---|
| `TestRelayModemToTUN` | bulk IN 收到 IP 包 → TUN.Write 收到同样包(下行) |
| `TestRelayTUNToModem` | TUN.Read 收到 IP 包 → bulkOut.Write 收到同样包(上行) |
| `TestRelayRawIPPassthrough` | raw-IP 直传,无头处理(首字节 0x45 原样到达) |
| `TestRelayZLP` | 512 倍数包触发 ZLP(b.zlp=true 时追加 0 字节 Write) |
| `TestRelayOffsetMacOS` | offset=4 时 macOS headroom 正确处理(TUN 读到的数据跳过前 4 字节) |
| `TestBridgeStopWaitsGoroutines` | Stop 等 relay goroutine 退出(不泄漏,`wg.Wait()` 生效) |
| `TestBridgeCloseOrdering` | Stop relay → 才能 Close transport(时序,防 segfault) |
| `TestRelayContextCancel` | ctx 取消后两个 goroutine 干净退出 |
| `TestConcurrentRelayRace` | `-race` 下并发读写安全 |

跑:`make test-race`(`-race` 硬性要求,AGENTS.md 测试约定)。

---

## 三、硬件集成测试(`internal/qmidatapath/relay_hardware_test.go`,build tag)

```go
//go:build hardware
```

### 3.1 测试用例

| 测试 | 内容 |
|---|---|
| `TestHardwareBulkEndpoints` | OpenBulkEndpoints 返回非 nil(EP 0x88/0x05 可打开) |
| `TestHardwareRelayEndToEnd` | 拨号 + TUN + relay + ping 8.8.8.8 走 4G(核心端到端) |
| `TestHardwareRelayDNS` | DNS 解析:nslookup baidu.com 通过(验证 DNS 自建) |
| `TestHardwareRelayZLPReal` | 真实 512 字节包是否需 ZLP(验证 R5) |

### 3.2 端到端测试流程

```go
func TestHardwareRelayEndToEnd(t *testing.T) {
    // 1. 拨号(transport + client + manager.Connect)
    // 2. 创建 TUN
    // 3. OpenBulkEndpoints + Bridge.Start
    // 4. 等 IP/路由配好(configureNetwork)
    // 5. 配 DNS(自建)
    // 6. exec.Command("ping", "-n", "4", "8.8.8.8")  // Windows: -n, Linux/macOS: -c
    // 7. 断言 ping 成功(loss < 100%)
    // 8. exec.Command("nslookup", "baidu.com")       // DNS 验证
    // 9. 断言 DNS 解析成功
    // 10. bridge.Stop + cleanup
}
```

成功标准:`ping 8.8.8.8` 通过 4G(loss < 100%) + `nslookup baidu.com` 解析成功。

跑:
```bash
# 需管理员/sudo + wintun.dll(Windows)
DJI_TEST_APN=3gnet mise exec -- go test -tags=hardware -v -run TestHardwareRelay ./internal/qmidatapath/
```

---

## 四、覆盖率目标(对齐 AGENTS.md)

| 层 | 目标 | 说明 |
|---|---|---|
| relay 适配层(qmidatapath) | ≥ 80% | mock 测,覆盖双向/ZLP/offset/Close 时序 |
| bulkendpoints | ≥ 80% | mock 测锁/closed |
| USB 物理层(bulk transfer) | 不计 | 硬件测试验证,不追求覆盖率 |

---

## 五、完成标志

- [ ] `relay_test.go` 9 个 mock 测试,`-race` 通过
- [ ] `relay_hardware_test.go` 硬件测试真实跑通(ping 8.8.8.8 + DNS 走 4G)
- [ ] coverage relay 适配层 ≥ 80%

---

## 六、相关文件

- `internal/qmidatapath/relay.go` — 子计划 01,被测代码
- `internal/testutil/scriptport.go` — 现有 mock(参考模式,但 relay 用专门的 fakeBulkReader/Writer)
- `internal/usbtransport/usbtransport_test.go` — mock 测试模式参考
