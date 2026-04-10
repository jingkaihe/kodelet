import React, { useEffect, useMemo, useState } from 'react';
import { marked } from 'marked';
import type { ChatRenderMessage, ChatRenderToolCall, ContentBlock, ToolResult } from '../../types';
import ToolRenderer from '../ToolRenderer';
import { CopyButton } from '../tool-renderers/shared';
import { cn } from '../../utils';
import { normalizeToolName, ReferenceCodeBlock } from '../tool-renderers/reference';

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

const formatToolInput = (input: string): string => {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
};

const parseToolInput = (input: string): Record<string, unknown> | null => {
  try {
    const parsed = JSON.parse(input);
    return parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : null;
  } catch {
    return null;
  }
};

const getMetadataRecord = (toolResult?: ToolResult): Record<string, unknown> | null => {
  const metadata = toolResult?.metadata;
  return metadata && typeof metadata === 'object' ? (metadata as Record<string, unknown>) : null;
};

const getStringField = (
  source: Record<string, unknown> | null,
  ...keys: string[]
): string | undefined => {
  for (const key of keys) {
    const value = source?.[key];
    if (typeof value === 'string' && value.trim().length > 0) {
      return value.trim();
    }
  }

  return undefined;
};

const getStringArrayField = (
  source: Record<string, unknown> | null,
  ...keys: string[]
): string[] => {
  for (const key of keys) {
    const value = source?.[key];
    if (!Array.isArray(value)) {
      continue;
    }

    const items = value
      .filter((item): item is string => typeof item === 'string')
      .map((item) => item.trim())
      .filter(Boolean);

    if (items.length > 0) {
      return items;
    }
  }

  return [];
};

const collapseWhitespace = (value: string): string => value.replace(/\s+/g, ' ').trim();

const summarizeList = (items: string[]): string | undefined => {
  const values = items.map(collapseWhitespace).filter(Boolean);

  if (values.length === 0) {
    return undefined;
  }

  if (values.length === 1) {
    return values[0];
  }

  return `${values[0]} (+${values.length - 1} more)`;
};

const summarizeApplyPatchInput = (patchInput: string): string | undefined => {
  const operations: string[] = [];

  patchInput.split('\n').forEach((line) => {
    const trimmedLine = line.trim();

    if (trimmedLine.startsWith('*** Add File: ')) {
      operations.push(`add ${trimmedLine.slice('*** Add File: '.length).trim()}`);
      return;
    }

    if (trimmedLine.startsWith('*** Update File: ')) {
      operations.push(`update ${trimmedLine.slice('*** Update File: '.length).trim()}`);
      return;
    }

    if (trimmedLine.startsWith('*** Delete File: ')) {
      operations.push(`delete ${trimmedLine.slice('*** Delete File: '.length).trim()}`);
      return;
    }

    if (trimmedLine.startsWith('*** Move to: ') && operations.length > 0) {
      const previousOperation = operations[operations.length - 1];
      operations[operations.length - 1] = `${previousOperation} → ${trimmedLine.slice('*** Move to: '.length).trim()}`;
    }
  });

  return summarizeList(operations);
};

const summarizeApplyPatchResult = (metadata: Record<string, unknown> | null): string | undefined => {
  const changes = metadata?.changes;
  if (!Array.isArray(changes) || changes.length === 0) {
    return summarizeList([
      ...getStringArrayField(metadata, 'added').map((path) => `add ${path}`),
      ...getStringArrayField(metadata, 'modified').map((path) => `update ${path}`),
      ...getStringArrayField(metadata, 'deleted').map((path) => `delete ${path}`),
    ]);
  }

  const operations = changes
    .map((change) => {
      if (!change || typeof change !== 'object') {
        return undefined;
      }

      const changeRecord = change as Record<string, unknown>;
      const operation = getStringField(changeRecord, 'operation') || 'update';
      const path = getStringField(changeRecord, 'path');
      const movePath = getStringField(changeRecord, 'movePath');

      if (!path) {
        return undefined;
      }

      return movePath ? `${operation} ${path} → ${movePath}` : `${operation} ${path}`;
    })
    .filter((value): value is string => Boolean(value));

  return summarizeList(operations);
};

