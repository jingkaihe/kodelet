import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import TerminalModalFrame from './TerminalModalFrame';

const terminalPreview = (
  <pre className="m-0 whitespace-pre-wrap font-mono text-[0.78rem] leading-5 text-[#f4eee3]">
    <span className="text-[#a6bf79]">$</span> npm run storybook:build{'\n'}
    vite v6.4.2 building for production...{'\n'}
    ✓ 2252 modules transformed.{'\n'}
    Storybook build completed successfully
  </pre>
);

const meta = {
  title: 'Workspace/TerminalModalFrame',
  component: TerminalModalFrame,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    children: terminalPreview,
    currentStatus: 'Connected',
    cwdLabel: '/home/jingkaihe/workspace/kodelet',
    statusVariant: 'live',
    terminalSize: {
      width: 980,
      height: 620,
    },
    onClose: fn(),
    onResizeStart: fn(),
  },
} satisfies Meta<typeof TerminalModalFrame>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Connected: Story = {};

export const Connecting: Story = {
  args: {
    currentStatus: 'Connecting…',
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
