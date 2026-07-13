import type { Meta, StoryObj } from '@storybook/react';
import { SignalBar } from './SignalBar';

const meta: Meta<typeof SignalBar> = {
  title: 'Components/SignalBar',
  component: SignalBar,
  tags: ['autodocs'],
  argTypes: {
    rssi: { control: { type: 'range', min: 0, max: 31, step: 1 } },
  },
};
export default meta;

type Story = StoryObj<typeof SignalBar>;

export const Excellent: Story = { args: { rssi: 28, label: '信号' } };
export const Good: Story = { args: { rssi: 20, label: '信号' } };
export const Weak: Story = { args: { rssi: 8, label: '信号' } };
export const NoSignal: Story = { args: { rssi: 99, label: '信号' } };
