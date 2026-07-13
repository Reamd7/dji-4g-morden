import type { Meta, StoryObj } from '@storybook/react';
import React from 'react';

/**
 * 设计规范基础 Tokens — 基于 Radix Themes 的 CSS 变量体系。
 * 展示 accent/gray 颜色阶梯、字号、间距、圆角、阴影。
 */
const meta: Meta = {
  title: 'Foundations/Tokens',
};
export default meta;

const STEPS = Array.from({ length: 12 }, (_, i) => i + 1);

const Swatch: React.FC<{ name: string; label?: string }> = ({ name, label }) => (
  <div
    style={{
      width: 64,
      height: 64,
      background: `var(--${name})`,
      display: 'flex',
      alignItems: 'flex-end',
      justifyContent: 'center',
      paddingBottom: 4,
      color: 'var(--gray-12)',
      mixBlendMode: 'difference',
      fontSize: 11,
      fontWeight: 600,
    }}
  >
    {label ?? name}
  </div>
);

const Section: React.FC<{ title: string; children: React.ReactNode }> = ({ title, children }) => (
  <div style={{ marginBottom: 32 }}>
    <h3 style={{ color: 'var(--gray-11)', fontSize: 13, textTransform: 'uppercase', letterSpacing: 0.5, marginBottom: 8 }}>
      {title}
    </h3>
    {children}
  </div>
);

export const Colors: StoryObj = {
  render: () => (
    <div style={{ fontFamily: 'var(--default-font-family)', padding: 8 }}>
      <Section title="Accent — iris (12-step)">
        <div style={{ display: 'flex' }}>
          {STEPS.map((s) => <Swatch key={s} name={`iris-${s}`} label={`${s}`} />)}
        </div>
      </Section>
      <Section title="Gray — slate (12-step)">
        <div style={{ display: 'flex' }}>
          {STEPS.map((s) => <Swatch key={s} name={`slate-${s}`} label={`${s}`} />)}
        </div>
      </Section>
      <Section title="语义色 (success / warning / danger)">
        <div style={{ display: 'flex', gap: 12 }}>
          <Swatch name="green-9" label="success" />
          <Swatch name="amber-9" label="warning" />
          <Swatch name="red-9" label="danger" />
          <Swatch name="blue-9" label="info" />
        </div>
      </Section>
    </div>
  ),
};

export const Typography: StoryObj = {
  render: () => (
    <div style={{ fontFamily: 'var(--default-font-family)', padding: 8, color: 'var(--gray-12)' }}>
      <Section title="字号阶梯 (1-9)">
        {[1, 2, 3, 4, 5, 6, 7, 8, 9].map((s) => (
          <div key={s} style={{ fontSize: `var(--font-size-${s})`, margin: '4px 0' }}>
            Size {s} — DJI 4G 模组桌面客户端
          </div>
        ))}
      </Section>
      <Section title="字重">
        {[400, 500, 600, 700].map((w) => (
          <div key={w} style={{ fontWeight: w, fontSize: 16, margin: '4px 0' }}>Weight {w}</div>
        ))}
      </Section>
    </div>
  ),
};

export const Spacing: StoryObj = {
  render: () => (
    <div style={{ fontFamily: 'var(--default-font-family)', padding: 8 }}>
      <Section title="间距阶梯 (space 1-9)">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {[1, 2, 3, 4, 5, 6, 7, 8, 9].map((s) => (
            <div key={s} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div style={{ background: 'var(--iris-9)', width: `var(--space-${s})`, height: 16 }} />
              <span style={{ color: 'var(--gray-11)', fontSize: 12 }}>space-{s}</span>
            </div>
          ))}
        </div>
      </Section>
    </div>
  ),
};

export const Radius: StoryObj = {
  render: () => (
    <div style={{ fontFamily: 'var(--default-font-family)', padding: 8 }}>
      <Section title="圆角阶梯 (radius 1-6)">
        <div style={{ display: 'flex', gap: 12 }}>
          {[1, 2, 3, 4, 5, 6].map((r) => (
            <div key={r} style={{
              width: 64, height: 64,
              background: 'var(--iris-9)',
              borderRadius: `var(--radius-${r})`,
              display: 'flex', alignItems: 'center', justifyContent: 'center',
              color: 'var(--gray-12)', mixBlendMode: 'difference', fontSize: 12,
            }}>
              {r}
            </div>
          ))}
        </div>
      </Section>
      <Section title="阴影 (shadow 1-6)">
        <div style={{ display: 'flex', gap: 16, padding: 16 }}>
          {[1, 2, 3, 4, 5, 6].map((s) => (
            <div key={s} style={{
              width: 64, height: 64,
              background: 'var(--color-panel)',
              boxShadow: `var(--shadow-${s})`,
              borderRadius: 'var(--radius-3)',
            }} />
          ))}
        </div>
      </Section>
    </div>
  ),
};
