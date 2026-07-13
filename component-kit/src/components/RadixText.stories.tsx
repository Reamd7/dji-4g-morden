import type { Meta, StoryObj } from '@storybook/react';
import { Text } from '@radix-ui/themes';

const meta: Meta<typeof Text> = {
  title: 'Radix/Text',
  component: Text,
  tags: ['autodocs'],
  argTypes: {
    size: { control: 'select', options: ['1', '2', '3', '4', '5', '6', '7', '8', '9'] },
    color: { control: 'select', options: ['gray', 'accent', 'red', 'amber', 'green', 'blue'] },
    weight: { control: 'select', options: ['regular', 'medium', 'bold'] },
  },
};
export default meta;

type Story = StoryObj<typeof Text>;

export const Sizes: Story = {
  render: () => (
    <>
      {(['1', '2', '3', '4', '5', '6', '7', '8', '9'] as const).map((s) => (
        <div key={s}>
          <Text size={s}>Size {s} — DJI 4G 模组桌面客户端</Text>
        </div>
      ))}
    </>
  ),
};

export const Colors: Story = {
  render: () => (
    <div style={{ display: 'flex', gap: 'var(--space-3)', flexWrap: 'wrap' }}>
      <Text color="gray">gray</Text>
      <Text color="accent">accent</Text>
      <Text color="red">red</Text>
      <Text color="amber">amber</Text>
      <Text color="green">green</Text>
      <Text color="blue">blue</Text>
    </div>
  ),
};

export const Weights: Story = {
  render: () => (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--space-1)' }}>
      <Text weight="regular" size="4">regular</Text>
      <Text weight="medium" size="4">medium</Text>
      <Text weight="bold" size="4">bold</Text>
    </div>
  ),
};

export const Playground: Story = {
  args: { children: 'DJI 4G Desktop', size: '3', color: 'gray' },
};
