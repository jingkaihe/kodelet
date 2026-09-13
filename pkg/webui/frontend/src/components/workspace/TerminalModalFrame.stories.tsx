import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, userEvent, within } from 'storybook/test';
import TerminalModalFrame from './TerminalModalFrame';

const terminalPreview = (
  <pre className="m-0 whitespace-pre-wrap font-mono text-[0.78rem] leading-5 text-[var(--terminal-foreground)]">
    <span className="text-[var(--terminal-green)]">$</span> npm run storybook:build{'\n'}
    vite v6.4.2 building for production...{'\n'}✓ 2252 modules transformed.{'\n'}
    Storybook build completed successfully
  </pre>
);

const meta = {
  title: 'Workspace/TerminalPanelFrame',
  component: TerminalModalFrame,
  decorators: [
    (Story) => (
      <div className="flex h-dvh min-h-0">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    children: terminalPreview,
    currentStatus: '',
    cwdLabel: '/home/jingkaihe/workspace/kodelet',
    statusVariant: 'live',
    onClose: fn(),
    onTerminalInput: fn(),
    onTerminalArrow: fn(),
  },
} satisfies Meta<typeof TerminalModalFrame>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Connected: Story = {};

export const Mobile: Story = {
  render: (args) => (
    <div className="flex h-[568px] w-[320px] max-w-full">
      <TerminalModalFrame {...args} />
    </div>
  ),
  play: async ({ canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    const canvas = within(canvasElement);
    const toggle = canvas.getByRole('button', { name: 'More keys' });
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(canvas.getByRole('button', { name: 'Ctrl+C' })).toBeVisible();

    const panel = canvas.getByTestId('terminal-panel');
    expect(panel.scrollWidth).toBeLessThanOrEqual(panel.clientWidth);
    for (const name of ['Terminal keys', 'More terminal keys']) {
      const strip = canvas.getByRole('group', { name });
      expect(strip.scrollWidth).toBeGreaterThan(strip.clientWidth);
    }
    for (const button of canvas.getAllByRole('button')) {
      const bounds = button.getBoundingClientRect();
      expect(bounds.width).toBeGreaterThanOrEqual(44);
      expect(bounds.height).toBeGreaterThanOrEqual(44);
    }
    const toggleBounds = toggle.getBoundingClientRect();
    const panelBounds = panel.getBoundingClientRect();
    expect(toggleBounds.right).toBeLessThanOrEqual(panelBounds.right);
    expect(toggleBounds.bottom).toBeLessThanOrEqual(panelBounds.bottom);
  },
};

export const TabletLandscape: Story = {
  render: (args) => (
    <div className="flex h-[600px] w-[1024px] max-w-full">
      <TerminalModalFrame {...args} />
    </div>
  ),
};

export const Connecting: Story = {
  args: {
    children: undefined,
    currentStatus: 'Connecting',
    statusVariant: 'connecting',
  },
};

export const Exited: Story = {
  args: {
    currentStatus: 'Exited with code 0',
    statusVariant: 'idle',
  },
};

export const ConnectionError: Story = {
  args: {
    currentStatus: 'Terminal connection failed',
    statusVariant: 'error',
  },
};