const formatToolSummary = (label: string, value?: string): string => {
  if (!value) {
    return label;
  }

  return `${label}: ${collapseWhitespace(value)}`;
};

const getFallbackToolLabel = (toolName: string): string =>
  normalizeToolName(toolName)
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (match) => match.toUpperCase());

const getToolSummary = (toolCall: ChatRenderToolCall): string => {
  const normalizedToolName = normalizeToolName(toolCall.name);
  const input = parseToolInput(toolCall.input);
  const metadata = getMetadataRecord(toolCall.result);

  switch (normalizedToolName) {
    case 'bash':
      return formatToolSummary(
        'Bash',
        getStringField(input, 'command') || getStringField(metadata, 'command')
      );

    case 'file_read':
      return formatToolSummary(
        'Read file',
        getStringField(input, 'file_path') || getStringField(metadata, 'filePath')
      );

    case 'file_write':
      return formatToolSummary(
        'Write file',
        getStringField(input, 'file_path') || getStringField(metadata, 'filePath')
      );

    case 'file_edit':
      return formatToolSummary(
        'Edit file',
        getStringField(input, 'file_path') || getStringField(metadata, 'filePath')
      );

    case 'apply_patch':
      return formatToolSummary(
        'Apply patch',
        summarizeApplyPatchResult(metadata) ||
          summarizeApplyPatchInput(getStringField(input, 'input') || '')
      );

    case 'grep_tool': {
      const pattern = getStringField(input, 'pattern') || getStringField(metadata, 'pattern');
      const path = getStringField(input, 'path') || getStringField(metadata, 'path');
      return formatToolSummary('Search', pattern && path ? `${pattern} in ${path}` : pattern || path);
    }

    case 'glob_tool': {
      const pattern = getStringField(input, 'pattern') || getStringField(metadata, 'pattern');
      const path = getStringField(input, 'path') || getStringField(metadata, 'path');
      return formatToolSummary(
        'Find files',
        pattern && path ? `${pattern} in ${path}` : pattern || path
      );
    }

    case 'web_fetch':
      return formatToolSummary(
        'Fetch URL',
        getStringField(input, 'url') || getStringField(metadata, 'url')
      );

    case 'view_image':
      return formatToolSummary(
        'View image',
        getStringField(input, 'path') || getStringField(metadata, 'path')
      );

    case 'openai_web_search': {
      const queries = [
        ...getStringArrayField(input, 'queries'),
        ...getStringArrayField(metadata, 'queries'),
      ];
      const query = queries[0];
      const location =
        getStringField(metadata, 'url', 'pattern') || getStringField(input, 'url', 'pattern');
      return formatToolSummary('Web search', query || location);
    }

    case 'subagent':
      return formatToolSummary(
        'Subagent',
        getStringField(input, 'workflow') ||
          getStringField(metadata, 'workflow') ||
          getStringField(input, 'question') ||
          getStringField(metadata, 'question')
      );

    case 'skill':
      return formatToolSummary(
        'Skill',
        getStringField(input, 'skill_name') || getStringField(metadata, 'skillName')
      );

    default:
      return formatToolSummary(
        getFallbackToolLabel(normalizedToolName),
        getStringField(input, 'path', 'file_path', 'url', 'command', 'pattern') ||
          getStringField(metadata, 'path', 'filePath', 'url', 'command', 'pattern')
      );
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
        return block.tools.map((toolCall) => `- ${getToolSummary(toolCall)}`).join('\n');
      }

      return '';
    })
    .filter(Boolean)
    .join('\n\n');
};

