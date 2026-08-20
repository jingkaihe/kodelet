import React from 'react';
import { cn } from '../utils';

const REDUCED_MOTION_MEDIA_QUERY = '(prefers-reduced-motion: reduce)';
const LOW_POWER_SPINNER_INTERVAL_MS = 125;

export interface SpinnerPreset<TFrame> {
  frames: readonly [TFrame, ...TFrame[]];
  intervalMs: number;
}

export const TUI_DOT_SPINNER = {
  frames: ['⣾', '⣽', '⣻', '⢿', '⡿', '⣟', '⣯', '⣷'],
  intervalMs: LOW_POWER_SPINNER_INTERVAL_MS,
} satisfies SpinnerPreset<string>;

export const useSpinnerFrame = <TFrame,>(
  preset: SpinnerPreset<TFrame>,
  resetKey?: React.Key
): { frame: TFrame; frameIndex: number } => {
  const [frameIndex, setFrameIndex] = React.useState(0);

  React.useEffect(() => {
    setFrameIndex(0);

    if (
      preset.frames.length < 2 ||
      (typeof window !== 'undefined' &&
        window.matchMedia?.(REDUCED_MOTION_MEDIA_QUERY).matches)
    ) {
      return undefined;
    }

    const intervalId = window.setInterval(() => {
      setFrameIndex((currentFrame) => (currentFrame + 1) % preset.frames.length);
    }, preset.intervalMs);

    return () => window.clearInterval(intervalId);
  }, [preset.frames, preset.intervalMs, resetKey]);

  const normalizedFrameIndex = frameIndex % preset.frames.length;
  return {
    frame: preset.frames[normalizedFrameIndex],
    frameIndex: normalizedFrameIndex,
  };
};

interface SpinnerProps extends Omit<React.HTMLAttributes<HTMLSpanElement>, 'children'> {
  preset?: SpinnerPreset<string>;
  resetKey?: React.Key;
}

export const Spinner: React.FC<SpinnerProps> = ({
  className,
  preset = TUI_DOT_SPINNER,
  resetKey,
  ...props
}) => {
  const { frame } = useSpinnerFrame(preset, resetKey);

  return (
    <span
      aria-hidden={props['aria-label'] ? undefined : true}
      {...props}
      className={cn('spinner-glyph', className)}
    >
      {frame}
    </span>
  );
};

export default Spinner;
