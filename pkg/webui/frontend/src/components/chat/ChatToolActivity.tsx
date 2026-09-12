import { Check, ChevronRight, X } from 'lucide-react';
import React from 'react';
import type { ApplyPatchChange, ChatRenderToolCall, ToolResult } from '../../types';
import { cn, formatDuration } from '../../utils';
import Spinner from '../Spinner';
import ToolRenderer from '../ToolRenderer';
import { getFileChangeSummary } from '../tool-renderers/ApplyPatchRenderer';
import {
  getExtensionToolPresentation,
  normalizeToolName,
  ReferenceCodeBlock,
  ReferenceDiffBlock,
} from '../tool-renderers/reference';
import { formatTaskRunElapsed, getTaskRunSnapshot } from '../tool-renderers/TaskRunRenderer';
import ToolImageAttachments, { imageAttachmentURL } from '../tool-renderers/ToolImageAttachments';

interface ChatToolActivityProps {
  tools: ChatRenderToolCall[];
}

const formatToolInput = (input: string): string => {
  try {
    return JSON.stringify(JSON.parse(input), null, 2);
  } catch {
    return input;
  }
};

const TOOL_INPUT_PREVIEW_LIMIT = 320;

export const formatToolInputPreview = (input: string): string => {
  const formattedInput = formatToolInput(input);
  if (formattedInput.length <= TOOL_INPUT_PREVIEW_LIMIT) {
    return formattedInput;
  }

  return `${formattedInput.slice(0, TOOL_INPUT_PREVIEW_LIMIT).trimEnd()}\n… (${formattedInput.length - TOOL_INPUT_PREVIEW_LIMIT} more characters)`;
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

const parseApplyPatchInput = (patchInput: string): ApplyPatchChange[] => {
  const changes: ApplyPatchChange[] = [];

  patchInput.split('\n').forEach((line) => {
    const trimmedLine = line.trim();

    if (trimmedLine.startsWith('*** Add File: ')) {
      changes.push({ operation: 'add', path: trimmedLine.slice('*** Add File: '.length).trim() });
      return;
    }

    if (trimmedLine.startsWith('*** Update File: ')) {
      changes.push({
        operation: 'update',
        path: trimmedLine.slice('*** Update File: '.length).trim(),
      });
      return;
    }

    if (trimmedLine.startsWith('*** Delete File: ')) {
      changes.push({
        operation: 'delete',
        path: trimmedLine.slice('*** Delete File: '.length).trim(),
      });
      return;
    }

    if (trimmedLine.startsWith('*** Move to: ') && changes.length > 0) {
      changes[changes.length - 1].movePath = trimmedLine.slice('*** Move to: '.length).trim();
    }
  });

  return changes;
};

const getApplyPatchChanges = (metadata: Record<string, unknown> | null): ApplyPatchChange[] => {
  const changes = metadata?.changes;
  if (!Array.isArray(changes) || changes.length === 0) {
    return [
      ...getStringArrayField(metadata, 'added').map((path) => ({ operation: 'add', path })),
      ...getStringArrayField(metadata, 'modified').map((path) => ({ operation: 'update', path })),
      ...getStringArrayField(metadata, 'deleted').map((path) => ({ operation: 'delete', path })),
    ];
  }

  return changes.filter((change): change is ApplyPatchChange =>
    Boolean(change && typeof change === 'object' && typeof change.path === 'string')
  );
};

const summarizePatchChanges = (changes: ApplyPatchChange[]): string | undefined =>
  summarizeList(
    changes.map(
      (change) =>
        `${change.operation || 'update'} ${change.path}${change.movePath ? ` → ${change.movePath}` : ''}`
    )
  );

const formatToolSummary = (label: string, value?: string): string => {
  if (!value) {
    return label;
  }

  return `${label}: ${collapseWhitespace(value)}`;
};

const getOpenAIWebSearchSummary = (
  input: Record<string, unknown> | null,
  metadata: Record<string, unknown> | null
): string => {
  const actionType = getStringField(input, 'type', 'action') || getStringField(metadata, 'action');
  const queries = [
    ...getStringArrayField(input, 'queries'),
    ...getStringArrayField(metadata, 'queries'),
  ];
  const query =
    queries[0] ||
    getStringField(input, 'query', 'content') ||
    getStringField(metadata, 'query', 'content');
  const url = getStringField(input, 'url', 'URL') || getStringField(metadata, 'url', 'URL');
  const pattern = getStringField(input, 'pattern') || getStringField(metadata, 'pattern');

  if (actionType === 'open_page') {
    return formatToolSummary('Open page', url || query || pattern || 'URL unavailable');
  }

  if (actionType === 'find_in_page') {
    return formatToolSummary(
      'Find in page',
      pattern && url ? `${pattern} in ${url}` : pattern || url || query || 'target unavailable'
    );
  }

  return formatToolSummary('Web search', query || url || pattern);
};

const getFallbackToolLabel = (toolName: string): string =>
  normalizeToolName(toolName)
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (match) => match.toUpperCase());

