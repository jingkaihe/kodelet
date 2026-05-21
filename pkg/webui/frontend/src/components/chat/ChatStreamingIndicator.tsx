import React, { useEffect, useState } from 'react';
import { cn } from '../../utils';

export interface AnimatedStreamingText {
  current: string;
  next: string;
  progress: number;
  phase: 'hold' | 'dissolve' | 'build';
}

export const STREAMING_INDICATOR_MESSAGES = [
  'Following the thread…',
  'Gathering the next clue…',
  'Composing the next move…',
  'Tracing the shape of the answer…',
  'Pulling the pieces together…',
  'Working through the details…',
];

export const getAnimatedStreamingText = (
  messages: string[],
  assistantTurnCount: number,
  frame: number
): AnimatedStreamingText => {
  const framesPerMessage = 36;
  const holdFrames = 18;
  const transitionFrames = framesPerMessage - holdFrames;
  const cycleIndex = Math.floor(frame / framesPerMessage);
  const frameInCycle = frame % framesPerMessage;
  const currentIndex = (Math.max(assistantTurnCount - 1, 0) + cycleIndex) % messages.length;
  const nextIndex = (currentIndex + 1) % messages.length;
  const current = messages[currentIndex];
  const next = messages[nextIndex];
  const progress = frameInCycle < holdFrames
    ? 0
    : (frameInCycle - holdFrames) / Math.max(transitionFrames - 1, 1);

  if (frameInCycle < holdFrames) {
    return { current, next, progress: 0, phase: 'hold' };
  }

  return {
    current: progress < 0.5 ? current : next,
    next,
    progress,
    phase: progress < 0.5 ? 'dissolve' : 'build',
  };
};

export const StreamingMark: React.FC = () => (
  <span className="chat-streaming-mark" aria-hidden="true">
    <svg viewBox="0 0 28 28" role="img">
      <circle className="chat-streaming-mark-core" cx="14" cy="14" r="3.45" />
      <g className="chat-streaming-mark-ring chat-streaming-mark-ring-slow">
        <circle cx="14" cy="3.8" r="1.2" />
        <circle cx="22.8" cy="9" r="0.72" />
        <circle cx="23.4" cy="19.2" r="0.92" />
        <circle cx="8.2" cy="23" r="0.72" />
        <circle cx="4.4" cy="11.8" r="0.9" />
      </g>
      <g className="chat-streaming-mark-ring chat-streaming-mark-ring-fast">
        <circle cx="18.7" cy="5.9" r="0.48" />
        <circle cx="25" cy="14.6" r="0.55" />
        <circle cx="15" cy="25" r="0.42" />
        <circle cx="5.8" cy="18.5" r="0.5" />
      </g>
      <path className="chat-streaming-mark-trail" d="M7 6.8C10.8 3.9 17.8 3.5 22 8" />
      <path className="chat-streaming-mark-trail chat-streaming-mark-trail-late" d="M21.5 21.2C17.6 24.4 10.6 24.2 6.5 19.7" />
    </svg>
  </span>
);

const StreamingText: React.FC<{
  text: string;
  animation: AnimatedStreamingText;
}> = ({ text, animation }) => (
  <span
    className={cn('chat-streaming-label', `chat-streaming-label-${animation.phase}`)}
    aria-label={animation.next}
    style={{ '--stream-progress': animation.progress } as React.CSSProperties}
  >
    <span className="chat-streaming-label-text">{text}</span>
    <span className="chat-streaming-label-dust" aria-hidden="true">
      <span />
      <span />
      <span />
      <span />
      <span />
      <span />
      <span />
      <span />
    </span>
  </span>
);

interface ChatStreamingIndicatorProps {
  assistantTurnCount: number;
}

const ChatStreamingIndicator: React.FC<ChatStreamingIndicatorProps> = ({
  assistantTurnCount,
}) => {
  const [streamingTextFrame, setStreamingTextFrame] = useState(0);

  useEffect(() => {
    setStreamingTextFrame(0);

    const intervalId = window.setInterval(() => {
      setStreamingTextFrame((currentValue) => currentValue + 1);
    }, 150);

    return () => {
      window.clearInterval(intervalId);
    };
  }, [assistantTurnCount]);

  const animatedStreamingText = getAnimatedStreamingText(
    STREAMING_INDICATOR_MESSAGES,
    assistantTurnCount,
    streamingTextFrame
  );

  return (
    <div className="chat-streaming-indicator" aria-label="Kodelet is working">
      <StreamingMark />
      <StreamingText text={animatedStreamingText.current} animation={animatedStreamingText} />
    </div>
  );
};

export default ChatStreamingIndicator;
