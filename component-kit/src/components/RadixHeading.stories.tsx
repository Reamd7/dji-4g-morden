import type { Meta, StoryObj } from '@storybook/react';
import { Heading } from '@radix-ui/themes';

const meta: Meta<typeof Heading> = {
  title: 'Radix/Heading',
  component: Heading,
  tags: ['autodocs'],
  argTypes: {
    size: { control: 'select', options: ['1', '2', '3', '4', '5', '6', '7', '8', '9'] },
    weight: { control: 'select', options: ['regular', 'medium', 'bold'] },
  },
};
export default meta;

type Story = StoryObj<typeof Heading>;

export const Sizes: Story = {
  render: () => (
    <>
      {(['1', '2', '3', '4', '5', '6', '7', '8', '9'] as const).map((s) => (
        <div key={s}>
          <Heading size={s}>设备发现 {s}</Heading>
        </div>
      ))}
    </>
  ),
};

export const Playground: Story = {
  args: { children: '设备发现', size: '6' },
};