export const getToolSummary = (toolCall: ChatRenderToolCall): string => {
  const normalizedToolName = normalizeToolName(toolCall.name);
  const input = parseToolInput(toolCall.input);
  const metadata = getMetadataRecord(toolCall.result);
  const presentation = getExtensionToolPresentation(toolCall.result);
  if (presentation) {
    return presentation.summary;
  }
  const images = toolCall.result?.attachments?.filter(
    (attachment) => attachment.type === 'image' && imageAttachmentURL(attachment)
  );
  if (!toolCall.inProgress && images?.length) {
    return `${normalizedToolName === 'view_image' ? 'Viewed' : 'Generated'} ${images.length === 1 ? 'image' : 'images'}`;
  }
  const taskRun = getTaskRunSnapshot(toolCall.result);
  if (taskRun) {
    return taskRun.title;
  }

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
        summarizePatchChanges(getApplyPatchChanges(metadata)) ||
          summarizePatchChanges(parseApplyPatchInput(getStringField(input, 'input') || ''))
      );

    case 'grep_tool': {
      const pattern = getStringField(input, 'pattern') || getStringField(metadata, 'pattern');
      const path = getStringField(input, 'path') || getStringField(metadata, 'path');
      return formatToolSummary(
        'Search',
        pattern && path ? `${pattern} in ${path}` : pattern || path
      );
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

    case 'openai_web_search':
      return getOpenAIWebSearchSummary(input, metadata);

    case 'read_conversation':
      return formatToolSummary(
        'Read conversation',
        getStringField(input, 'conversation_id', 'conversationID', 'conversationId') ||
          getStringField(metadata, 'conversationID', 'conversationId') ||
          getStringField(input, 'goal') ||
          getStringField(metadata, 'goal')
      );

    case 'extension_tool':
      return formatToolSummary(
        'Extension tool',
        getStringField(metadata, 'toolName') || toolCall.name
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

export const getToolActivityStatus = (toolCall: ChatRenderToolCall): string => {
  const normalizedToolName = normalizeToolName(toolCall.name);
  const metadata = getMetadataRecord(toolCall.result);

  if (toolCall.inProgress || !toolCall.result) {
    return 'running';
  }

  if (!toolCall.result.success) {
    return 'failed';
  }

  const taskRun = getTaskRunSnapshot(toolCall.result);
  if (taskRun) {
    return formatTaskRunElapsed(taskRun.elapsedMs) || 'done';
  }

  if (normalizedToolName === 'bash') {
    const duration = getNumberField(metadata, 'executionTime');
    const durationText = duration !== undefined ? formatDuration(duration) : '';
    if (durationText) {
      return durationText;
    }
  }

  return 'done';
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

const ActivitySummaryText: React.FC<{
  summaryText: string;
}> = ({ summaryText }) => {
  const { label, detail } = splitActivitySummary(summaryText);

  return (
    <span className="tool-summary-text" title={summaryText}>
      {detail ? <span className="sr-only">{summaryText}</span> : null}
      <span
        className={cn('tool-summary-label', detail && 'tool-summary-label-prefix')}
        aria-hidden={detail ? 'true' : undefined}
      >
        {detail ? `${label}:` : label}
      </span>
      {detail ? (
        <span className="tool-summary-detail" aria-hidden="true">
          {' '}
          {detail}
        </span>
      ) : null}
    </span>
  );
};

const FileToolActivity: React.FC<{ tool: ChatRenderToolCall }> = ({ tool }) => {
  const name = normalizeToolName(tool.name);
  const input = parseToolInput(tool.input);
  const metadata = getMetadataRecord(tool.result);
  const status = getToolActivityStatus(tool);
  const running = status === 'running';
  const failed = status === 'failed';
  let changes: ApplyPatchChange[];
  if (name === 'apply_patch') {
    changes = getApplyPatchChanges(metadata);
    if (changes.length === 0 && (!tool.result?.success || !Array.isArray(metadata?.changes))) {
      changes = parseApplyPatchInput(getStringField(input, 'input') || tool.input);
    }
  } else {
    const path = getStringField(metadata, 'filePath') || getStringField(input, 'file_path');
    changes = path
      ? [
          {
            path,
            operation: name === 'file_read' ? 'read' : name === 'file_write' ? 'write' : 'update',
            unifiedDiff:
              typeof metadata?.unifiedDiff === 'string' ? metadata.unifiedDiff : undefined,
          },
        ]
      : [];
  }

  return (
    <>
      {(changes.length > 0 ? changes : [undefined]).map((change, index) => {
        const file = change ? getFileChangeSummary(change) : undefined;
        const summaryText = file
          ? formatToolSummary(`${file.label} file`, file.path)
          : getToolSummary(tool);
        const showCounts = name !== 'file_read' && change?.unifiedDiff !== undefined;
        return (
          <details
            className={cn(
              'activity-card',
              'activity-file',
              running && 'activity-card-live',
              failed && 'activity-card-error'
            )}
            key={`${change?.path || ''}-${index}-${running ? 'running' : failed ? 'failed' : 'settled'}`}
            open={running ? true : undefined}
          >
            <summary className="tool-summary activity-summary" title={summaryText}>
              <span className="activity-marker" aria-hidden="true">
                {running ? <Spinner /> : failed ? <X size={14} /> : <Check size={14} />}
              </span>
              <ActivitySummaryText summaryText={summaryText} />
              {showCounts && file ? (
                <span className="file-activity-counts">
                  (<span className="apply-patch-count-added">+{file.counts.added}</span>{' '}
                  <span className="apply-patch-count-removed">-{file.counts.removed}</span>)
                </span>
              ) : null}
              <span className="tool-summary-chevron" aria-hidden="true">
                <ChevronRight size={12} />
              </span>
              <output
                className={cn('tool-summary-status', !failed && 'sr-only')}
                aria-label={`Tool ${status}`}
              >
                {status}
              </output>
            </summary>
            <div className="activity-detail-content">
              {file && name !== 'file_read' && tool.result ? (
                <>
                  {failed ? (
                    <div className="apply-patch-error" role="alert">
                      {tool.result.error || 'File operation failed.'}
                    </div>
                  ) : null}
                  {file.lines.length > 0 ? (
                    <ReferenceDiffBlock lines={file.lines} />
                  ) : (
                    <p className="tool-awaiting">No file diff available.</p>
                  )}
                </>
              ) : tool.result ? (
                <ToolRenderer
                  isPartial={tool.inProgress}
                  showAttachments={false}
                  toolInput={tool.input}
                  toolResult={tool.result}
                />
              ) : (
                <p className="tool-awaiting">Awaiting file result…</p>
              )}
            </div>
          </details>
        );
      })}
      {tool.result && !tool.inProgress ? <ToolImageAttachments toolResult={tool.result} /> : null}
    </>
  );
};

const builtinToolNames = new Set([
  'get_goal',
  'glob_tool',
  'grep_tool',
  'openai_web_search',
  'read_conversation',
  'skill',
  'todo_read',
  'todo_write',
  'update_goal',
  'view_image',
  'web_fetch',
]);

const toolGroupKind = (tool: ChatRenderToolCall): 'commands' | 'tools' | 'file' | 'extension' => {
  if (tool.result?.metadataType === 'extension_tool' || getExtensionToolPresentation(tool.result)) {
    return 'extension';
  }
  const name = normalizeToolName(tool.name);
  if (name === 'bash') return 'commands';
  if (['apply_patch', 'file_edit', 'file_read', 'file_write'].includes(name)) return 'file';
  return builtinToolNames.has(name) ? 'tools' : 'extension';
};

const ChatToolActivity: React.FC<ChatToolActivityProps> = ({ tools }) => {
  if (tools.length === 0) {
    return null;
  }

  // Preserve transcript order and keep files and extension-owned presentations independent.
  const groups: ChatRenderToolCall[][] = [];
  for (const tool of tools) {
    const previous = groups[groups.length - 1];
    const kind = toolGroupKind(tool);
    if (
      (kind === 'commands' || kind === 'tools') &&
      previous &&
      toolGroupKind(previous[0]) === kind
    ) {
      previous.push(tool);
    } else {
      groups.push([tool]);
    }
  }

  return (
    <div className="activity-stack">
      {groups.map((group, groupIndex) => {
        const toolCall = group[0];
        const kind = toolGroupKind(toolCall);
        if (kind === 'file') {
          return (
            <FileToolActivity
              key={toolCall.callId || `${toolCall.name}-${groupIndex}`}
              tool={toolCall}
            />
          );
        }
        const commands = kind === 'commands';
        const builtin = kind !== 'extension';
        const running = group.some((tool) => getToolActivityStatus(tool) === 'running');
        const failedCount = group.filter((tool) => getToolActivityStatus(tool) === 'failed').length;
        const noun = commands ? 'command' : 'tool';
        const summaryText = builtin
          ? `${running ? 'Running' : 'Ran'} ${group.length} ${noun}${group.length === 1 ? '' : 's'}`
          : getToolSummary(toolCall);
        const activityStatus = running
          ? 'running'
          : failedCount
            ? 'failed'
            : getToolActivityStatus(toolCall);

        return (
          <React.Fragment
            key={`${toolCall.callId || `${toolCall.name}-${groupIndex}`}-${running ? 'running' : failedCount ? 'failed' : 'settled'}`}
          >
            <details
              className={cn(
                'activity-card',
                commands && 'activity-command-group',
                kind === 'tools' && 'activity-tool-group',
                running && 'activity-card-live',
                failedCount > 0 && 'activity-card-error'
              )}
              open={running ? true : undefined}
            >
              <summary className="tool-summary activity-summary" title={summaryText}>
                <span className="activity-marker" aria-hidden="true">
                  {running ? <Spinner /> : failedCount ? <X size={14} /> : <Check size={14} />}
                </span>
                <ActivitySummaryText summaryText={summaryText} />
                <span className="tool-summary-chevron" aria-hidden="true">
                  <ChevronRight size={12} />
                </span>
                {builtin ? (
                  failedCount > 0 ? (
                    <span className="tool-summary-status">{failedCount} failed</span>
                  ) : null
                ) : (
                  <output className="tool-summary-status" aria-label={`Tool ${activityStatus}`}>
                    {activityStatus}
                  </output>
                )}
              </summary>

              <div className="activity-detail-content space-y-2">
                {group.map((tool, toolIndex) => {
                  const status = getToolActivityStatus(tool);
                  const input = parseToolInput(tool.input);
                  const commandText =
                    getStringField(input, 'command') ||
                    getStringField(getMetadataRecord(tool.result), 'command');
                  return (
                    <section
                      className={commands ? 'command-activity' : 'tool-activity'}
                      key={tool.callId || toolIndex}
                    >
                      {builtin ? (
                        <div className="command-activity-header">
                          {commands ? (
                            <code className="command-activity-command">
                              $ {commandText || 'Receiving command…'}
                            </code>
                          ) : (
                            <ActivitySummaryText summaryText={getToolSummary(tool)} />
                          )}
                          <output className="tool-summary-status" aria-label={`Tool ${status}`}>
                            {status}
                          </output>
                        </div>
                      ) : null}
                      {tool.result ? (
                        <ToolRenderer
                          isPartial={tool.inProgress}
                          showAttachments={false}
                          toolInput={tool.input}
                          toolResult={tool.result}
                        />
                      ) : (
                        <>
                          <p className="tool-awaiting">
                            {commands ? 'Waiting for command output…' : 'Awaiting tool result…'}
                          </p>
                          {!commands && tool.input ? (
                            <div className="running-tool-input-preview">
                              <ReferenceCodeBlock
                                content={formatToolInputPreview(
                                  normalizeToolName(tool.name) === 'apply_patch'
                                    ? getStringField(input, 'input') || tool.input
                                    : tool.input
                                )}
                                language={
                                  normalizeToolName(tool.name) === 'apply_patch' ? 'diff' : 'json'
                                }
                              />
                            </div>
                          ) : null}
                        </>
                      )}
                    </section>
                  );
                })}
              </div>
            </details>
            {group.map((tool, toolIndex) =>
              tool.result && !tool.inProgress ? (
                <ToolImageAttachments key={tool.callId || toolIndex} toolResult={tool.result} />
              ) : null
            )}
          </React.Fragment>
        );
      })}
    </div>
  );
};

export default ChatToolActivity;
