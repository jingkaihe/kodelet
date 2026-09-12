import React from 'react';
import Anser from 'anser';
import { marked } from 'marked';
import type { ExtensionToolMetadata, ToolPresentation, ToolResult } from '../../types';
import { cn, detectLanguageFromPath, escapeHtml, formatFileSize, formatDuration } from '../../utils';

const MAX_TOOL_PRESENTATION_SUMMARY_LENGTH = 160;
const MAX_TOOL_PRESENTATION_BODY_LENGTH = 102400;
const textEncoder = new TextEncoder();

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const hasBoundedLength = (value: string, maximum: number): boolean =>
  Array.from(value).length <= maximum;

const hasBoundedByteLength = (value: string, maximum: number): boolean =>
  textEncoder.encode(value).length <= maximum;

const hasInvisibleSummaryFormatting = (value: string): boolean => /\p{Cf}/u.test(value);

const hasUnsafeSummaryControls = (value: string): boolean => /\p{Cc}/u.test(value);

export const getExtensionToolPresentation = (
  toolResult?: ToolResult
): ToolPresentation | undefined => {
  if (
    toolResult?.metadataType &&
    toolResult.metadataType.trim().toLowerCase() !== 'extension_tool'
  ) {
    return undefined;
  }
  const metadata = toolResult?.metadata as ExtensionToolMetadata | undefined;
  const raw = metadata?.data?.presentation;
  if (!isRecord(raw) || typeof raw.summary !== 'string') {
    return undefined;
  }

  const summary = raw.summary.replace(/\s+/g, ' ').trim();
  if (
    !summary ||
    !hasBoundedLength(summary, MAX_TOOL_PRESENTATION_SUMMARY_LENGTH) ||
    hasInvisibleSummaryFormatting(raw.summary) ||
    hasUnsafeSummaryControls(summary)
  ) {
    return undefined;
  }
  if (raw.body !== undefined && typeof raw.body !== 'string') {
    return undefined;
  }
  if (
    typeof raw.body === 'string' &&
    !hasBoundedByteLength(raw.body, MAX_TOOL_PRESENTATION_BODY_LENGTH)
  ) {
    return undefined;
  }
  if (raw.format !== undefined && typeof raw.format !== 'string') {
    return undefined;
  }
  const format = raw.format?.trim().toLowerCase() || 'text';
  if (format !== 'text' && format !== 'markdown') {
    return undefined;
  }

  return {
    summary,
    body: raw.body,
    format,
  };
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
    if (
      line.startsWith('diff --git ') ||
      line.startsWith('diff --cc ') ||
      line.startsWith('diff --combined ')
    ) {
      seenHunk = false;
      parsedLines.push({ kind: 'meta', content: line });
      return;
    }
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

// Only SGR styling belongs in a transcript. Ignore cursor commands and OSC/DCS
// payloads (titles, hyperlinks, clipboard data), including incomplete updates.
// biome-ignore lint/suspicious/noControlCharactersInRegex: terminal protocol bytes
const terminalControlSequence = /\u001b(?:\[[0-?]*[ -/]*[@-~]?|[\]PX^_][\s\S]*?(?:\u0007|\u001b\\|$)|[ -/]*[@-~]?)/g;
// biome-ignore lint/suspicious/noControlCharactersInRegex: ANSI SGR sequence
const terminalSGR = /^\u001b\[[\d;]*m$/;

const terminalColor = (color: string, truecolor: string): string | undefined => {
  if (!color) return undefined;
  if (color === 'ansi-truecolor') return `rgb(${truecolor})`;
  if (!color.startsWith('ansi-palette-')) return `var(--${color})`;

  const index = Number(color.slice('ansi-palette-'.length));
  if (index >= 232) {
    const gray = 8 + (index - 232) * 10;
    return `rgb(${gray}, ${gray}, ${gray})`;
  }
  const levels = [0, 95, 135, 175, 215, 255];
  const cube = index - 16;
  return `rgb(${levels[Math.floor(cube / 36)]}, ${levels[Math.floor(cube / 6) % 6]}, ${levels[cube % 6]})`;
};

const terminalTextStyle = (entry: Anser.AnserJsonEntry): React.CSSProperties => ({
  color: terminalColor(entry.fg, entry.fg_truecolor),
  backgroundColor: terminalColor(entry.bg, entry.bg_truecolor),
  fontWeight: entry.decorations.includes('bold') ? 700 : undefined,
  opacity: entry.decorations.includes('dim') ? 0.7 : undefined,
  fontStyle: entry.decorations.includes('italic') ? 'italic' : undefined,
  textDecorationLine: [
    entry.decorations.includes('underline') ? 'underline' : '',
    entry.decorations.includes('strikethrough') ? 'line-through' : '',
  ].filter(Boolean).join(' ') || undefined,
  visibility: entry.decorations.includes('hidden') ? 'hidden' : undefined,
});

export const ReferenceTerminal: React.FC<{ output: string }> = ({ output }) => {
  const lines = React.useMemo(() => {
    const text = output
      .split('\u009b').join('\u001b[')
      .replace(terminalControlSequence, (sequence) => terminalSGR.test(sequence) ? sequence : '')
      .replace(/\r/g, '');
    // Each update contains accumulated output, so parsing starts fresh. Styles
    // carry across newlines, but never leak into another command or rerender.
    const entries = Anser.ansiToJson(truncateLines(text, 120), { use_classes: true, remove_empty: true });
    const result: Array<{ text: string; content: React.ReactNode[] }> = [{ text: '', content: [] }];
    entries.forEach((entry, entryIndex) => {
      const style = terminalTextStyle(entry);
      entry.content.split('\n').forEach((content, lineIndex) => {
        if (lineIndex > 0) result.push({ text: '', content: [] });
        const line = result[result.length - 1];
        line.text += content;
        if (content) {
          line.content.push(<span key={`${entryIndex}-${lineIndex}`} style={style}>{content}</span>);
        }
      });
    });
    return result;
  }, [output]);

  return (
    <div className="tool-terminal">
      <div className="tool-terminal-body">
        <pre>{lines.map((line, index) => (
          <div key={index} className={cn('tool-terminal-line', line.text.trim() === '---' && 'tool-terminal-separator')}>
            {line.content.length ? line.content : '\u00a0'}
          </div>
        ))}</pre>
      </div>
    </div>
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

const safeMarkdownRenderer = new marked.Renderer();
const defaultMarkdownRenderer = new marked.Renderer();

const decodeUrlCodePoint = (code: string, radix: number): string => {
  const value = Number.parseInt(code, radix);
  if (!Number.isInteger(value) || value < 0 || value > 0x10ffff || (value >= 0xd800 && value <= 0xdfff)) {
    return '';
  }
  return String.fromCodePoint(value);
};

const decodeUrlCharacterReferences = (value: string): string => {
  let decoded = value;
  for (let iteration = 0; iteration < 4; iteration += 1) {
    const next = decoded
      .replace(/&#x([\da-f]+);?/gi, (_match, code: string) => decodeUrlCodePoint(code, 16))
      .replace(/&#(\d+);?/g, (_match, code: string) => decodeUrlCodePoint(code, 10))
      .replace(/&(amp|colon|newline|tab);/gi, (_match, name: string) => {
        const values: Record<string, string> = {
          amp: '&',
          colon: ':',
          newline: '\n',
          tab: '\t',
        };
        return values[name.toLowerCase()] || '';
      });
    if (next === decoded) {
      return decoded;
    }
    decoded = next;
  }
  return decoded;
};

const isSafeMarkdownUrl = (href: string): boolean => {
  const normalized = [...decodeUrlCharacterReferences(href.trim())]
    .filter((character) => {
      const code = character.charCodeAt(0);
      return code > 32 && code !== 127;
    })
    .join('')
    .toLowerCase();
  const scheme = /^([a-z][a-z\d+.-]*):/.exec(normalized)?.[1];
  return !scheme || ['file', 'http', 'https', 'mailto'].includes(scheme);
};

safeMarkdownRenderer.html = (html) => escapeHtml(html);
safeMarkdownRenderer.link = (href, title, text) =>
  isSafeMarkdownUrl(href) ? defaultMarkdownRenderer.link(href, title, text) : text;
safeMarkdownRenderer.image = (href, title, text) =>
  isSafeMarkdownUrl(href) ? defaultMarkdownRenderer.image(href, title, text) : escapeHtml(text);

export const renderSafeMarkdown = (content?: string | null): string =>
  content ? ((marked.parse(content, { renderer: safeMarkdownRenderer }) as string) || '') : '';

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
