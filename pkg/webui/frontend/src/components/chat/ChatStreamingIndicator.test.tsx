import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import ChatStreamingIndicator, {
  STREAMING_INDICATOR_MESSAGES,
  getStreamingIndicatorMessage,
} from './ChatStreamingIndicator';

describe('ChatStreamingIndicator', () => {
  it('renders an accessible working indicator', () => {
    render(<ChatStreamingIndicator assistantTurnCount={1} />);

    expect(screen.getByLabelText('Kodelet is working')).toBeInTheDocument();
    expect(screen.getByText(STREAMING_INDICATOR_MESSAGES[0])).toBeInTheDocument();
    expect(document.querySelector('.chat-streaming-spinner')).toHaveTextContent('⣾');
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
