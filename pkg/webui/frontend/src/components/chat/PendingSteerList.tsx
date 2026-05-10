import type { ChatRenderMessage, ContentBlock, Message } from '../../types';
import ChatTranscript from './ChatTranscript';

interface PendingSteerListProps {
  messages: Message[];
}

const imageCountForContent = (content: string | ContentBlock[]): number => {
  if (!Array.isArray(content)) {
    return 0;
  }
  return content.filter((block) => block.type === 'image').length;
};

const PendingSteerList = ({ messages }: PendingSteerListProps) => {
  if (messages.length === 0) {
    return null;
  }

  const renderedMessages: ChatRenderMessage[] = messages.map((message) => ({
    role: 'user',
    content: message.content,
  }));

  const imageCount = messages.reduce(
    (total, message) => total + imageCountForContent(message.content),
    0
  );

  return (
    <section className="pending-steer-shell" data-testid="pending-steer-list">
      <div className="pending-steer-header">
        <div>
          <p className="pending-steer-eyebrow">Queued steering</p>
          <p className="pending-steer-copy">
            Will be added as your next guidance when the agent makes another model call.
          </p>
        </div>
        <span className="pending-steer-count">
          {messages.length} message{messages.length === 1 ? '' : 's'}
          {imageCount > 0 ? ` · ${imageCount} image${imageCount === 1 ? '' : 's'}` : ''}
        </span>
      </div>
      <div className="pending-steer-transcript">
        <ChatTranscript
          emptyStateTitle=""
          isStreaming={false}
          messages={renderedMessages}
        />
      </div>
    </section>
  );
};

export default PendingSteerList;
