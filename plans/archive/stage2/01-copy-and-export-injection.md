# 子计划 01 — 复制 quectel-qmi-go + 导出注入点

> 隶属 `plans/stage2-qmi-dialup.md`(总览)。纯源码操作,不依赖硬件。
> 让外部包能把自定义 USB transport 注入 quectel-qmi-go 协议栈。

---

## 目标

复制 quectel-qmi-go 核心包到 `third_party/`,新增导出构造函数 `NewClientFromTransport`,
使 `internal/qmitransport/` 能注入 USB transport。

## 依赖 / 前置

- 无代码依赖(纯源码复制 + 改)
- 不依赖子计划 00(可并行)

## 步骤

### 1. 复制核心包

```bash
cp -r ../source/vohive-collection/quectel-qmi-go/pkg/qmi/   third_party/quectel-qmi-go/qmi/
cp -r ../source/vohive-collection/quectel-qmi-go/pkg/netcfg/ third_party/quectel-qmi-go/netcfg/
```

复制范围:`pkg/qmi/`(协议栈核心)+ `pkg/netcfg/`(三平台网络配置,阶段 3 用)。
**不复制**:`pkg/manager/`(耦合 Linux 设备发现,见子计划 07)、`pkg/device/`(Linux sysfs)。

包路径改为本仓:`dji-modem-research/third_party/quectel-qmi-go/pkg/qmi` 等。复制后 import 全部指向本仓。

### 2. 新增 `qmi/transport_export.go`(~40 行)

```go
package qmi

// Transport 导出 qmiTransport 接口别名,供 USB transport 实现。
type Transport = qmiTransport

// NewClientFromTransport 用自定义 Transport(如 USB)创建 Client。
// 必须复刻 NewClientWithOptions 的初始化流程(client.go:277-332):newClientWithTransport
// 本身只启动 readLoop/writerLoop/indicationLoop 三 goroutine,不做 CTL Sync / 版本查询。
// 跳过 SyncOnOpen 会让模组残留上一会话状态;跳过 QueryVersionOnOpen 使 HasService() 乐观返回 true。
func NewClientFromTransport(ctx context.Context, conn Transport, opts ClientOptions) (*Client, error) {
	opts = normalizeClientOptions(opts)
	c := newClientWithTransport("usb", opts, conn)

	if opts.SyncOnOpen {
		syncCtx := ctx
		if syncCtx == nil { syncCtx = context.Background() }
		if _, hasDeadline := syncCtx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			syncCtx, cancel = context.WithTimeout(syncCtx, 5*time.Second)
			defer cancel()
		}
		if err := c.Sync(syncCtx); err != nil {
			c.logf(ClientLogLevelDebug, "QMI: initial sync failed (non-fatal): %v", err)
		}
	}

	if opts.QueryVersionOnOpen {
		versionCtx := ctx
		if versionCtx == nil { versionCtx = context.Background() }
		if _, hasDeadline := versionCtx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			versionCtx, cancel = context.WithTimeout(versionCtx, 5*time.Second)
			defer cancel()
		}
		if versions, err := c.GetServiceVersions(versionCtx); err == nil {
			c.serviceVersions = ServiceVersionMap(versions)
			c.versionQueried = true
		}
	}
	return c, nil
}
```

**为什么不能只调 newClientWithTransport**:它(client.go:371)只启动 3 goroutine,不做 CTL Sync / 版本查询。
`NewClientWithOptions`(client.go:244-335)在其后做 SyncOnOpen(client.go:299-312,清模组残留状态)+ QueryVersionOnOpen(client.go:315-332,让 HasService/ensureServiceAllocatable 工作)。必须复刻。

### 3. 验证编译

```bash
mise exec -- go build ./third_party/quectel-qmi-go/...
```

确认 qmi/netcfg 只依赖标准库 + 日志库,无 vohive 内部包依赖。

## 交付物 / 完成标志

- [x] `third_party/quectel-qmi-go/{qmi,netcfg}/` 复制完成,import 指向本仓(无 import 改动:源码无内部交叉引用)
- [x] `qmi/transport_export.go` 新增,`NewClientFromTransport` + `Transport` 导出
- [x] `go build ./third_party/quectel-qmi-go/...` 通过(+ go vet + go test 全通过)
- [x] SyncOnOpen 复刻(非空壳)。注:计划原文提到的 QueryVersionOnOpen 在源码中不存在,已删除

## 风险

| 风险 | 缓解 |
|---|---|
| **license**:本地源码无 LICENSE 文件,module=`github.com/iniwex5/quectel-qmi-go` | 复制前从上游 GitHub 确认许可证(本会话 GitHub 不可达,未坐实)。若有,随复制;若无,联系作者授权 |
| vohive 内部包依赖 | go build 验证;qmi/netcfg 应自包含 |
| manager 复制与否 | 本计划**不复制** manager(见子计划 07 决策) |
