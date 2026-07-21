import React from 'react';
import {
  BookOpenText,
  FileCog,
  FileImage,
  FilePen,
  FilePlus,
  FileText,
  Globe,
  Pencil,
  PocketKnife,
  Search,
  SquareTerminal,
  Wrench,
  type LucideIcon,
} from 'lucide-react';
import type { ChatRenderToolCall, ToolResult } from '../../types';
import { cn, formatDuration } from '../../utils';
import ToolRenderer from '../ToolRenderer';
import {
  formatTaskRunElapsed,
  getTaskRunSnapshot,
} from '../tool-renderers/TaskRunRenderer';
import { normalizeToolName, ReferenceCodeBlock } from '../tool-renderers/reference';

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
      operations[operations.length - 1] =
        `${previousOperation} → ${trimmedLine.slice('*** Move to: '.length).trim()}`;
    }
  });

  return summarizeList(operations);
};

const summarizeApplyPatchResult = (
  metadata: Record<string, unknown> | null
): string | undefined => {
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
      return formatToolSummary(
        'Search',
        pattern && path ? `${pattern} in ${path}` : pattern || path
      );
    }

    case 'code_search':
      return formatToolSummary('Code search', getStringField(input, 'query'));

    case 'subagent':
      return formatToolSummary('Delegated task', getStringField(input, 'task'));

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

const toolSummaryIcons: Partial<Record<string, LucideIcon>> = {
  'Apply patch': Pencil,
  Bash: SquareTerminal,
  'Code execution': FileCog,
  'Code search': Search,
  'Delegated task': FileCog,
  'Edit file': FilePen,
  'Extension tool': Wrench,
  'Fetch URL': Globe,
  'Find files': Search,
  'Find in page': Search,
  'Open page': Globe,
  'Read conversation': BookOpenText,
  'Read file': FileText,
  Search,
  Skill: PocketKnife,
  'View image': FileImage,
  'Web search': Globe,
  'Write file': FilePlus,
};

const ActivitySummaryText: React.FC<{
  summaryText: string;
  status?: string;
}> = ({ summaryText, status }) => {
  const { label, detail } = splitActivitySummary(summaryText);
  const SummaryIcon = toolSummaryIcons[label];

  return (
    <span className="tool-summary-text" title={summaryText}>
      {detail ? <span className="sr-only">{summaryText}</span> : null}
      {SummaryIcon ? (
        <SummaryIcon
          aria-hidden="true"
          className={cn(
            'tool-summary-icon',
            status === 'running' && 'tool-summary-icon-running',
            status === 'failed' && 'tool-summary-icon-error'
          )}
          size={14}
          strokeWidth={2.2}
        />
      ) : (
        <span className="tool-summary-label" aria-hidden={detail ? 'true' : undefined}>
          {detail ? `${label}:` : label}
        </span>
      )}
      {SummaryIcon && !detail ? <span className="tool-summary-label">{label}</span> : null}
      {detail ? (
        <span className="tool-summary-detail" aria-hidden="true">
          {' '}
          {detail}
        </span>
      ) : null}
    </span>
  );
};

const ChatToolActivity: React.FC<ChatToolActivityProps> = ({ tools }) => {
  if (tools.length === 0) {
    return null;
  }

  return (
    <div className="activity-stack">
      {tools.map((toolCall, toolIndex) => {
        const summaryText = getToolSummary(toolCall);
        const activityStatus = getToolActivityStatus(toolCall);

        return (
          <details
            key={`${toolCall.callId || `${toolCall.name}-${toolIndex}`}-${activityStatus === 'running' ? 'running' : 'settled'}`}
            className={cn(
              'activity-card',
              activityStatus === 'running' && 'activity-card-live',
              activityStatus === 'failed' && 'activity-card-error'
            )}
            open={activityStatus === 'running' ? true : undefined}
          >
            <summary className="tool-summary activity-summary" title={summaryText}>
              <span className="tool-summary-chevron" aria-hidden="true">
                ›
              </span>
              <ActivitySummaryText summaryText={summaryText} status={activityStatus} />
              <span className="tool-summary-status" aria-label={`Tool ${activityStatus}`}>
                {activityStatus}
              </span>
            </summary>

            <div className="activity-detail-content space-y-2">
              {toolCall.result ? (
                <ToolRenderer
                  isPartial={toolCall.inProgress}
                  toolInput={toolCall.input}
                  toolResult={toolCall.result}
                />
              ) : (
                <>
                  <p className="tool-awaiting">Awaiting tool result…</p>
                  {toolCall.input ? (
                    <div className="running-tool-input-preview">
                      <ReferenceCodeBlock
                        content={formatToolInputPreview(toolCall.input)}
                        language="json"
                      />
                    </div>
                  ) : null}
                </>
              )}
            </div>
          </details>
        );
      })}
    </div>
  );
};

export default ChatToolActivity;