const STREAMING_INDICATOR_MESSAGES = [
  'Following the thread…',
  'Gathering the next clue…',
  'Composing the next move…',
  'Tracing the shape of the answer…',
  'Pulling the pieces together…',
  'Working through the details…',
];

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
  const [streamingMessageOffset, setStreamingMessageOffset] = useState(0);

  const assistantTurnCount = useMemo(
    () => messages.filter((message) => message.role === 'assistant').length,
    [messages]
  );

  useEffect(() => {
    setStreamingMessageOffset(0);
  }, [assistantTurnCount]);

  useEffect(() => {
    if (!isStreaming) {
      return undefined;
    }

    const intervalId = window.setInterval(() => {
      setStreamingMessageOffset((currentValue) => currentValue + 1);
    }, 2200);

    return () => {
      window.clearInterval(intervalId);
    };
  }, [isStreaming]);

  const streamingIndicatorMessage =
    STREAMING_INDICATOR_MESSAGES[
      (Math.max(assistantTurnCount - 1, 0) + streamingMessageOffset) %
        STREAMING_INDICATOR_MESSAGES.length
    ];

  if (messages.length === 0) {
    return (
      <div className="flex min-h-full items-center justify-center px-6 py-12">
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
        const isActiveStreamingAssistant =
          !isUser && isStreaming && index === messages.length - 1;
        const hasVisibleInProgressBlock =
          isActiveStreamingAssistant &&
          (message.blocks || []).some(
            (block) =>
              (block.type === 'thinking' || block.type === 'message') && block.inProgress
          );

        return (
          <article key={`${message.role}-${index}`} className="w-full">
            <div
              className={cn(
                'chat-message-panel group w-full rounded-[1.5rem]',
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
					</div>
				</div>

                <CopyButton
                  className="pointer-events-none px-3 py-2 opacity-0 transition-opacity duration-200 group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100 focus-visible:pointer-events-auto focus-visible:opacity-100"
                  content={getCopyText(message)}
                />
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
                      const thinkingLabel =
                        isActiveStreamingAssistant && block.inProgress
                          ? streamingIndicatorMessage
                          : 'Thinking';
                      const hasThinkingContent =
                        extractContentText(block.content).trim().length > 0;

                      if (!block.inProgress) {
                        return (
                          <details
                            key={`thinking-${blockIndex}`}
                            className="chat-subpanel overflow-hidden rounded-2xl"
                          >
                            <summary className="cursor-pointer list-none px-4 py-3 font-heading text-sm font-semibold text-kodelet-blue">
                              {thinkingLabel}
                            </summary>
                            <div className="border-t border-black/8 px-4 py-4">
                              {hasThinkingContent ? (
                                <div
                                  className="chat-prose max-w-none text-kodelet-dark"
                                  dangerouslySetInnerHTML={{
                                    __html: renderContent(normalizeThinkingMarkdown(block.content)),
                                  }}
                                />
                              ) : (
                                <p className="text-sm italic text-kodelet-blue/80">
                                  Reasoning complete.
                                </p>
                              )}
                            </div>
                          </details>
                        );
                      }

                      return (
                        <div
                          key={`thinking-${blockIndex}`}
                          className="chat-subpanel rounded-2xl px-4 py-4"
                        >
                          <div className="mb-3 flex items-center gap-2">
                            <span
                              className={cn(
                                'font-heading text-sm font-semibold text-kodelet-blue',
                                isActiveStreamingAssistant && block.inProgress && 'animate-pulse'
                              )}
                            >
                              {thinkingLabel}
                            </span>
                          </div>
                          {hasThinkingContent ? (
                            <div
                              className="chat-prose max-w-none text-kodelet-dark"
                              dangerouslySetInnerHTML={{
                                __html: renderContent(normalizeThinkingMarkdown(block.content)),
                              }}
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
                      if (block.tools.length === 0) {
                        return null;
                      }

                      return (
                        <div key={`tools-${blockIndex}`} className="space-y-3">
                          {block.tools.map((toolCall, toolIndex) => {
                            const summaryText = getToolSummary(toolCall);

                            return (
                              <details
                                key={toolCall.callId || `${toolCall.name}-${blockIndex}-${toolIndex}`}
                                className="chat-subpanel overflow-hidden rounded-2xl"
                              >
                                <summary className="tool-summary" title={summaryText}>
                                  <span className="tool-summary-chevron" aria-hidden="true">
                                    ›
                                  </span>
                                  <span className="tool-summary-text">{summaryText}</span>
                                </summary>

                                <div className="space-y-2 border-t border-black/8 px-4 py-4">
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
                              </details>
                            );
                          })}
                        </div>
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

                  {isActiveStreamingAssistant && !hasVisibleInProgressBlock ? (
                    <div className="chat-streaming-indicator" aria-label="Kodelet is working">
                      <span className="chat-streaming-dot" aria-hidden="true" />
                      <span className="chat-streaming-label">{streamingIndicatorMessage}</span>
                    </div>
                  ) : null}
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
