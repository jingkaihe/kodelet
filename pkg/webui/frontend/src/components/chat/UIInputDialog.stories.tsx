import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import UIInputDialog from './UIInputDialog';

const meta = {
  title: 'Chat/UIInputDialog',
  component: UIInputDialog,
  parameters: { layout: 'fullscreen' },
  decorators: [
    (Story) => (
      <div className="relative h-dvh">
        <Story />
      </div>
    ),
  ],
  args: {
    mode: 'confirm',
    request: {
      id: 'schedule-update',
      title: 'Update schedule "joke-2242"?',
      message: `Instruction:
Think of a short, family-friendly programming joke. Then use AppleScript via the osascript command to deliver it on this Mac: show it with \`display notification "<joke>" with title "Joke time"\` and speak it aloud with \`say "<joke>"\`. Escape any double quotes in the joke properly. Reply with the joke you told.`,
      confirmButtonText: 'Update',
    },
    submitting: false,
    onCancel: fn(),
    onSubmit: fn(),
  },
} satisfies Meta<typeof UIInputDialog>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Confirm: Story = {
  play: async ({ args, canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: 'Update' }));
    await expect(args.onSubmit).toHaveBeenCalledWith('');
  },
};

export const ConfirmWithoutMessage: Story = {
  args: {
    request: {
      id: 'terminal-close',
      title: 'Close tab?',
      confirmButtonText: 'Close',
    },
  },
};

export const Input: Story = {
  args: {
    mode: 'input',
    request: {
      id: 'release-name',
      title: 'Name this release',
      helpText: 'Used as the tag annotation and the changelog heading.',
      placeholder: 'e.g. v0.9.0',
      defaultValue: 'v0.9.0',
      required: true,
      submitButtonText: 'Tag release',
    },
  },
};

export const Select: Story = {
  args: {
    mode: 'select',
    request: {
      id: 'deploy-target',
      title: 'Deploy to which environment?',
      message: 'The build passed all checks. Pick where the extension should deploy it.',
      options: ['staging', 'production-eu', 'production-us'],
      submitButtonText: 'Deploy',
    },
  },
};

export const Submitting: Story = {
  args: {
    submitting: true,
  },
};
