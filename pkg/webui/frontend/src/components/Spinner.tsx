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

  // biome-ignore lint/correctness/useExhaustiveDependencies(resetKey): A new turn intentionally restarts the animation even when the preset is unchanged.
  React.useEffect(() => {
    setFrameIndex(0);

    if (
      preset.frames.length < 2 ||
      (typeof window !== 'undefined' && window.matchMedia?.(REDUCED_MOTION_MEDIA_QUERY).matches)
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
  const isDotSpinner = preset === TUI_DOT_SPINNER;
  // Draw the same braille frame without depending on system symbol fonts.
  const dots = isDotSpinner ? frame.charCodeAt(0) - 0x2800 : 0;

  return (
    <span
      aria-hidden={props['aria-label'] ? undefined : true}
      {...props}
      className={cn('spinner-glyph', className)}
    >
      <span className={isDotSpinner ? 'sr-only' : undefined}>{frame}</span>
      {isDotSpinner ? (
        <svg aria-hidden="true" viewBox="0 0 10 16" width="1em" height="1em" fill="currentColor">
          {[0, 1, 2, 6, 3, 4, 5, 7].map((bit, position) => (
            <circle
              key={bit}
              cx={position < 4 ? 3 : 7}
              cy={2 + (position % 4) * 4}
              r="1.25"
              opacity={dots & (1 << bit) ? 1 : 0.15}
            />
          ))}
        </svg>
      ) : null}
    </span>
  );
};

export default Spinner;
