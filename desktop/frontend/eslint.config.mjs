import tsParser from '@typescript-eslint/parser';

/**
 * 设计规范强制规则 — 与 component-kit/eslint.config.mjs 保持一致。
 *
 * 核心约束:desktop 业务代码不得在 inline style 中硬编码【间距】或【颜色】,
 * 必须使用 Radix Themes 的 CSS 变量(var(--iris-9)、var(--space-3))
 * 或封装好的业务组件。详见 component-kit/DESIGN_GUIDE.md。
 *
 * 允许:var(--iris-9)、百分比、calc()、margin: 0(重置)。
 * 禁止:#ffffff、rgb(1,2,3)、'8px'、16(裸数字)、'1rem' 等字面量。
 */
const colorKey = /color|background|border|fill|stroke|outline|shadow|boxShadow/i;
const spaceKey = /padding|margin|gap|inset|top|right|bottom|left/i;

const noHardcodedColor = {
  selector: `JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property[key.name=${colorKey}] > Literal[value=/^(#|rgb|hsl)/i]`,
  message: '禁止硬编码颜色 — 使用 Radix CSS 变量(var(--iris-9))或封装组件。见 component-kit/DESIGN_GUIDE.md',
};

const noHardcodedSpaceStr = {
  selector: `JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property[key.name=${spaceKey}] > Literal[value=/\\d+(px|rem|em|vh|vw|pt)$/i]`,
  message: '禁止硬编码间距 — 使用 Radix CSS 变量(var(--space-3))或封装组件。见 component-kit/DESIGN_GUIDE.md',
};

const noHardcodedSpaceNum = {
  selector: `JSXAttribute[name.name='style'] > JSXExpressionContainer > ObjectExpression > Property[key.name=${spaceKey}] > Literal[raw=/^[1-9]\\d*$/]`,
  message: '禁止硬编码间距(数字)— 使用 Radix CSS 变量(var(--space-3))或封装组件。见 component-kit/DESIGN_GUIDE.md',
};

export default [
  {
    // src/ 是业务代码(强制);bindings/ 是 generated(在 frontend/bindings,不在 src,不 lint)。
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: { parser: tsParser },
    rules: {
      'no-restricted-syntax': ['error', noHardcodedColor, noHardcodedSpaceStr, noHardcodedSpaceNum],
    },
  },
];
