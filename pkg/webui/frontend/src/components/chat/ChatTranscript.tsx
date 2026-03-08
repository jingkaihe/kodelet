import React from 'react';
import { marked } from 'marked';
import type { ChatRenderMessage, ContentBlock } from '../../types';
import ToolRenderer from '../ToolRenderer';
import { cn, copyToClipboard } from '../../utils';
import { normalizeToolName, ReferenceCodeBlock, TOOL_LABELS } from '../tool-renderers/reference';

const renderContent = (content: string | ContentBlock[] | undefined): string => {
  if (!content) {
    return '';
  }

  if (typeof content === 'string') {
    return marked.parse(content) as string;
  }

  return content
    .map((block) => {
      if (block.type === 'text') {
        return marked.parse(block.text || '') as string;
      }

      if (block.type === 'image') {
        const imageUrl = block.source?.data || block.image_url?.url;
        if (!imageUrl) {
          return '';
        }
        return `<img src="${imageUrl}" alt="Uploaded content" class="max-w-full rounded-xl border border-black/10" />`;
      }

      return '';
    })
    .join('');
};

const formatToolInput = (input: string): string => {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
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

      if (block.type === 'image') {
        return '[image]';
      }

      return '';
    })
    .filter(Boolean)
    .join('\n\n');
};

const getCopyText = (message: ChatRenderMessage): string => {
  if (message.role === 'user') {
    return extractContentText(message.content);
  }

  return (message.blocks || [])
    .map((block) => {
      if (block.type === 'thinking') {
        return `Thinking\n${extractContentText(block.content)}`.trim();
      }

      if (block.type === 'message') {
        return extractContentText(block.content);
      }

      if (block.type === 'tools') {
        return `Tools (${block.tools.length})`;
      }

      return '';
    })
    .filter(Boolean)
    .join('\n\n');
};

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
  if (messages.length === 0) {
    return (
      <div className="flex min-h-[50vh] items-center justify-center px-6 py-12">
        <div className="max-w-2xl text-center">
          <p className="eyebrow-label mb-3 text-kodelet-orange">
            Kodelet Chat
          </p>
          <h1 className="mb-4 text-4xl font-heading font-bold tracking-tight text-kodelet-dark md:text-6xl">
            {emptyStateTitle}
          </h1>
          <p className="mx-auto max-w-xl text-lg font-body italic leading-8 text-kodelet-dark/70">
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

        return (
          <article key={`${message.role}-${index}`} className="w-full">
            <div
              className={cn(
                'chat-message-panel w-full rounded-[1.5rem]',
                isUser ? 'px-5 py-4' : 'px-5 py-5'
              )}
            >
              <div className="mb-4 flex items-center justify-between gap-3">
                <div className="flex items-center gap-3">
                  <div
                    className={cn(
                      'flex h-10 w-10 items-center justify-center rounded-full font-heading text-sm font-semibold uppercase',
                      isUser
                        ? 'bg-kodelet-orange text-white'
                        : 'bg-kodelet-dark text-kodelet-light'
                    )}
                  >
                    {isUser ? 'You' : 'Ko'}
                  </div>
                  <div>
                    <p className="font-heading text-sm font-semibold tracking-tight text-kodelet-dark">
                      {isUser ? 'You' : 'Kodelet'}
                    </p>
                    <p className="eyebrow-label text-kodelet-mid-gray">
                      {isUser ? 'Prompt' : isStreaming && index === messages.length - 1 ? 'Streaming' : 'Reply'}
                    </p>
                  </div>
                </div>

                <button
                  className="panel-action-button px-3 py-2"
                  onClick={() => copyToClipboard(getCopyText(message))}
                  type="button"
                >
                  Copy
                </button>
              </div>

              {isUser ? (
                <div
                  className="chat-prose max-w-none text-kodelet-dark"
                  dangerouslySetInnerHTML={{ __html: renderContent(message.content) }}
                />
              ) : (
                <div className="space-y-4">
                  {(message.blocks || []).map((block, blockIndex) => {
                    if (block.type === 'thinking') {
                      return (
                        <div
                          key={`thinking-${blockIndex}`}
                          className="chat-subpanel rounded-2xl px-4 py-4"
                        >
                          <div className="mb-3 flex items-center gap-2">
                            <span className="font-heading text-sm font-semibold text-kodelet-blue">
                              Thinking{block.inProgress ? '…' : ''}
                            </span>
                          </div>
                          {extractContentText(block.content).trim() ? (
                            <div
                              className="chat-prose max-w-none text-kodelet-dark"
                              dangerouslySetInnerHTML={{ __html: renderContent(block.content) }}
                            />
                          ) : (
                            <p className="text-sm italic text-kodelet-blue/80">
                              Reasoning in progress…
                            </p>
                          )}
                        </div>
                      );
                    }

                    if (block.type === 'tools') {
                      return (
                        <details
                          key={`tools-${blockIndex}`}
                          className="chat-subpanel overflow-hidden rounded-2xl"
                        >
                          <summary className="cursor-pointer list-none px-4 py-3 font-heading text-sm font-semibold text-kodelet-dark">
                            Tools ({block.tools.length})
                          </summary>
                          <div className="space-y-4 border-t border-black/8 px-4 py-4">
                            {block.tools.map((toolCall, toolIndex) => {
                              const normalizedToolName = normalizeToolName(toolCall.name);

                              return (
                                <div
                                  key={toolCall.callId || `${toolCall.name}-${blockIndex}`}
                                  className="space-y-2"
                                >
                                  <p className="tool-item-label">
                                    {toolIndex + 1}. {TOOL_LABELS[normalizedToolName] || normalizedToolName}
                                  </p>

                                  {toolCall.result ? (
                                    <ToolRenderer toolResult={toolCall.result} />
                                  ) : (
                                    <>
                                      <p className="tool-awaiting">Awaiting tool result…</p>
                                      {toolCall.input ? (
                                        <ReferenceCodeBlock
                                          content={formatToolInput(toolCall.input)}
                                          language="json"
                                        />
                                      ) : null}
                                    </>
                                  )}
                                </div>
                              );
                            })}
                          </div>
                        </details>
                      );
                    }

                    return (
                      <div
                        key={`message-${blockIndex}`}
                        className="chat-prose max-w-none text-kodelet-dark"
                        dangerouslySetInnerHTML={{ __html: renderContent(block.content) }}
                      />
                    );
                  })}
                </div>
              )}
            </div>
          </article>
        );
      })}
    </div>
  );
};

export default ChatTranscript;
