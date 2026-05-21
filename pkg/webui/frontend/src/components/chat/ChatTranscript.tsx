import React, { useMemo } from 'react';
import { Brain, SquareSlash } from 'lucide-react';
import { marked } from 'marked';
import type {
  ChatAssistantBlock,
  ChatRenderMessage,
  ContentBlock,
} from '../../types';
import ChatMessageFrame from './ChatMessageFrame';
import ChatStreamingIndicator, { StreamingMark } from './ChatStreamingIndicator';
import ChatToolActivity from './ChatToolActivity';
import { CopyButton } from '../tool-renderers/shared';

const renderer = new marked.Renderer();
const defaultRenderer = new marked.Renderer();

renderer.link = (href, title, text) => {
  const renderedLink = defaultRenderer.link(href, title, text);
  if (!renderedLink.startsWith('<a ')) {
    return renderedLink;
  }

  return renderedLink.replace('<a ', '<a class="chat-markdown-link" ');
};

renderer.list = (body, ordered, start) => {
  const tag = ordered ? 'ol' : 'ul';
  const startAttribute = ordered && typeof start === 'number' && start !== 1 ? ` start="${start}"` : '';
  return `<${tag} class="chat-markdown-list"${startAttribute}>\n${body}</${tag}>\n`;
};

const parseMarkdown = (content: string): string => marked.parse(content, { renderer }) as string;

const isSlashCommandText = (text: string): boolean => /^\/[\w./-]+(?:\s|$)/.test(text.trim());

const renderContent = (content: string | ContentBlock[] | undefined): string => {
  if (!content) {
    return '';
  }

  if (typeof content === 'string') {
    return parseMarkdown(content);
  }

  return content
    .map((block) => {
      if (block.type === 'text') {
        return parseMarkdown(block.text || '');
      }

      if (block.type === 'slash-command' || block.type === 'goal') {
        return parseMarkdown(block.text || '');
      }

      if (block.type === 'image') {
        const imageUrl = block.source?.data && block.source?.media_type
          ? `data:${block.source.media_type};base64,${block.source.data}`
          : block.image_url?.url;
        if (!imageUrl) {
          return '';
        }
        return [
          '<figure class="chat-uploaded-image">',
          `<img src="${imageUrl}" alt="Uploaded content" class="chat-uploaded-image-media" loading="lazy" />`,
          '</figure>',
        ].join('');
      }

      return '';
    })
    .join('');
};

