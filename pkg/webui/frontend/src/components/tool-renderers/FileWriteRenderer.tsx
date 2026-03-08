import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import {
  estimateLanguageFromPath,
  formatReferenceSize,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  TOOL_ICONS,
  truncateLines,
} from './reference';

interface FileWriteMetadata extends FileMetadata {
  content?: string;
}

interface FileWriteRendererProps {
  toolResult: ToolResult;
}

const FileWriteRenderer: React.FC<FileWriteRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as FileWriteMetadata;
  if (!meta) return null;

  const language = meta.language || estimateLanguageFromPath(meta.filePath);
  const sizeText = meta.size ? formatReferenceSize(meta.size) : '';
  const lines = meta.content?.split('\n') || [];

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[{ text: 'Written', variant: 'success' }]}
        subtitle={meta.filePath}
        title={`${TOOL_ICONS.file_write} File Write`}
      />

      <ReferenceToolKVGrid
        items={[
          { label: 'Language', value: language },
          { label: 'Size', value: sizeText },
          { label: 'Lines', value: lines.length, monospace: true },
        ]}
      />

      {meta.content ? (
        <pre className="overflow-x-auto rounded-lg border border-kodelet-light-gray bg-kodelet-light p-3 text-xs font-mono text-kodelet-dark">
          {truncateLines(meta.content, 80)}
        </pre>
      ) : null}
    </div>
  );
};

export default FileWriteRenderer;
