import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import { CopyButton, StatusBadge } from './shared';
import { detectLanguageFromPath } from './utils';

interface FileReadRendererProps {
  toolResult: ToolResult;
}

const FileReadRenderer: React.FC<FileReadRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileMetadata;
  if (!meta) return null;

  const language = meta.language || detectLanguageFromPath(meta.filePath);
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
  const fileContent = displayLines.join('\n');
  const maxLineNumber = startLine + displayLines.length - 1;
  const lineNumberWidth = Math.max(4, maxLineNumber.toString().length);

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
          <span className="font-medium">{meta.filePath}</span>
          <StatusBadge
            text={`${displayLines.length} lines`}
            variant={meta.truncated ? 'warning' : 'success'}
          />
          {remainingLines > 0 && <StatusBadge text={`${remainingLines} more`} variant="info" />}
          {language && <span className="text-kodelet-mid-gray">{language}</span>}
        </div>
        <CopyButton content={fileContent} />
      </div>

      {remainingLines > 0 && (
        <div className="text-xs text-kodelet-blue font-body">
          Use offset={startLine + (lineLimit || displayLines.length)} to continue reading
        </div>
      )}

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