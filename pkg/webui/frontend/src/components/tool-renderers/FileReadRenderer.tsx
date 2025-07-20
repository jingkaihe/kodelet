import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import { ToolCard, CopyButton, MetadataRow } from './shared';
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
  const totalLines = meta.totalLines || lines.length || 0;
  
  // Remove trailing empty lines, but exclude truncation messages
  let lastNonEmptyIndex = lines.length - 1;
  const isTruncationMessage = (line: string) => 
    line.includes('lines remaining') || line.includes('truncated due to');
  
  // If the last line is a truncation message, keep it
  if (lastNonEmptyIndex >= 0 && isTruncationMessage(lines[lastNonEmptyIndex])) {
    // Keep the truncation message, but remove empty lines before it
    let searchIndex = lastNonEmptyIndex - 1;
    while (searchIndex >= 0 && lines[searchIndex] === '') {
      searchIndex--;
    }
    lastNonEmptyIndex = Math.max(searchIndex + 1, lastNonEmptyIndex);
  } else {
    // Remove trailing empty lines normally
    while (lastNonEmptyIndex >= 0 && lines[lastNonEmptyIndex] === '') {
      lastNonEmptyIndex--;
    }
  }
  
  const displayLines = lines.slice(0, lastNonEmptyIndex + 1);
  
  const fileContent = displayLines.join('\n');
  const maxLineNumber = startLine + displayLines.length - 1;
  const lineNumberWidth = Math.max(4, maxLineNumber.toString().length);
  
  const badges = [];
  if (meta.truncated) {
    if (remainingLines > 0) {
      badges.push({ text: `${remainingLines} more lines`, className: 'badge-info' });
    } else {
      badges.push({ text: 'Truncated', className: 'badge-warning' });
    }
  }

  return (
    <ToolCard
      title="📄 File Read"
      badge={badges[0]}
      actions={<CopyButton content={fileContent} />}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Path" value={meta.filePath} monospace />
          {startLine > 1 && <MetadataRow label="Starting at line" value={startLine} />}
          <MetadataRow 
            label="Lines shown" 
            value={`${displayLines.length}${totalLines > 0 && totalLines > displayLines.length ? ` of ${totalLines}` : ''}`} 
          />
          {lineLimit && lineLimit !== 2000 && <MetadataRow label="Line limit" value={lineLimit} />}
          {remainingLines > 0 && (
            <MetadataRow 
              label="Remaining" 
              value={`${remainingLines} lines`} 
            />
          )}
          {language && <MetadataRow label="Language" value={language} />}
        </div>
        {remainingLines > 0 && (
          <div className="mt-2 text-xs text-info">
            💡 Use offset={startLine + (lineLimit || displayLines.length)} to continue reading
          </div>
        )}
      </div>

      <div 
        className="bg-base-300 text-sm font-mono rounded-lg" 
        style={{ maxHeight: '600px', overflowY: 'auto' }}
      >
        <div className="flex p-4">
          {/* Line Numbers */}
          <div className="text-base-content/50 flex-shrink-0 whitespace-pre select-none">
            {displayLines.map((_, index) => {
              const lineNumber = (startLine + index).toString().padStart(lineNumberWidth, ' ');
              return (
                <div key={index} className="min-h-[1.2em] text-right pr-2">
                  {lineNumber}
                </div>
              );
            })}
          </div>
          
          {/* Code Content */}
          <div className="flex-grow overflow-x-auto whitespace-pre">
            {displayLines.map((line, index) => (
              <div key={index} className="min-h-[1.2em]">
                {line === '' ? '\u00A0' : line}
              </div>
            ))}
          </div>
        </div>
      </div>
    </ToolCard>
  );
};

export default FileReadRenderer;