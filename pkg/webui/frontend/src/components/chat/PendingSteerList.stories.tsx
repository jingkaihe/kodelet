import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, within } from 'storybook/test';
import { sampleAttachment } from '../../stories/fixtures';
import PendingSteerList from './PendingSteerList';

const meta = {
  title: 'Chat/PendingSteerList',
  component: PendingSteerList,
  parameters: {
    layout: 'padded',
  },
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    if (args.messages.length === 0) {
      expect(canvas.queryByRole('region', { name: /^Queued messages?$/ })).not.toBeInTheDocument();
      return;
    }

    const label = args.messages.length === 1 ? 'Queued message' : 'Queued messages';
    const guidance = canvas.getByRole('region', { name: label });
    expect(within(guidance).getByText(label)).toBeInTheDocument();
    expect(within(guidance).getAllByRole('listitem')).toHaveLength(args.messages.length);
  },
  args: {
    messages: [
      {
        role: 'user',
        content: 'When you continue, focus on the Storybook smoke tests.',
      },
      {
        role: 'user',
        content: [
          { type: 'text', text: 'Also check this screenshot.' },
          { type: 'image', image_url: { url: sampleAttachment.previewUrl } },
        ],
      },
    ],
  },
} satisfies Meta<typeof PendingSteerList>;

export default meta;

type Story = StoryObj<typeof meta>;

export const QueuedGuidance: Story = {};

export const LongQueuedGuidance: Story = {
  args: {
    messages: [
      {
        role: 'user',
        content:
          'Investigate the failed provider request, compare the installed client version with the minimum supported release, verify whether any authentication headers need to change, inspect the request-building path for model-specific behavior, confirm the migration guidance against the current SDK source, and report the required, recommended, and informational follow-up work with evidence before continuing. Include the exact error details, affected model capability checks, and any compatibility risks that should be addressed in a later cleanup.',
      },
    ],
  },
};

export const MultilineGuidance: Story = {
  args: {
    messages: [
      {
        role: 'user',
        content:
          'Keep the change focused:\n- Match the transcript styling.\n- Check the mobile layout.',
      },
    ],
  },
  play: async ({ canvasElement }) => {
    const message = within(canvasElement).getByText(/Keep the change focused:/);
    expect(message.textContent).toContain('\n- Match the transcript styling.\n');
    expect(getComputedStyle(message).whiteSpace).toBe('pre-wrap');
  },
};

export const Empty: Story = {
  args: {
    messages: [],
  },
};
