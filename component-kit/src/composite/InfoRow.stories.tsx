import type { Meta, StoryObj } from '@storybook/react';
import { Flex } from '@radix-ui/themes';
import { InfoRow } from './InfoRow';

const meta: Meta<typeof InfoRow> = {
  title: 'Composite/InfoRow',
  component: InfoRow,
  tags: ['autodocs'],
};
export default meta;

type Story = StoryObj<typeof InfoRow>;

export const Single: Story = {
  args: { label: 'VID:PID', value: '2c7c:0125' },
};

export const DeviceDetailRows: Story = {
  render: () => (
    <Flex direction="column" gap="2">
      <InfoRow label="IMEI" value="860000000000000" />
      <InfoRow label="ICCID" value="89000000000000000000" />
      <InfoRow label="运营商" value="<运营商>" />
      <InfoRow label="信号" value="26 · -61 dBm" />
      <InfoRow label="固件" value="QDC507GLEFM21" />
    </Flex>
  ),
};
