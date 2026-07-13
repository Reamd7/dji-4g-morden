import type { Meta, StoryObj } from '@storybook/react';
import { StatusBadge } from './StatusBadge';

const meta: Meta<typeof StatusBadge> = {
  title: 'Components/StatusBadge',
  component: StatusBadge,
  tags: ['autodocs'],
  argTypes: {
    status: { control: 'select', options: ['connected', 'disconnected', 'connecting', 'error'] },
  },
};
export default meta;

type Story = StoryObj<typeof StatusBadge>;

export const Connected: Story = { args: { status: 'connected' } };
export const Disconnected: Story = { args: { status: 'disconnected' } };
export const Connecting: Story = { args: { status: 'connecting' } };
export const Error: Story = { args: { status: 'error' } };
export const CustomLabel: Story = { args: { status: 'connected', label: 'QDC507 已连接' } };
