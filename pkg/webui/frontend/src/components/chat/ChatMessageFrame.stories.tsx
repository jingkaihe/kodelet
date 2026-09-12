import type { Meta, StoryObj } from '@storybook/react-vite';
import ChatMessageFrame from './ChatMessageFrame';

const meta = {
  title: 'Chat/ChatMessageFrame',
  component: ChatMessageFrame,
  parameters: {
    layout: 'padded',
  },
  args: {
    copyText: 'Copyable message text',
    messageRole: 'user',
    children: (
      <div className="chat-prose max-w-none text-kodelet-dark">
        <p>Extract this panel so message chrome can be tested in isolation.</p>
      </div>
    ),
  },
  argTypes: {
    messageRole: {
      control: 'inline-radio',
      options: ['user', 'assistant'],
    },
  },
} satisfies Meta<typeof ChatMessageFrame>;

export default meta;

type Story = StoryObj<typeof meta>;

export const UserMessage: Story = {};

export const AssistantMessage: Story = {
  args: {
    copyText: '',
    messageRole: 'assistant',
    children: (
      <div className="chat-prose max-w-none text-kodelet-dark">
        <p>The transcript can now compose this frame around assistant blocks.</p>
      </div>
    ),
  },
};