const normalizeThinkingMarkdown = (content: string): string =>
  content
    .replace(/([^\n])\n(#{1,6}\s)/g, '$1\n\n$2')
    .replace(/([^\n])\n(\*\*[A-Z][^*\n]+\*\*)/g, '$1\n\n$2')
    .replace(/([.!?])(?=\*\*[A-Z][^*\n]+\*\*)/g, '$1\n\n')
    .replace(/([.!?])(?=#{1,6}\s)/g, '$1\n\n');

const renderThinkingContent = (content: string) => {
  const hasThinkingContent = extractContentText(content).trim().length > 0;

  if (!hasThinkingContent) {
    return (
      <p className="text-sm italic text-kodelet-blue/80">
        Reasoning complete.
      </p>
    );
  }

  return (
    <div
      className="chat-prose max-w-none text-kodelet-dark"
      dangerouslySetInnerHTML={{
        __html: renderContent(normalizeThinkingMarkdown(content)),
      }}
    />
  );
};

const renderCompletedThinkingGroup = (
  thinkingBlocks: Array<Extract<ChatAssistantBlock, { type: 'thinking' }>>,
  key: string
) => {
  const summaryText = thinkingBlocks.length === 1 ? 'Thought' : `${thinkingBlocks.length} Thoughts`;

  return (
    <div key={key} className="activity-stack activity-stack-thinking">
      <details className="activity-card activity-card-thinking">
        <summary className="tool-summary activity-summary" title={summaryText}>
          <span className="tool-summary-chevron" aria-hidden="true">
            ›
          </span>
          <Brain
            aria-hidden="true"
            className="tool-summary-icon tool-summary-icon-thinking"
            size={14}
            strokeWidth={2.2}
          />
          <span className="tool-summary-text" title={summaryText}>
            <span className="tool-summary-label">{summaryText}</span>
          </span>
        </summary>
        <div className="activity-detail-content thinking-group-content">
          {thinkingBlocks.map((thinkingBlock, index) => (
            <section className="thinking-group-item" key={`thought-${index}`}>
              {renderThinkingContent(thinkingBlock.content)}
            </section>
          ))}
        </div>
      </details>
    </div>
  );
};

const extractContentText = (content: string | ContentBlock[] | undefined): string => {
  if (!content) {
    return '';
  }

  if (typeof content === 'string') {
    return content;
  }

  return content
    .map((block) => {
      if (block.type === 'text') {
        return block.text || '';
      }

      if (block.type === 'slash-command' || block.type === 'goal') {
        return block.text || '';
      }

      if (block.type === 'image') {
        return '[image]';
      }

      return '';
    })
    .filter(Boolean)
    .join('\n\n');
};

const getMessageBlockCopyText = (content: string | ContentBlock[] | undefined): string =>
  extractContentText(content);

const renderSlashCommandCard = (text: string) => (
  <div className="slash-command-card" data-testid="slash-command-card">
    <SquareSlash aria-hidden="true" className="slash-command-card-icon" size={14} strokeWidth={2.2} />
    <code className="slash-command-card-command">{text.trim()}</code>
  </div>
);

const renderUserContent = (content: string | ContentBlock[] | undefined): React.ReactNode => {
  if (!content) {
    return null;
  }

  if (typeof content === 'string') {
    return isSlashCommandText(content) ? (
      renderSlashCommandCard(content)
    ) : (
      <div
        className="chat-prose max-w-none text-kodelet-dark"
        dangerouslySetInnerHTML={{ __html: renderContent(content) }}
      />
    );
  }

  return content.map((block, index) => {
    if (block.type === 'slash-command' || block.type === 'goal') {
      return (
        <React.Fragment key={`${block.type}-${index}-${block.text || ''}`}>
          {renderSlashCommandCard(block.text || '')}
        </React.Fragment>
      );
    }

    if (block.type === 'text' && block.text && isSlashCommandText(block.text)) {
      return (
        <React.Fragment key={`slash-text-${index}-${block.text}`}>
          {renderSlashCommandCard(block.text)}
        </React.Fragment>
      );
    }

    return (
      <div
        key={`${block.type}-${index}`}
        className="chat-prose max-w-none text-kodelet-dark"
        dangerouslySetInnerHTML={{ __html: renderContent([block]) }}
      />
    );
  });
};

const messageCopyButtonBaseClassName =
  'pointer-events-none px-3 py-2 opacity-0 transition-opacity duration-200 focus-visible:pointer-events-auto focus-visible:opacity-100';

const assistantMessageCopyButtonClassName = `${messageCopyButtonBaseClassName} absolute right-0 top-0 z-10 group-hover/message:pointer-events-auto group-hover/message:opacity-100 group-focus-within/message:pointer-events-auto group-focus-within/message:opacity-100`;

interface ChatTranscriptProps {
  messages: ChatRenderMessage[];
  isStreaming: boolean;
  emptyStateTitle?: string;
}

const ChatTranscript: React.FC<ChatTranscriptProps> = ({
  messages,
  isStreaming,
  emptyStateTitle = 'Good morning',
}) => {
  const assistantTurnCount = useMemo(
    () => messages.filter((message) => message.role === 'assistant').length,
    [messages]
  );

  const renderAssistantBlocks = (blocks: ChatAssistantBlock[]): React.ReactNode[] => {
    const renderedBlocks: React.ReactNode[] = [];

    for (let blockIndex = 0; blockIndex < blocks.length; blockIndex += 1) {
      const block = blocks[blockIndex];

      if (block.type === 'thinking') {
        if (!block.inProgress) {
          const thinkingBlocks: Array<Extract<ChatAssistantBlock, { type: 'thinking' }>> = [block];
          let lookaheadIndex = blockIndex + 1;

          while (lookaheadIndex < blocks.length) {
            const nextBlock = blocks[lookaheadIndex];
            if (nextBlock.type !== 'thinking' || nextBlock.inProgress) {
              break;
            }

            thinkingBlocks.push(nextBlock);
            lookaheadIndex += 1;
          }

          renderedBlocks.push(renderCompletedThinkingGroup(thinkingBlocks, `thinking-${blockIndex}`));
          blockIndex = lookaheadIndex - 1;
          continue;
        }

        const hasThinkingContent = extractContentText(block.content).trim().length > 0;
        renderedBlocks.push(
          <div key={`thinking-${blockIndex}`} className="activity-stack activity-stack-thinking">
            <div
              className="activity-card activity-card-thinking activity-card-live"
              role="status"
              aria-live="polite"
            >
              <div className="tool-summary activity-summary activity-summary-static">
                <StreamingMark />
                <span className="tool-summary-text" title="Thinking">
                  <span className="tool-summary-label">Thinking</span>
                </span>
              </div>
              {hasThinkingContent ? (
                <div className="activity-detail-content activity-detail-content-live">
                  {renderThinkingContent(block.content)}
                </div>
              ) : null}
            </div>
          </div>
        );
        continue;
      }

      if (block.type === 'tools') {
        if (block.tools.length === 0) {
          continue;
        }

        renderedBlocks.push(
          <ChatToolActivity key={`tools-${blockIndex}`} tools={block.tools} />
        );
        continue;
      }

      const copyText = getMessageBlockCopyText(block.content);
      renderedBlocks.push(
        <div key={`message-${blockIndex}`} className="group/message relative">
          {copyText.trim() ? (
            <CopyButton
              className={assistantMessageCopyButtonClassName}
              content={copyText}
            />
          ) : null}
          <div
            className="chat-prose max-w-none pr-12 text-kodelet-dark"
            dangerouslySetInnerHTML={{ __html: renderContent(block.content) }}
          />
        </div>
      );
    }

    return renderedBlocks;
  };

  if (messages.length === 0) {
    return (
      <div className="flex min-h-full items-center justify-center px-6 py-12">
        <div className="empty-state-copy-stack text-center">
          <h1 className="empty-state-title">
            {emptyStateTitle}
          </h1>
          <p className="empty-state-copy">
            Ask kodelet to inspect the repo, make changes, run tools, and keep the entire
            conversation threaded in one place.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl space-y-5 px-4 py-6 md:px-8">
      {messages.map((message, index) => {
        const isUser = message.role === 'user';
        const isActiveStreamingAssistant =
          !isUser && isStreaming && index === messages.length - 1;
        const hasVisibleInProgressBlock =
          isActiveStreamingAssistant &&
          (message.blocks || []).some(
            (block) =>
              ((block.type === 'thinking' || block.type === 'message') && block.inProgress) ||
              (block.type === 'tools' && block.tools.some((toolCall) => !toolCall.result))
          );

        return (
          <ChatMessageFrame
            copyText={isUser ? getMessageBlockCopyText(message.content) : undefined}
            key={`${message.role}-${index}`}
            role={message.role}
          >
            {isUser ? (
              <div className="space-y-3">{renderUserContent(message.content)}</div>
            ) : (
              <div className="space-y-4">
                {renderAssistantBlocks(message.blocks || [])}

                {isActiveStreamingAssistant && !hasVisibleInProgressBlock ? (
                  <ChatStreamingIndicator assistantTurnCount={assistantTurnCount} />
                ) : null}
              </div>
            )}
          </ChatMessageFrame>
        );
      })}
    </div>
  );
};

export default ChatTranscript;
