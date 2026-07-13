import type { Meta, StoryObj } from '@storybook/react';
import { Flex, Text, Box } from '@radix-ui/themes';

const meta: Meta<typeof Flex> = {
  title: 'Radix/Flex',
  component: Flex,
  tags: ['autodocs'],
  argTypes: {
    direction: { control: 'select', options: ['row', 'column', 'row-reverse', 'column-reverse'] },
    align: { control: 'select', options: ['start', 'center', 'end', 'stretch', 'baseline'] },
    justify: { control: 'select', options: ['start', 'center', 'end', 'between', 'around'] },
    gap: { control: 'select', options: ['1', '2', '3', '4', '5', '6', '7', '8', '9'] },
  },
};
export default meta;

type Story = StoryObj<typeof Flex>;

const Dot = ({ label }: { label: string }) => (
  <Box style={{ background: 'var(--iris-9)', color: 'var(--gray-12)', mixBlendMode: 'difference', padding: 'var(--space-2)' }}>
    <Text size="1">{label}</Text>
  </Box>
);

export const DirectionGap: Story = {
  render: () => (
    <Flex direction="column" gap="4">
      <Flex gap="2" align="center">
        <Text size="2" color="gray">row gap=2:</Text>
        <Dot label="A" /><Dot label="B" /><Dot label="C" />
      </Flex>
      <Flex direction="column" gap="2">
        <Text size="2" color="gray">column gap=2:</Text>
        <Dot label="A" /><Dot label="B" />
      </Flex>
      <Flex justify="between">
        <Text size="2" color="gray">justify=between:</Text>
        <Dot label="左" /><Dot label="右" />
      </Flex>
    </Flex>
  ),
};

export const GapScale: Story = {
  render: () => (
    <Flex direction="column" gap="3">
      {(['1', '2', '3', '4', '5'] as const).map((g) => (
        <Flex key={g} gap={g} align="center">
          <Text size="1" color="gray">gap={g}</Text>
          <Dot label="•" /><Dot label="•" /><Dot label="•" />
        </Flex>
      ))}
    </Flex>
  ),
};
