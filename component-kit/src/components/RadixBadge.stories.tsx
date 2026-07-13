import type { Meta, StoryObj } from '@storybook/react';
import { Badge, Flex } from '@radix-ui/themes';

const meta: Meta<typeof Badge> = {
  title: 'Radix/Badge',
  component: Badge,
  tags: ['autodocs'],
  argTypes: {
    variant: { control: 'select', options: ['solid', 'soft', 'surface', 'outline'] },
    color: { control: 'select', options: ['accent', 'gray', 'red', 'green', 'amber', 'blue'] },
    size: { control: 'select', options: ['1', '2'] },
  },
};
export default meta;

type Story = StoryObj<typeof Badge>;

export const StatusBadges: Story = {
  render: () => (
    <Flex gap="3" align="center" wrap="wrap">
      <Badge color="green" variant="soft">● 已连接</Badge>
      <Badge color="gray" variant="soft">○ 未连接</Badge>
      <Badge color="amber" variant="soft">◐ 连接中</Badge>
      <Badge color="red" variant="solid">✕ 错误</Badge>
    </Flex>
  ),
};

export const Variants: Story = {
  render: () => (
    <Flex gap="3" align="center">
      <Badge variant="solid" color="accent">Solid</Badge>
      <Badge variant="soft" color="accent">Soft</Badge>
      <Badge variant="surface" color="accent">Surface</Badge>
      <Badge variant="outline" color="accent">Outline</Badge>
    </Flex>
  ),
};

export const Playground: Story = {
  args: { children: '已连接', color: 'green', variant: 'soft', size: '2' },
};
