import React from 'react';
import { marked } from 'marked';
import { cn, detectLanguageFromPath, formatFileSize, formatDuration } from '../../utils';

export const TOOL_LABELS: Record<string, string> = {
  bash: 'Shell Command',
  file_read: 'File Read',
  file_write: 'File Write',
  file_edit: 'File Edit',
  apply_patch: 'Apply Patch',
  grep_tool: 'Search Results',
  glob_tool: 'File Discovery',
  web_fetch: 'Web Fetch',
  view_image: 'View Image',
  openai_web_search: 'OpenAI Web Search',
};

export const TOOL_ICONS: Record<string, string> = {
  bash: '▸',
  file_read: '[]',
  file_write: '<>',
  file_edit: '//',
  apply_patch: '+/-',
  grep_tool: '::',
  glob_tool: '**',
  web_fetch: '//',
  view_image: '◫',
  openai_web_search: '>>',
};

export const normalizeToolName = (toolName: string): string => {
  if (toolName === 'grep') {
    return 'grep_tool';
  }
  if (toolName === 'glob') {
    return 'glob_tool';
  }
  return toolName;
};

type ToolBadgeVariant = 'success' | 'info' | 'warning' | 'error' | 'neutral';

const badgeClassName: Record<ToolBadgeVariant, string> = {
  success: 'tool-badge-success',
  info: 'tool-badge-info',
  warning: 'tool-badge-warning',
  error: 'tool-badge-error',
  neutral: 'tool-badge-neutral',
};

interface ToolHeaderProps {
  title: string;
  subtitle?: string | null;
  badges?: Array<{ text: string; variant?: ToolBadgeVariant }>;
}

export const ReferenceToolHeader: React.FC<ToolHeaderProps> = ({
  title,
  subtitle,
  badges = [],
}) => (
  <div className="tool-header">
    <div>
      <div className="tool-title">{title}</div>
      {subtitle ? <div className="tool-subtitle">{subtitle}</div> : null}
    </div>
    {badges.length > 0 ? (
      <div className="tool-badges">
        {badges.map((badge, index) => (
          <span
            className={cn('tool-badge', badgeClassName[badge.variant || 'neutral'])}
            key={`${badge.text}-${index}`}
          >
            {badge.text}
          </span>
        ))}
      </div>
    ) : null}
  </div>
);

interface ToolKVGridProps {
  items: Array<{ label: string; value?: string | number | null; monospace?: boolean }>;
}

export const ReferenceToolKVGrid: React.FC<ToolKVGridProps> = ({ items }) => {
  const validItems = items.filter(
    (item) => item.value !== null && item.value !== undefined && item.value !== ''
  );

  if (validItems.length === 0) {
    return null;
  }

  return (
    <div className="tool-kv-grid">
      {validItems.map((item) => (
        <div className="tool-kv-item" key={item.label}>
          <span className="tool-kv-label">{item.label}</span>
          <span className={cn('tool-kv-value', item.monospace && 'mono')}>
            {String(item.value)}
          </span>
        </div>
      ))}
    </div>
  );
};

export const ReferenceToolNote: React.FC<{ text?: string | null }> = ({ text }) => {
  if (!text) {
    return null;
  }

  return <div className="tool-note">{text}</div>;
};

interface ReferenceCodeBlockProps {
  content: string;
  language?: string;
}

export const ReferenceCodeBlock: React.FC<ReferenceCodeBlockProps> = ({
  content,
  language,
}) => (
  <pre className="tool-code-block">
    <code className={language ? `language-${language}` : undefined}>{content}</code>
  </pre>
);

interface ReferenceCodeListProps {
  items: string[];
}

export const ReferenceCodeList: React.FC<ReferenceCodeListProps> = ({ items }) => {
  if (items.length === 0) {
    return null;
  }

  return (
    <div className="tool-code-list">
      {items.map((item) => (
        <code className="tool-inline-code" key={item}>
          {item}
        </code>
      ))}
    </div>
  );
};

export const truncateLines = (text: string, maxLines = 60): string => {
  const lines = text.split('\n');
  if (lines.length <= maxLines) {
    return text;
  }

  return `${lines.slice(0, maxLines).join('\n')}\n... (${lines.length - maxLines} more lines)`;
};

type DiffKind = 'context' | 'added' | 'removed' | 'header' | 'meta';

export interface ReferenceDiffLine {
  kind: DiffKind;
  content: string;
}

export const parseUnifiedDiff = (unifiedDiff: string): ReferenceDiffLine[] =>
  unifiedDiff.split('\n').map((line) => {
    if (line.startsWith('@@')) {
      return { kind: 'header', content: line };
    }
    if (line.startsWith('+++') || line.startsWith('---')) {
      return { kind: 'meta', content: line };
    }
    if (line.startsWith('+')) {
      return { kind: 'added', content: line.slice(1) };
    }
    if (line.startsWith('-')) {
      return { kind: 'removed', content: line.slice(1) };
    }
    if (line.startsWith(' ')) {
      return { kind: 'context', content: line.slice(1) };
    }
    return { kind: 'context', content: line };
  });

