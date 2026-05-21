import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import ChatStreamingIndicator, {
  STREAMING_INDICATOR_MESSAGES,
  getAnimatedStreamingText,
} from './ChatStreamingIndicator';

describe('ChatStreamingIndicator', () => {
  it('renders an accessible working indicator', () => {
    render(<ChatStreamingIndicator assistantTurnCount={1} />);

    expect(screen.getByLabelText('Kodelet is working')).toBeInTheDocument();
    expect(screen.getByText(STREAMING_INDICATOR_MESSAGES[0])).toBeInTheDocument();
  });

  it('selects stable text frames from assistant turn count and frame', () => {
    expect(getAnimatedStreamingText(STREAMING_INDICATOR_MESSAGES, 2, 0)).toEqual({
      current: STREAMING_INDICATOR_MESSAGES[1],
      next: STREAMING_INDICATOR_MESSAGES[2],
      phase: 'hold',
      progress: 0,
    });

    const transition = getAnimatedStreamingText(STREAMING_INDICATOR_MESSAGES, 2, 24);
    expect(transition.phase).toBe('dissolve');
    expect(transition.current).toBe(STREAMING_INDICATOR_MESSAGES[1]);
    expect(transition.next).toBe(STREAMING_INDICATOR_MESSAGES[2]);
    expect(transition.progress).toBeGreaterThan(0);
  });
});
