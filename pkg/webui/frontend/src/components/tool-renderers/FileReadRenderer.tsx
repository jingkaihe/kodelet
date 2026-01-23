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
  
  const getBadge = () => {
    if (meta.truncated) {
      if (remainingLines > 0) {
        return { 
          text: `${remainingLines} more`, 
          className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' 
        };
      }
      return { 
        text: 'Truncated', 
        className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-orange/10 text-kodelet-orange border border-kodelet-orange/20' 
      };
    }
    return { 
      text: 'Read', 
      className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20' 
    };
  };

  return (
    <ToolCard
      title="File Read"
      badge={getBadge()}
      actions={<CopyButton content={fileContent} />}
    >
      <div className="text-xs text-kodelet-dark/60 mb-3 font-mono">
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
          <div className="mt-2 text-xs text-kodelet-blue font-body">
            Use offset={startLine + (lineLimit || displayLines.length)} to continue reading
          </div>
        )}
      </div>

      <div 
        className="bg-kodelet-light text-sm font-mono rounded-lg border border-kodelet-light-gray" 
        style={{ maxHeight: '600px', overflowY: 'auto' }}
      >
        <div className="flex p-4">
          <div className="text-kodelet-mid-gray flex-shrink-0 whitespace-pre select-none">
            {displayLines.map((_, index) => {
              const lineNumber = (startLine + index).toString().padStart(lineNumberWidth, ' ');
              return (
                <div key={index} className="min-h-[1.2em] text-right pr-2">
                  {lineNumber}
                </div>
              );
            })}
          </div>
          
          <div className="flex-grow overflow-x-auto whitespace-pre text-kodelet-dark">
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