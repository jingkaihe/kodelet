import type { Meta, StoryObj } from '@storybook/react-vite';
import ChatStreamingIndicator, { StreamingMark } from './ChatStreamingIndicator';

const meta = {
  title: 'Chat/ChatStreamingIndicator',
  component: ChatStreamingIndicator,
  parameters: {
    layout: 'padded',
  },
  args: {
    assistantTurnCount: 1,
  },
} satisfies Meta<typeof ChatStreamingIndicator>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Working: Story = {};

export const LaterTurn: Story = {
  args: {
    assistantTurnCount: 4,
  },
};

export const MarkOnly: Story = {
  render: () => <StreamingMark />,
};
