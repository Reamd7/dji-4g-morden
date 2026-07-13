import type { Meta, StoryObj } from '@storybook/react';
import { DeviceCard } from './DeviceCard';

const meta: Meta<typeof DeviceCard> = {
  title: 'Composite/DeviceCard',
  component: DeviceCard,
  tags: ['autodocs'],
};
export default meta;

type Story = StoryObj<typeof DeviceCard>;

export const Connected: Story = {
  args: {
    model: 'QDC507',
    imei: '860000000000000',
    iccid: '89000000000000000000',
    carrier: '<运营商>',
    signalRssi: 26,
    connected: true,
  },
};

export const Disconnected: Story = {
  args: {
    model: 'QDC507',
    imei: '860000000000000',
    iccid: '89000000000000000000',
    carrier: '—',
    connected: false,
  },
};

export const WeakSignal: Story = {
  args: {
    model: 'QDC507',
    imei: '860000000000000',
    iccid: '89000000000000000000',
    carrier: '<运营商>',
    signalRssi: 8,
    connected: true,
  },
};
