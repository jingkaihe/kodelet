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
    .filter((block) => block.type === 'text' || block.type === 'slash-command')
    .map((block) => block.text?.trim() || '')
    .filter(Boolean)
    .join('\n');
};

const summaryForContent = (content: string | ContentBlock[]): string => {
  const text = textForContent(content).trim();
  const imageCount = imageCountForContent(content);
  const imageSuffix =
    imageCount > 0 ? `with ${imageCount === 1 ? 'a screenshot' : `${imageCount} screenshots`}` : '';

  if (text && imageSuffix) {
    return `${text} · ${imageSuffix}`;
  }

  return text || imageSuffix;
};

const PendingSteerList = ({ messages }: PendingSteerListProps) => {
  if (messages.length === 0) {
    return null;
  }

  const label = messages.length === 1 ? 'Queued message' : 'Queued messages';

  return (
    <section
      aria-label={label}
      className="pending-steer-shell mx-auto w-full max-w-5xl px-3 sm:px-4 md:px-8"
      data-testid="pending-steer-list"
    >
      <div className="pending-steer-content">
        <p className="pending-steer-copy">{label}</p>
        <ul className="pending-steer-lines">
          {messages.map((message, index) => (
            <li key={`${index}-${summaryForContent(message.content)}`}>
              <p className="pending-steer-message">{summaryForContent(message.content)}</p>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
};

export default PendingSteerList;
