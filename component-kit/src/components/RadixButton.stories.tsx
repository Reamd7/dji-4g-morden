import type { Meta, StoryObj } from '@storybook/react';
import { Button, Flex } from '@radix-ui/themes';

const meta: Meta<typeof Button> = {
  title: 'Radix/Button',
  component: Button,
  tags: ['autodocs'],
  argTypes: {
    variant: { control: 'select', options: ['classic', 'solid', 'soft', 'surface', 'outline', 'ghost'] },
    size: { control: 'select', options: ['1', '2', '3', '4'] },
    color: { control: 'select', options: ['accent', 'gray', 'red', 'crimson', 'blue', 'green', 'amber'] },
  },
};
export default meta;

type Story = StoryObj<typeof Button>;

export const Variants: Story = {
  render: (args) => (
    <Flex gap="3" wrap="wrap">
      <Button {...args} variant="classic">拨号</Button>
      <Button {...args} variant="solid">Solid</Button>
      <Button {...args} variant="soft">Soft</Button>
      <Button {...args} variant="surface">Surface</Button>
      <Button {...args} variant="outline">Outline</Button>
      <Button {...args} variant="ghost">Ghost</Button>
    </Flex>
  ),
};

export const SemanticColors: Story = {
  render: () => (
    <Flex gap="3" wrap="wrap">
      <Button variant="solid" color="accent">连接(accent)</Button>
      <Button variant="soft" color="gray">取消(gray)</Button>
      <Button variant="solid" color="red">挂断(red)</Button>
      <Button variant="soft" color="amber">警告(amber)</Button>
      <Button variant="outline" color="green">在线(green)</Button>
    </Flex>
  ),
};

export const Sizes: Story = {
  render: () => (
    <Flex gap="3" align="center">
      <Button size="1" variant="solid">Size 1</Button>
      <Button size="2" variant="solid">Size 2</Button>
      <Button size="3" variant="solid">Size 3</Button>
      <Button size="4" variant="solid">Size 4</Button>
    </Flex>
  ),
};

export const Playground: Story = {
  args: { children: '拨号', variant: 'classic', color: 'accent', size: '2' },
};
