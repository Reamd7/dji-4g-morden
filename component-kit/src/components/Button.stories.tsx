import type { Meta, StoryObj } from '@storybook/react';
import { Button } from './Button';

const meta: Meta<typeof Button> = {
  title: 'Components/Button',
  component: Button,
  tags: ['autodocs'],
  argTypes: {
    variant: { control: 'select', options: ['primary', 'secondary', 'danger', 'ghost'] },
    size: { control: 'select', options: ['small', 'medium', 'large'] },
  },
};
export default meta;

type Story = StoryObj<typeof Button>;

export const Primary: Story = { args: { children: '拨号', variant: 'primary' } };
export const Secondary: Story = { args: { children: '取消', variant: 'secondary' } };
export const Danger: Story = { args: { children: '挂断', variant: 'danger' } };
export const Ghost: Story = { args: { children: '高级设置', variant: 'ghost' } };
export const Large: Story = { args: { children: '连接设备', size: 'large', variant: 'primary' } };
export const Small: Story = { args: { children: '刷新', size: 'small', variant: 'ghost' } };
