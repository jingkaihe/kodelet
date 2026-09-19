import { Check, ChevronRight, SquareSlash } from 'lucide-react';
import Prism from 'prismjs';
import 'prismjs/components/prism-bash';
import 'prismjs/components/prism-go';
import 'prismjs/components/prism-json';
import 'prismjs/components/prism-jsx';
import 'prismjs/components/prism-python';
import 'prismjs/components/prism-typescript';
import 'prismjs/components/prism-tsx';
import 'prismjs/components/prism-yaml';
import React, { useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { ChatAssistantBlock, ChatRenderMessage, ContentBlock } from '../../types';
import { escapeHtml } from '../../utils';
import Spinner from '../Spinner';
import { renderSafeMarkdown } from '../tool-renderers/reference';
import { CopyButton } from '../tool-renderers/shared';
import ChatMessageFrame from './ChatMessageFrame';
import ChatStreamingIndicator from './ChatStreamingIndicator';
import ChatToolActivity from './ChatToolActivity';

// Highlight only changed message HTML, never the entire document during streaming.
Prism.manual = true;

const parseMarkdown = (content: string): string =>
  renderSafeMarkdown(content)
    .replace(
      /<table>/g,
      '<div class="chat-markdown-table-shell">\n<table class="chat-markdown-table">'
    )
    .replace(/<\/table>/g, '</table></div>')
    .replace(/<pre>/g, '<div class="chat-code-block"><pre>')
    .replace(/<\/pre>/g, '</pre></div>');

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

      if (block.type === 'slash-command') {
        return parseMarkdown(block.text || '');
      }

      if (block.type === 'image') {
        const imageUrl =
          block.source?.data && block.source?.media_type
            ? `data:${block.source.media_type};base64,${block.source.data}`
            : block.image_url?.url;
        if (!imageUrl) {
          return '';
        }
        try {
          const protocol = new URL(imageUrl, window.location.href).protocol;
          if (
            !['http:', 'https:', 'file:', 'blob:'].includes(protocol) &&
            !/^data:image\/(?:png|jpeg|gif|webp);base64,[a-z\d+/=\s]+$/i.test(imageUrl)
          ) {
            return '';
          }
        } catch {
          return '';
        }
        const escapedImageUrl = escapeHtml(imageUrl).replace(/"/g, '&quot;');
        return [
          '<figure class="chat-uploaded-image">',
          `<img src="${escapedImageUrl}" alt="Uploaded content" class="chat-uploaded-image-media" loading="lazy" />`,
          '</figure>',
        ].join('');
      }

      return '';
    })
    .join('');
};

interface MarkdownContentProps {
  html: string;
  className: string;
}

const MarkdownContent = React.memo(({ html, className }: MarkdownContentProps) => {
  // Stream events clone messages; keep unchanged HTML stable to preserve text selection.
  const markup = useMemo(() => ({ __html: html }), [html]);
  const contentRef = useRef<HTMLDivElement>(null);
  const [codeBlocks, setCodeBlocks] = useState<Array<{ element: Element; content: string }>>([]);

  // biome-ignore lint/correctness/useExhaustiveDependencies(html): New HTML replaces the DOM nodes that need highlighting and copy portals.
  useLayoutEffect(() => {
    const blocks = Array.from(contentRef.current?.querySelectorAll('.chat-code-block') || []);
    const nextCodeBlocks = blocks.map((element) => {
      const code = element.querySelector<HTMLElement>('pre > code');
      const language = code?.className.match(/\blanguage-([\w-]+)\b/i)?.[1].toLowerCase();
      if (code && language && typeof Prism.languages[language] === 'object') {
        Prism.highlightElement(code);
      }
      return { element, content: code?.textContent || '' };
    });
    setCodeBlocks((current) =>
      current.length || nextCodeBlocks.length ? nextCodeBlocks : current
    );
  }, [html]);

  return (
    <>
      {/* biome-ignore lint/security/noDangerouslySetInnerHtml: Only renderContent supplies HTML: Markdown escapes raw HTML and rejects unsafe URLs; uploaded image URLs are validated and attribute-escaped. */}
      <div ref={contentRef} className={className} dangerouslySetInnerHTML={markup} />
      {codeBlocks.map(({ element, content }, index) =>
        createPortal(
          <CopyButton className="chat-code-copy-button" content={content} label="Copy code" />,
          element,
          String(index)
        )
      )}
    </>
  );
});

