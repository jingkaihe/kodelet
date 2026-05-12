import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import {
  estimateLanguageFromPath,
  ReferenceCodeBlock,
  ReferenceToolKVGrid,
  ReferenceToolNote,
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

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{displayLines.length} lines</span>
        {remainingLines > 0 ? (
          <span className="quiet-tool-muted">{remainingLines} more</span>
        ) : null}
        {meta.truncated ? <span className="quiet-tool-warning">truncated</span> : null}
      </div>
      <div className="quiet-tool-path">{meta.filePath}</div>

      <ReferenceToolKVGrid
        items={[
          { label: 'Language', value: language },
          { label: 'Offset', value: startLine, monospace: true },
          { label: 'Line limit', value: lineLimit, monospace: true },
        ]}
      />

      {remainingLines > 0 ? (
        <ReferenceToolNote
          text={`Use offset=${startLine + lines.length} to continue reading this file.`}
        />
      ) : null}

      <ReferenceCodeBlock content={displayLines.join('\n')} language={language} />
    </div>
  );
};

export default FileReadRenderer;