export const compactDiffLines = (
  lines: ReferenceDiffLine[],
  keepStart = 1,
  keepEnd = 1,
  maxTotalLines = 48
): ReferenceDiffLine[] => {
  const compacted: ReferenceDiffLine[] = [];
  let contextRun: ReferenceDiffLine[] = [];

  const flushContextRun = () => {
    if (contextRun.length === 0) {
      return;
    }

    if (contextRun.length <= keepStart + keepEnd + 1) {
      compacted.push(...contextRun);
    } else {
      compacted.push(...contextRun.slice(0, keepStart));
      compacted.push({
        kind: 'meta',
        content: `... ${contextRun.length - keepStart - keepEnd} unchanged lines ...`,
      });
      compacted.push(...contextRun.slice(-keepEnd));
    }
    contextRun = [];
  };

  for (const line of lines) {
    if (line.kind === 'context') {
      contextRun.push(line);
      continue;
    }
    flushContextRun();
    compacted.push(line);
  }

  flushContextRun();

  if (compacted.length <= maxTotalLines) {
    return compacted;
  }

  return [
    ...compacted.slice(0, maxTotalLines - 1),
    {
      kind: 'meta',
      content: `... ${compacted.length - maxTotalLines + 1} more diff lines omitted ...`,
    },
  ];
};

export const ReferenceDiffBlock: React.FC<{ lines: ReferenceDiffLine[] }> = ({ lines }) => {
  if (lines.length === 0) {
    return null;
  }

  return (
    <div className="diff-block">
      {lines.map((line, index) => {
        const sign =
          line.kind === 'added'
            ? '+'
            : line.kind === 'removed'
              ? '-'
              : line.kind === 'header'
                ? '@@'
                : line.kind === 'meta'
                  ? '›'
                  : ' ';

        return (
          <div
            className={cn(
              'diff-line',
              line.kind !== 'context' && `diff-line-${line.kind}`
            )}
            key={`${line.kind}-${index}`}
          >
            <span className="diff-sign">{sign}</span>
            <span className="diff-content">{line.content || '\u00A0'}</span>
          </div>
        );
      })}
    </div>
  );
};

export const highlightPattern = (text: string, pattern: string): string => {
  const escapedText = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
  if (!pattern) {
    return escapedText;
  }

  try {
    const regex = new RegExp(`(${pattern.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`, 'gi');
    return escapedText.replace(regex, '<mark class="grep-mark">$1</mark>');
  } catch {
    return escapedText;
  }
};

export const ReferenceTerminal: React.FC<{ output: string }> = ({ output }) => {
  const escapedOutput = output
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');

  const lines = truncateLines(escapedOutput, 120)
    .split('\n')
    .map((line) => (
      `<div class="${line.trim() === '---' ? 'tool-terminal-line tool-terminal-separator' : 'tool-terminal-line'}">${line || '&nbsp;'}</div>`
    ))
    .join('');

  return (
    <div
      className="tool-terminal"
      dangerouslySetInnerHTML={{
        __html:
          '<div class="tool-terminal-header">' +
          '<div class="tool-terminal-lights">' +
          '<span class="tool-terminal-light red"></span>' +
          '<span class="tool-terminal-light yellow"></span>' +
          '<span class="tool-terminal-light green"></span>' +
          '</div>' +
          '<div class="tool-terminal-label">terminal output</div>' +
          '</div>' +
          `<div class="tool-terminal-body"><pre>${lines}</pre></div>`,
      }}
    />
  );
};

interface ReferenceFileListProps {
  items: Array<{ path: string; meta?: string }>;
}

export const ReferenceFileList: React.FC<ReferenceFileListProps> = ({ items }) => (
  <div className="tool-file-list">
    {items.map((item) => (
      <div className="tool-file-item" key={`${item.path}-${item.meta || ''}`}>
        <span className="tool-file-path">{item.path}</span>
        {item.meta ? <span className="tool-file-meta">{item.meta}</span> : null}
      </div>
    ))}
  </div>
);

export const renderMarkdown = (content?: string | null): string =>
  content ? ((marked.parse(content) as string) || '') : '';

export const formatReferenceSize = (value?: number | null): string => {
  if (value === null || value === undefined) {
    return '';
  }
  return formatFileSize(value);
};

export const formatReferenceDuration = (value?: number | string | null): string => {
  if (value === null || value === undefined || value === '') {
    return '';
  }
  return formatDuration(value);
};

export const estimateLanguageFromPath = (path?: string | null): string =>
  path ? detectLanguageFromPath(path) : '';