const normalizeThinkingMarkdown = (content: string): string =>
  content
    .replace(/([^\n])\n(#{1,6}\s)/g, '$1\n\n$2')
    .replace(/([^\n])\n(\*\*[A-Z][^*\n]+\*\*)/g, '$1\n\n$2')
    .replace(/([.!?])(?=\*\*[A-Z][^*\n]+\*\*)/g, '$1\n\n')
    .replace(/([.!?])(?=#{1,6}\s)/g, '$1\n\n');

const renderThinkingContent = (content: string) => {
  const hasThinkingContent = extractContentText(content).trim().length > 0;

  if (!hasThinkingContent) {
    return <p className="text-sm italic text-kodelet-blue/80">Reasoning complete.</p>;
  }

  return (
    <MarkdownContent
      className="chat-prose max-w-none text-kodelet-dark"
      html={renderContent(normalizeThinkingMarkdown(content))}
    />
  );
};

const renderCompletedThinkingGroup = (
  thinkingBlocks: Array<Extract<ChatAssistantBlock, { type: 'thinking' }>>,
  key: string
) => {
  const summaryText = `Had ${thinkingBlocks.length} ${thinkingBlocks.length === 1 ? 'thought' : 'thoughts'}`;

  return (
    <div key={key} className="activity-stack activity-stack-thinking">
      <details className="activity-card activity-card-thinking">
        <summary className="tool-summary activity-summary" title={summaryText}>
          <span className="activity-marker" aria-hidden="true">
            <Check size={14} />
          </span>
          <span className="tool-summary-text" title={summaryText}>
            <span className="tool-summary-label">{summaryText}</span>
          </span>
          <span className="tool-summary-chevron" aria-hidden="true">
            <ChevronRight size={12} />
          </span>
        </summary>
        <div className="activity-detail-content thinking-group-content">
          {thinkingBlocks.map((thinkingBlock, index) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: Completed thoughts are append-only chronological blocks with no IDs; positional identity preserves text selection across stream clones.
            <section className="thinking-group-item" key={index}>
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

      if (block.type === 'slash-command') {
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
    <SquareSlash
      aria-hidden="true"
      className="slash-command-card-icon"
      size={14}
      strokeWidth={2.2}
    />
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
      <MarkdownContent
        className="chat-prose max-w-none text-kodelet-dark"
        html={renderContent(content)}
      />
    );
  }

  return content.map((block, index) => {
    if (block.type === 'slash-command') {
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
      <MarkdownContent
        key={`${block.type}-${index}`}
        className="chat-prose max-w-none text-kodelet-dark"
        html={renderContent([block])}
      />
    );
  });
};

const messageCopyButtonBaseClassName =
  'pointer-events-none opacity-0 transition-opacity duration-200 focus-visible:pointer-events-auto focus-visible:opacity-100';

const assistantMessageCopyButtonClassName = `${messageCopyButtonBaseClassName} group-hover/message:pointer-events-auto group-hover/message:opacity-100 group-focus-within/message:pointer-events-auto group-focus-within/message:opacity-100`;

interface ChatTranscriptProps {
  messages: ChatRenderMessage[];
  isStreaming: boolean;
}

const ChatTranscript: React.FC<ChatTranscriptProps> = ({ messages, isStreaming }) => {
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

          renderedBlocks.push(
            renderCompletedThinkingGroup(thinkingBlocks, `thinking-${blockIndex}`)
          );
          blockIndex = lookaheadIndex - 1;
          continue;
        }

        const hasThinkingContent = extractContentText(block.content).trim().length > 0;
        renderedBlocks.push(
          <div key={`thinking-${blockIndex}`} className="activity-stack activity-stack-thinking">
            <output
              className="activity-card activity-card-thinking activity-card-live block"
              aria-live="polite"
            >
              <div className="tool-summary activity-summary activity-summary-static">
                <Spinner className="chat-streaming-spinner activity-marker" />
                <span className="tool-summary-text" title="Thinking">
                  <span className="tool-summary-label">Thinking</span>
                </span>
              </div>
              {hasThinkingContent ? (
                <div className="activity-detail-content activity-detail-content-live">
                  {renderThinkingContent(block.content)}
                </div>
              ) : null}
            </output>
          </div>
        );
        continue;
      }

      if (block.type === 'tools') {
        if (block.tools.length === 0) {
          continue;
        }

        renderedBlocks.push(<ChatToolActivity key={`tools-${blockIndex}`} tools={block.tools} />);
        continue;
      }

      const copyText = getMessageBlockCopyText(block.content);
      renderedBlocks.push(
        <div key={`message-${blockIndex}`} className="group/message relative">
          <MarkdownContent
            className="chat-prose max-w-none text-kodelet-dark"
            html={renderContent(block.content)}
          />
          {copyText.trim() ? (
            <div className="chat-message-actions">
              <CopyButton className={assistantMessageCopyButtonClassName} content={copyText} />
            </div>
          ) : null}
        </div>
      );
    }

    return renderedBlocks;
  };

  if (messages.length === 0) {
    return (
      <div className="chat-empty-state">
        <h1 className="chat-empty-state-title">Hello! What would you like me to work on?</h1>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-5xl space-y-4 px-3 py-6 sm:space-y-5 sm:px-4 md:px-8">
      {messages.map((message, index) => {
        const isUser = message.role === 'user';
        const isActiveStreamingAssistant = !isUser && isStreaming && index === messages.length - 1;
        const hasVisibleInProgressBlock =
          isActiveStreamingAssistant &&
          (message.blocks || []).some(
            (block) =>
              ((block.type === 'thinking' || block.type === 'message') && block.inProgress) ||
              (block.type === 'tools' &&
                block.tools.some((toolCall) => toolCall.inProgress || !toolCall.result))
          );

        return (
          <ChatMessageFrame
            copyText={isUser ? getMessageBlockCopyText(message.content) : undefined}
            key={`${message.role}-${index}`}
            messageRole={message.role}
          >
            {isUser ? (
              <div className="space-y-3">{renderUserContent(message.content)}</div>
            ) : (
              <div className="chat-assistant-blocks">
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

// Runner polling must not rewrite unchanged message HTML and clear text selection.
export default React.memo(ChatTranscript);
