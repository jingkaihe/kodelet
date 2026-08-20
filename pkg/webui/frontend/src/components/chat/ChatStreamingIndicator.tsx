import type { SpinnerPreset } from '../Spinner';
import Spinner, { useSpinnerFrame } from '../Spinner';

export const STREAMING_INDICATOR_MESSAGES = [
  'Following the thread…',
  'Gathering the next clue…',
  'Composing the next move…',
  'Tracing the shape of the answer…',
  'Pulling the pieces together…',
  'Working through the details…',
] as const;

const STREAMING_MESSAGE_SPINNER = {
  frames: STREAMING_INDICATOR_MESSAGES,
  intervalMs: 2400,
} satisfies SpinnerPreset<string>;

export const getStreamingIndicatorMessage = (
  messages: readonly string[],
  assistantTurnCount: number,
  frameIndex: number
): string => {
  if (messages.length === 0) {
    return '';
  }

  const startIndex = Math.max(assistantTurnCount - 1, 0);
  return messages[(startIndex + Math.max(frameIndex, 0)) % messages.length];
};

interface ChatStreamingIndicatorProps {
  assistantTurnCount: number;
}

const ChatStreamingIndicator = ({ assistantTurnCount }: ChatStreamingIndicatorProps) => {
  const { frameIndex } = useSpinnerFrame(STREAMING_MESSAGE_SPINNER, assistantTurnCount);
  const streamingMessage = getStreamingIndicatorMessage(
    STREAMING_INDICATOR_MESSAGES,
    assistantTurnCount,
    frameIndex
  );

  return (
    <div className="chat-streaming-indicator" aria-label="Kodelet is working">
      <Spinner className="chat-streaming-spinner" resetKey={assistantTurnCount} />
      <span className="chat-streaming-label">{streamingMessage}</span>
    </div>
  );
};

export default ChatStreamingIndicator;
