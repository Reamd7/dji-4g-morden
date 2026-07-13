# DJI 4G Desktop 设计规范

> **核心原则:业务代码不得自定义间距和颜色,必须使用封装好的组件或设计 token。**

## 为什么

视觉一致性不能靠人肉记忆。如果每个业务文件都能写 `padding: '8px'` 或 `color: '#fff'`,
设计规范(深色 iris 主色、slate 灰阶、Radix 间距体系)会迅速瓦解。
强制所有视觉值来自统一 token 体系,才能保证:

- 换主题(亮色/暗色/高对比)只改一处,全局生效
- 视觉一致性(所有间距/颜色来自同一尺度)
- 可维护性(值有语义,`var(--space-3)` 而非 `12px`)

## 规则(ESLint 强制)

`eslint.config.mjs` 用 `no-restricted-syntax` 拦截 inline style 的硬编码:

| 禁止 | 允许 |
|---|---|
| `#ffffff` / `rgb(1,2,3)` / `hsl(...)` | `var(--iris-9)` / `var(--gray-11)` |
| `8px` / `16` / `'1rem'` | `var(--space-3)` / `var(--radius-2)` |
| `<div style={{ color: '#fff' }}>` | `<Text color="gray">` 或 `<div style={{ color: 'var(--gray-12)' }}>` |

例外(豁免 lint):
- `src/foundations/**` — token 展示页,需要绝对尺寸来可视化 token 本身
- 组件库内部封装(`src/components/*.tsx`)— 可用 token 变量做样式,这是封装的目的

## 如何写业务代码

### 1. 优先用 Radix Themes 组件(布局原语)

Radix 的 Flex/Box/Grid/Text/Heading/Callout 等组件,props 是 token 级别,天然不硬编码:

```tsx
// ✅ 正确:用 Radix 原语,gap/p 是 token 级别
import { Flex, Text } from '@radix-ui/themes';

<Flex gap="3" p="4" direction="column">
  <Text size="2" color="gray">信号强度</Text>
</Flex>

// ❌ 错误:硬编码间距/颜色,lint 报错
<div style={{ display: 'flex', gap: '12px', padding: '16px', color: '#9ca3af' }}>
  <span style={{ fontSize: '14px' }}>信号强度</span>
</div>
```

### 2. 复杂样式用 CSS 变量(token),不用字面量

```tsx
// ✅ 正确:引用 token 变量
<div style={{ background: 'var(--iris-9)', padding: 'var(--space-3)', borderRadius: 'var(--radius-3)' }} />

// ❌ 错误:硬编码
<div style={{ background: '#5b5bd6', padding: '12px', borderRadius: '8px' }} />
```

### 3. 业务特定 UI 封装成组件

如果一个 UI 片段在多处复用(如设备信息卡、信号显示),封装成组件(放 `src/components/`),
内部用 token,业务代码组合组件:

```tsx
// src/components/DeviceCard.tsx — 封装,内部用 tokens
export const DeviceCard = ({ imei, signal }) => ( ... )

// 业务页面 — 只组合,不写样式
<DeviceCard imei="..." signal={28} />
```

## Token 速查(Radix Themes)

| 类别 | 变量 | 示例值 |
|---|---|---|
| 主色 | `--iris-1` ~ `--iris-12` | step 9 = `#5b5bd6` |
| 灰阶 | `--slate-1` ~ `--slate-12` | step 12 = `#1c2024`(深色文字) |
| 语义 | `--green-9`(success)`--amber-9`(warn)`--red-9`(danger)`--blue-9`(info) | |
| 间距 | `--space-1` ~ `--space-9` | space-3 ≈ 12px |
| 圆角 | `--radius-1` ~ `--radius-6` | |
| 字号 | `--font-size-1` ~ `--font-size-9` | |
| 阴影 | `--shadow-1` ~ `--shadow-6` | |

完整 token 见 Storybook → Foundations/Tokens。
