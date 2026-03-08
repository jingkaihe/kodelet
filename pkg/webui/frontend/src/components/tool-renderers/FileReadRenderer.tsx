import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import {
  estimateLanguageFromPath,
  ReferenceCodeBlock,
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
          text={`Use offset=${startLine + lines.length} to continue reading this file.`}
        />
      ) : null}

      <ReferenceCodeBlock content={displayLines.join('\n')} language={language} />
    </div>
  );
};

export default FileReadRenderer;
