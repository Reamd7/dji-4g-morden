import tsParser from '@typescript-eslint/parser';

/**
 * 设计规范强制规则(component-kit / desktop 业务代码通用)。
 *
 * 核心约束:业务代码不得在 inline style 中硬编码【间距】或【颜色】,
 * 必须使用 Radix Themes 的 CSS 变量(var(--iris-9)、var(--space-3))
 * 或封装好的业务组件。这确保视觉值都来自设计 token 体系。
 *
 * “间距”= padding/margin/gap/inset/定位(top/right/bottom/left)。
 * 尺寸(width/height)不在此约束内(组件固有尺寸,如信号条格子宽度)。
 *
 * 允许:var(--iris-9)、百分比('50%')、calc()、CSS 关键字、margin: 0(重置)。
 * 禁止:#ffffff、rgb(1,2,3)、'8px'、16(裸数字)、'1rem' 等字面量。
 */
const colorKey = /color|background|border|fill|stroke|outline|shadow|boxShadow/i;
const spaceKey = /padding|margin|gap|inset|top|right|bottom|left/i;

const noHardcodedColor = {
  selector: `JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property[key.name=${colorKey}] > Literal[value=/^(#|rgb|hsl)/i]`,
  message: '禁止硬编码颜色 — 使用 Radix CSS 变量(var(--iris-9))或封装组件。见 DESIGN_GUIDE.md',
};

// 字符串间距:'8px' '1rem'(var() 无 px/rem 后缀,不匹配)
const noHardcodedSpaceStr = {
  selector: `JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property[key.name=${spaceKey}] > Literal[value=/\\d+(px|rem|em|vh|vw|pt)$/i]`,
  message: '禁止硬编码间距 — 使用 Radix CSS 变量(var(--space-3))或封装组件。见 DESIGN_GUIDE.md',
};

// 裸数字间距:padding: 16 / margin: 8(number literal,raw 无引号;0 豁免=重置)
const noHardcodedSpaceNum = {
  selector: `JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property[key.name=${spaceKey}] > Literal[raw=/^[1-9]\\d*$/]`,
  message: '禁止硬编码间距(数字)— 使用 Radix CSS 变量(var(--space-3))或封装组件。见 DESIGN_GUIDE.md',
};

export default [
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: { parser: tsParser },
    rules: {
      'no-restricted-syntax': ['error', noHardcodedColor, noHardcodedSpaceStr, noHardcodedSpaceNum],
    },
  },
  {
    // token 展示页需绝对尺寸可视化 token 本身,豁免。
    files: ['src/foundations/**/*.{ts,tsx}'],
    rules: { 'no-restricted-syntax': 'off' },
  },
];
