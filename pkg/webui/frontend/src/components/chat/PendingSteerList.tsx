import type { ContentBlock, Message } from '../../types';

interface PendingSteerListProps {
  messages: Message[];
}

const imageCountForContent = (content: string | ContentBlock[]): number => {
  if (!Array.isArray(content)) {
    return 0;
  }
  return content.filter((block) => block.type === 'image').length;
};

const textForContent = (content: string | ContentBlock[]): string => {
  if (typeof content === 'string') {
    return content;
  }

  return content
    .filter((block) => block.type === 'text')
    .map((block) => block.text?.trim() || '')
    .filter(Boolean)
    .join('\n');
};

const summaryForContent = (content: string | ContentBlock[]): string => {
  const text = textForContent(content).trim();
  const imageCount = imageCountForContent(content);
  const imageSuffix = imageCount > 0 ? `with ${imageCount === 1 ? 'a screenshot' : `${imageCount} screenshots`}` : '';

  if (text && imageSuffix) {
    return `${text} · ${imageSuffix}`;
  }

  return text || imageSuffix;
};

const PendingSteerList = ({ messages }: PendingSteerListProps) => {
  if (messages.length === 0) {
    return null;
  }

  return (
    <section className="pending-steer-shell" data-testid="pending-steer-list">
      <div className="pending-steer-header">
        <p className="pending-steer-copy">
          Kodelet will use this guidance as soon as it continues.
        </p>
      </div>
      <div className="pending-steer-lines">
        {messages.map((message, index) => (
          <div className="pending-steer-line" key={`${index}-${summaryForContent(message.content)}`}>
            <span className="pending-steer-prompt">↳</span>
            <code className="pending-steer-message">{summaryForContent(message.content)}</code>
          </div>
        ))}
      </div>
    </section>
  );
};

export default PendingSteerList;
