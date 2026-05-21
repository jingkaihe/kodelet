import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import ChatMessageFrame from './ChatMessageFrame';

const meta = {
  title: 'Chat/ChatMessageFrame',
  component: ChatMessageFrame,
  parameters: {
    layout: 'padded',
  },
  args: {
    copyText: 'Copyable message text',
    role: 'user',
    children: (
      <div className="chat-prose max-w-none text-kodelet-dark">
        <p>Extract this panel so message chrome can be tested in isolation.</p>
      </div>
    ),
  },
  argTypes: {
    role: {
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
    role: 'assistant',
    children: (
      <div className="space-y-3">
        <p className="text-sm text-kodelet-dark">
          The transcript can now compose this frame around assistant blocks.
        </p>
        <button className="composer-capsule" onClick={fn()} type="button">
          Inline action
        </button>
      </div>
    ),
  },
};
