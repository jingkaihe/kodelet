import React from 'react';
import { marked } from 'marked';
import { cn, detectLanguageFromPath, formatFileSize, formatDuration } from '../../utils';

export const normalizeToolName = (toolName: string): string => {
  if (toolName === 'grep') {
    return 'grep_tool';
  }
  if (toolName === 'glob') {
    return 'glob_tool';
  }
  if (toolName.startsWith('mcp__') || toolName.startsWith('mcp_')) {
    return 'mcp_tool';
  }
  return toolName;
};

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
  oldLine?: number;
  newLine?: number;
}

const hunkHeaderPattern = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

const splitDiffLines = (text: string): string[] => {
  const lines = text.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') {
    return lines.slice(0, -1);
  }
  return lines;
};

export const parseUnifiedDiff = (unifiedDiff: string): ReferenceDiffLine[] => {
  const parsedLines: ReferenceDiffLine[] = [];
  let oldLine = 0;
  let newLine = 0;
  let seenHunk = false;

  splitDiffLines(unifiedDiff).forEach((line) => {
    if (!seenHunk && (line.startsWith('+++ ') || line.startsWith('--- '))) {
      return;
    }
    if (line.startsWith('@@')) {
      const match = line.match(hunkHeaderPattern);
      oldLine = match ? Number(match[1]) : 0;
      newLine = match ? Number(match[2]) : 0;
      seenHunk = true;
      parsedLines.push({ kind: 'header', content: line });
      return;
    }
    if (line.startsWith('\\ No newline')) {
      parsedLines.push({ kind: 'meta', content: line });
      return;
    }
    if (!seenHunk) {
      parsedLines.push({ kind: 'meta', content: line });
      return;
    }
    if (line.startsWith('+')) {
      parsedLines.push({ kind: 'added', content: line.slice(1), newLine });
      newLine += 1;
      return;
    }
    if (line.startsWith('-')) {
      parsedLines.push({ kind: 'removed', content: line.slice(1), oldLine });
      oldLine += 1;
      return;
    }
    if (line.startsWith(' ')) {
      parsedLines.push({ kind: 'context', content: line.slice(1), oldLine, newLine });
      oldLine += 1;
      newLine += 1;
      return;
    }
    parsedLines.push({ kind: 'context', content: line, oldLine, newLine });
    oldLine += 1;
    newLine += 1;
  });

  return parsedLines;
};

export const ReferenceDiffBlock: React.FC<{ lines: ReferenceDiffLine[] }> = ({ lines }) => {
  if (lines.length === 0) {
    return null;
  }

  const oldWidth = Math.max(1, ...lines.map((line) => String(line.oldLine || '').length));
  const newWidth = Math.max(1, ...lines.map((line) => String(line.newLine || '').length));

  return (
    <div className="diff-block">
      {lines.map((line, index) => {
        const sign =
          line.kind === 'added'
            ? '+'
            : line.kind === 'removed'
              ? '-'
              : line.kind === 'header'
                ? ' '
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
            style={{
              gridTemplateColumns: `${oldWidth}ch ${newWidth}ch 1.2rem minmax(0, 1fr)`,
            }}
          >
            <span className="diff-line-number">{line.oldLine || ''}</span>
            <span className="diff-line-number">{line.newLine || ''}</span>
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
