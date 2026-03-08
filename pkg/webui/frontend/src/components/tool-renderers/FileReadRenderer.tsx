import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import {
  estimateLanguageFromPath,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  ReferenceToolNote,
  TOOL_ICONS,
} from './reference';

interface FileReadRendererProps {
  toolResult: ToolResult;
}

const FileReadRenderer: React.FC<FileReadRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileMetadata;
  if (!meta) return null;

  const language = meta.language || estimateLanguageFromPath(meta.filePath);
  const lines = meta.lines || [];
  const startLine = meta.offset || 1;
  const lineLimit = meta.lineLimit;
  const remainingLines = meta.remainingLines || 0;

  let lastNonEmptyIndex = lines.length - 1;
  const isTruncationMessage = (line: string) =>
    line.includes('lines remaining') || line.includes('truncated due to');

  if (lastNonEmptyIndex >= 0 && isTruncationMessage(lines[lastNonEmptyIndex])) {
    let searchIndex = lastNonEmptyIndex - 1;
    while (searchIndex >= 0 && lines[searchIndex] === '') {
      searchIndex--;
    }
    lastNonEmptyIndex = Math.max(searchIndex + 1, lastNonEmptyIndex);
  } else {
    while (lastNonEmptyIndex >= 0 && lines[lastNonEmptyIndex] === '') {
      lastNonEmptyIndex--;
    }
  }

  const displayLines = lines.slice(0, lastNonEmptyIndex + 1);
  const maxLineNumber = startLine + displayLines.length - 1;
  const lineNumberWidth = Math.max(4, maxLineNumber.toString().length);

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          {
            text: `${displayLines.length} lines`,
            variant: meta.truncated ? 'warning' : 'success',
          },
          ...(remainingLines > 0
            ? [{ text: `${remainingLines} more`, variant: 'info' as const }]
            : []),
        ]}
        subtitle={meta.filePath}
        title={`${TOOL_ICONS.file_read} File Read`}
      />

      <ReferenceToolKVGrid
        items={[
          { label: 'Language', value: language },
          { label: 'Offset', value: startLine, monospace: true },
          { label: 'Line limit', value: lineLimit, monospace: true },
        ]}
      />

      {remainingLines > 0 ? (
        <ReferenceToolNote
          text={`Use offset=${startLine + (lineLimit || displayLines.length)} to continue reading this file.`}
        />
      ) : null}

      <div
        className="bg-kodelet-light text-sm font-mono rounded border border-kodelet-light-gray"
        style={{ maxHeight: '400px', overflowY: 'auto' }}
      >
        <div className="flex p-3">
          <div className="text-kodelet-mid-gray flex-shrink-0 whitespace-pre select-none text-xs">
            {displayLines.map((_, index) => {
              const lineNumber = (startLine + index).toString().padStart(lineNumberWidth, ' ');
              return (
                <div key={index} className="min-h-[1.2em] text-right pr-2">
                  {lineNumber}
                </div>
              );
            })}
          </div>
          <div className="flex-grow overflow-x-auto whitespace-pre text-kodelet-dark text-xs">
            {displayLines.map((line, index) => (
              <div key={index} className="min-h-[1.2em]">
                {line === '' ? '\u00A0' : line}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
};

export default FileReadRenderer;
