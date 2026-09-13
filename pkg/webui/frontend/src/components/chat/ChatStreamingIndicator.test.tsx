import { act, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ChatStreamingIndicator, {
  getStreamingIndicatorMessage,
  STREAMING_INDICATOR_MESSAGES,
} from './ChatStreamingIndicator';

describe('ChatStreamingIndicator', () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it('renders an accessible working indicator', () => {
    render(<ChatStreamingIndicator assistantTurnCount={1} />);

    expect(screen.getByLabelText('Kodelet is working')).toBeInTheDocument();
    expect(screen.getByText(STREAMING_INDICATOR_MESSAGES[0])).toBeInTheDocument();
    expect(document.querySelector('.chat-streaming-spinner')).toHaveTextContent('⣾');
  });

  it('keeps status text static while the busy indicator advances with reduced motion', () => {
    vi.useFakeTimers();
    const motionPreference = Object.assign(new EventTarget(), { matches: true });
    vi.spyOn(window, 'matchMedia').mockReturnValue(motionPreference as MediaQueryList);
    render(<ChatStreamingIndicator assistantTurnCount={1} />);

    act(() => vi.advanceTimersByTime(2500));

    expect(screen.getByText(STREAMING_INDICATOR_MESSAGES[0])).toBeInTheDocument();
    expect(document.querySelector('.chat-streaming-spinner')).toHaveTextContent('⡿');
    expect(vi.getTimerCount()).toBe(1);
  });

  it('selects messages from assistant turn count and discrete frame', () => {
    expect(getStreamingIndicatorMessage(STREAMING_INDICATOR_MESSAGES, 2, 0)).toBe(
      STREAMING_INDICATOR_MESSAGES[1]
    );
    expect(getStreamingIndicatorMessage(STREAMING_INDICATOR_MESSAGES, 2, 1)).toBe(
      STREAMING_INDICATOR_MESSAGES[2]
    );
  });
});
