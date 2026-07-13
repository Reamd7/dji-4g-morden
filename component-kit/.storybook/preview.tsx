import type { Preview } from '@storybook/react';
import React from 'react';
import '@radix-ui/themes/styles.css';
import { Theme } from '@radix-ui/themes';

/**
 * 全局主题配置 — DJI 4G Desktop 设计规范。
 * 深色为基(appearance=dark),accent=iris(科技紫),gray=slate(冷灰),
 * radius=medium(适度圆角)。所有组件在此 Theme 下渲染。
 */
const preview: Preview = {
  parameters: {
    backgrounds: {
      default: 'app',
      values: [
        { name: 'app', value: '#08090f' },
        { name: 'panel', value: '#0e0f17' },
        { name: 'light', value: '#ffffff' },
      ],
    },
    controls: { matchers: { color: /(background|color)$/i, date: /Date$/i } },
  },
  decorators: [
    (Story) => (
      <Theme
        appearance="dark"
        accentColor="iris"
        grayColor="slate"
        radius="medium"
        scaling="100%"
      >
        <Story />
      </Theme>
    ),
  ],
};

export default preview;
