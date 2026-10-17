import type { Meta, StoryObj } from '@storybook/react-vite';
import { type ComponentProps, useEffect, useState } from 'react';
import { fn } from 'storybook/test';
import DictationDialog from './DictationDialog';

type StoryProps = ComponentProps<typeof DictationDialog> & {
  input: 'speech and pauses' | 'continuous speech' | 'silence';
};

// Exercise the real modal with simulated input; never open the microphone or call a provider.
const SimulatedDictation = (args: StoryProps) => {
  const [levels, setLevels] = useState(args.levels);
  useEffect(() => {
    setLevels(args.levels);
    if (args.phase !== 'recording') return;
    const started = performance.now();
    const timer = window.setInterval(() => {
      const seconds = (performance.now() - started) / 1000;
      const phrase = seconds % 7;
      const speaking =
        args.input === 'continuous speech' ||
        (args.input === 'speech and pauses' &&
          phrase < 4.6 &&
          !(phrase > 1.4 && phrase < 1.7) &&
          !(phrase > 3.1 && phrase < 3.45));
      const level = speaking
        ? 0.12 +
          0.58 * Math.abs(Math.sin(seconds * 6.7)) * (0.6 + 0.4 * Math.sin(seconds * 17) ** 2)
        : 0;
      setLevels((history) => [...history.slice(1), level]);
    }, 50);
    return () => window.clearInterval(timer);
  }, [args.input, args.levels, args.phase]);
  return <DictationDialog {...args} levels={levels} />;
};

const meta = {
  title: 'Chat/DictationDialog',
  component: DictationDialog,
  render: (args) => <SimulatedDictation {...args} />,
  parameters: { layout: 'fullscreen' },
  argTypes: {
    input: {
      control: 'radio',
      options: ['speech and pauses', 'continuous speech', 'silence'],
    },
  },
  args: {
    phase: 'recording',
    error: null,
    elapsedSeconds: 12,
    levels: Array<number>(32).fill(0),
    transcript: '',
    transcribing: false,
    input: 'speech and pauses',
    stop: fn(),
    cancel: fn(),
    onReturnFocus: fn(),
  },
} satisfies Meta<StoryProps>;

export default meta;
type Story = StoryObj<typeof meta>;

export const QuieterBars: Story = {};
export const Silence: Story = { args: { input: 'silence' } };
export const Finishing: Story = {
  args: {
    phase: 'finishing',
    transcribing: true,
    transcript: 'Please make the waveform smoother.',
  },
};
export const Review: Story = {
  args: { phase: 'review', transcript: 'Please make the waveform smoother.' },
};
