import React, { useEffect, useMemo, useState } from 'react';
import { marked } from 'marked';
import type { ChatRenderMessage, ChatRenderToolCall, ContentBlock, ToolResult } from '../../types';
import ToolRenderer from '../ToolRenderer';
import { CopyButton } from '../tool-renderers/shared';
import { cn, formatDuration } from '../../utils';
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

const getNumberField = (
  source: Record<string, unknown> | null,
  ...keys: string[]
): number | undefined => {
  for (const key of keys) {
    const value = source?.[key];
    if (typeof value === 'number' && Number.isFinite(value)) {
      return value;
    }
  }

  return undefined;
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

const splitActivitySummary = (summaryText: string): { label: string; detail?: string } => {
  const separatorIndex = summaryText.indexOf(': ');

  if (separatorIndex === -1) {
    return { label: summaryText };
  }

  return {
    label: summaryText.slice(0, separatorIndex),
    detail: summaryText.slice(separatorIndex + 2),
  };
};

const ActivitySummaryText: React.FC<{ summaryText: string }> = ({ summaryText }) => {
  const { label, detail } = splitActivitySummary(summaryText);

  return (
    <span className="tool-summary-text" title={summaryText}>
      {detail ? <span className="sr-only">{summaryText}</span> : null}
      <span className="tool-summary-label" aria-hidden={detail ? 'true' : undefined}>
        {detail ? `${label}:` : label}
      </span>
      {detail ? (
        <span className="tool-summary-detail" aria-hidden="true">
          {' '}{detail}
        </span>
      ) : null}
    </span>
  );
};

const getToolActivityStatus = (toolCall: ChatRenderToolCall): string => {
  const normalizedToolName = normalizeToolName(toolCall.name);
  const metadata = getMetadataRecord(toolCall.result);

  if (normalizedToolName === 'bash') {
    const duration = getNumberField(metadata, 'executionTime');
    const durationText = duration !== undefined ? formatDuration(duration) : '';
    if (durationText) {
      return durationText;
    }
  }

  if (!toolCall.result) {
    return 'running';
  }

  if (!toolCall.result.success) {
    return 'failed';
  }

  return 'done';
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

const getMessageBlockCopyText = (content: string | ContentBlock[] | undefined): string =>
  extractContentText(content);

const messageCopyButtonBaseClassName =
  'pointer-events-none px-3 py-2 opacity-0 transition-opacity duration-200 focus-visible:pointer-events-auto focus-visible:opacity-100';

const userMessageCopyButtonClassName = `${messageCopyButtonBaseClassName} group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100`;

const assistantMessageCopyButtonClassName = `${messageCopyButtonBaseClassName} absolute right-0 top-0 z-10 group-hover/message:pointer-events-auto group-hover/message:opacity-100 group-focus-within/message:pointer-events-auto group-focus-within/message:opacity-100`;

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
                    aria-hidden="true"
                    className={cn(
                      'message-avatar',
                      isUser ? 'message-avatar-user' : 'message-avatar-kodelet'
                    )}
                  >
                    {isUser ? (
                      <svg
                        className="h-5 w-5"
                        fill="none"
                        viewBox="0 0 24 24"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="M12 11.4a3.35 3.35 0 1 0 0-6.7 3.35 3.35 0 0 0 0 6.7Z"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="1.8"
                        />
                        <path
                          d="M5.9 19.1c.78-3.02 2.9-4.52 6.1-4.52s5.32 1.5 6.1 4.52"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="1.8"
                        />
                      </svg>
                    ) : (
                      <svg
                        className="h-5 w-5"
                        fill="none"
                        viewBox="0 0 24 24"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="m7.5 7.5 4.5 4.5-4.5 4.5"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="2"
                        />
                        <path
                          d="M13.5 16.5h4.2"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeWidth="2"
                        />
                      </svg>
                    )}
                  </div>
					<div>
						<p className="font-heading text-sm font-semibold tracking-tight text-kodelet-dark">
							{isUser ? 'You' : 'Kodelet'}
						</p>
					</div>
				</div>

                {isUser ? (
                  <CopyButton
                    className={userMessageCopyButtonClassName}
                    content={getMessageBlockCopyText(message.content)}
                  />
                ) : null}
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
                            className="activity-card activity-card-thinking"
                          >
                            <summary className="tool-summary activity-summary" title={thinkingLabel}>
                              <span className="tool-summary-chevron" aria-hidden="true">
                                ›
                              </span>
                              <span className="activity-dot activity-dot-thinking" aria-hidden="true" />
                              <ActivitySummaryText summaryText={thinkingLabel} />
                            </summary>
                            <div className="activity-detail-content">
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
                          className="activity-card activity-card-thinking activity-card-live"
                          role="status"
                          aria-live="polite"
                        >
                          <div className="tool-summary activity-summary activity-summary-static">
                            <span className="activity-dot activity-dot-live" aria-hidden="true" />
                            <ActivitySummaryText summaryText={thinkingLabel} />
                          </div>
                          {hasThinkingContent ? (
                            <div className="activity-detail-content activity-detail-content-live">
                              <div
                                className="chat-prose max-w-none text-kodelet-dark"
                                dangerouslySetInnerHTML={{
                                  __html: renderContent(normalizeThinkingMarkdown(block.content)),
                                }}
                              />
                            </div>
                          ) : null}
                        </div>
                      );
                    }

                    if (block.type === 'tools') {
                      if (block.tools.length === 0) {
                        return null;
                      }

                      return (
                        <div key={`tools-${blockIndex}`} className="activity-stack">
                          {block.tools.map((toolCall, toolIndex) => {
                            const summaryText = getToolSummary(toolCall);
                            const activityStatus = getToolActivityStatus(toolCall);

                            return (
                              <details
                                key={toolCall.callId || `${toolCall.name}-${blockIndex}-${toolIndex}`}
                                className={cn(
                                  'activity-card',
                                  activityStatus === 'running' && 'activity-card-live',
                                  activityStatus === 'failed' && 'activity-card-error'
                                )}
                              >
                                <summary className="tool-summary activity-summary" title={summaryText}>
                                  <span className="tool-summary-chevron" aria-hidden="true">
                                    ›
                                  </span>
                                  <span
                                    className={cn(
                                      'activity-dot',
                                      activityStatus === 'running'
                                        ? 'activity-dot-live'
                                        : activityStatus === 'failed'
                                          ? 'activity-dot-error'
                                          : 'activity-dot-done'
                                    )}
                                    aria-hidden="true"
                                  />
                                  <ActivitySummaryText summaryText={summaryText} />
                                  <span className="tool-summary-status" aria-label={`Tool ${activityStatus}`}>
                                    {activityStatus}
                                  </span>
                                </summary>

                                <div className="activity-detail-content space-y-2">
                                  {toolCall.result ? (
                                    <ToolRenderer
                                      toolInput={toolCall.input}
                                      toolResult={toolCall.result}
                                    />
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

                    const copyText = getMessageBlockCopyText(block.content);

                    return (
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
