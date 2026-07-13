import type { Meta, StoryObj } from '@storybook/react';
import { Card, Text, Flex } from '@radix-ui/themes';

const meta: Meta<typeof Card> = {
  title: 'Radix/Card',
  component: Card,
  tags: ['autodocs'],
  argTypes: {
    size: { control: 'select', options: ['1', '2', '3', '4', '5'] },
    variant: { control: 'select', options: ['surface', 'classic'] },
  },
};
export default meta;

type Story = StoryObj<typeof Card>;

export const Sizes: Story = {
  render: () => (
    <Flex gap="3" wrap="wrap">
      {(['1', '2', '3', '4', '5'] as const).map((s) => (
        <Card key={s} size={s}>
          <Text size="2">Size {s}</Text>
        </Card>
      ))}
    </Flex>
  ),
};

export const DeviceCardExample: Story = {
  render: () => (
    <Card size="3">
      <Flex direction="column" gap="2">
        <Text size="4" weight="bold">QDC507</Text>
        <Flex justify="between">
          <Text size="2" color="gray">VID:PID</Text>
          <Text size="2" color="gray" highContrast>2c7c:0125</Text>
        </Flex>
        <Flex justify="between">
          <Text size="2" color="gray">接口数</Text>
          <Text size="2" color="gray" highContrast>5</Text>
        </Flex>
      </Flex>
    </Card>
  ),
};

export const Playground: Story = {
  args: { children: 'Card content', size: '3' },
};
