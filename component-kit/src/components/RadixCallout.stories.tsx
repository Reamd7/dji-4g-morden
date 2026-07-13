import type { Meta, StoryObj } from '@storybook/react';
import { Callout, Flex } from '@radix-ui/themes';

const meta: Meta<typeof Callout.Root> = {
  title: 'Radix/Callout',
  component: Callout.Root,
  tags: ['autodocs'],
  argTypes: {
    color: { control: 'select', options: ['accent', 'gray', 'red', 'amber', 'green', 'blue'] },
    variant: { control: 'select', options: ['soft', 'surface', 'outline'] },
    size: { control: 'select', options: ['1', '2', '3'] },
  },
};
export default meta;

type Story = StoryObj<typeof Callout.Root>;

export const SemanticColors: Story = {
  render: () => (
    <Flex direction="column" gap="2">
      <Callout.Root color="red"><Callout.Text>错误:设备扫描失败,USB 权限不足</Callout.Text></Callout.Root>
      <Callout.Root color="amber"><Callout.Text>警告:信号微弱,建议调整天线</Callout.Text></Callout.Root>
      <Callout.Root color="green"><Callout.Text>成功:设备已连接</Callout.Text></Callout.Root>
      <Callout.Root color="blue"><Callout.Text>提示:请插入 SIM 卡并确保 PS 附着</Callout.Text></Callout.Root>
    </Flex>
  ),
};

export const Variants: Story = {
  render: () => (
    <Flex direction="column" gap="2">
      <Callout.Root color="accent" variant="soft"><Callout.Text>soft</Callout.Text></Callout.Root>
      <Callout.Root color="accent" variant="surface"><Callout.Text>surface</Callout.Text></Callout.Root>
      <Callout.Root color="accent" variant="outline"><Callout.Text>outline</Callout.Text></Callout.Root>
    </Flex>
  ),
};

export const Playground: Story = {
  render: (args) => (
    <Callout.Root {...args}>
      <Callout.Text>扫描失败:USB 权限不足</Callout.Text>
    </Callout.Root>
  ),
  args: { color: 'red', variant: 'soft', size: '2' },
};
