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

interface ChatTranscriptProps {
  messages: ChatRenderMessage[];
  isStreaming: boolean;
}

const ChatTranscript: React.FC<ChatTranscriptProps> = ({ messages, isStreaming }) => {
  if (messages.length === 0) {
    return (
      <div className="flex min-h-[50vh] items-center justify-center px-6 py-12">
        <div className="max-w-2xl text-center">
          <p className="mb-3 text-xs font-heading uppercase tracking-[0.24em] text-kodelet-orange">
            Kodelet Chat
          </p>
          <h1 className="mb-4 text-4xl font-heading font-bold tracking-tight text-kodelet-dark md:text-6xl">
            Ship the next idea.
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
    <div className="space-y-6 px-4 py-6 md:px-8">
      {messages.map((message, index) => {
        const isUser = message.role === 'user';

        return (
          <article
            key={`${message.role}-${index}`}
            className={cn(
              'flex w-full',
              isUser ? 'justify-end' : 'justify-start'
            )}
          >
            <div
              className={cn(
                'w-full max-w-4xl rounded-[1.5rem] border shadow-[0_20px_60px_rgba(20,20,19,0.07)]',
                isUser
                  ? 'border-kodelet-orange/20 bg-white/90 px-5 py-4 md:max-w-2xl'
                  : 'border-black/8 bg-white/88 px-5 py-5 backdrop-blur'
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
                    <p className="text-xs uppercase tracking-[0.18em] text-kodelet-mid-gray">
                      {isUser ? 'Prompt' : isStreaming && index === messages.length - 1 ? 'Streaming' : 'Reply'}
                    </p>
                  </div>
                </div>

                <button
                  className="rounded-full border border-black/10 px-3 py-1 text-xs font-heading font-medium text-kodelet-dark transition hover:border-kodelet-orange/30 hover:text-kodelet-orange"
                  onClick={() =>
                    copyToClipboard(
                      typeof message.content === 'string'
                        ? message.content
                        : JSON.stringify(message.blocks || [], null, 2)
                    )
                  }
                  type="button"
                >
                  Copy
                </button>
              </div>

              {isUser ? (
                <div
                  className="prose prose-sm max-w-none text-kodelet-dark"
                  dangerouslySetInnerHTML={{ __html: renderContent(message.content) }}
                />
              ) : (
                <div className="space-y-4">
                  {(message.blocks || []).map((block, blockIndex) => {
                    if (block.type === 'thinking') {
                      return (
                        <details
                          key={`thinking-${blockIndex}`}
                          className="overflow-hidden rounded-2xl border border-kodelet-blue/18 bg-kodelet-blue/5"
                          open={block.inProgress}
                        >
                          <summary className="cursor-pointer list-none px-4 py-3 font-heading text-sm font-semibold text-kodelet-blue">
                            Thinking{block.inProgress ? '…' : ''}
                          </summary>
                          <div className="border-t border-kodelet-blue/12 px-4 py-4">
                            <div
                              className="prose prose-sm max-w-none text-kodelet-dark"
                              dangerouslySetInnerHTML={{ __html: renderContent(block.content) }}
                            />
                          </div>
                        </details>
                      );
                    }

                    if (block.type === 'tools') {
                      return (
                        <details
                          key={`tools-${blockIndex}`}
                          className="overflow-hidden rounded-2xl border border-black/10 bg-kodelet-light-gray/22"
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
                        className="prose prose-sm max-w-none text-kodelet-dark"
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
