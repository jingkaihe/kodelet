import React from 'react';
import { ToolResult, FileMetadata } from '../../types';
import {
  estimateLanguageFromPath,
  formatReferenceSize,
  ReferenceCodeBlock,
  ReferenceToolKVGrid,
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
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">written</span>
        {sizeText ? <span className="quiet-tool-muted">{sizeText}</span> : null}
      </div>
      <div className="quiet-tool-path">{meta.filePath}</div>

      <ReferenceToolKVGrid
        items={[
          { label: 'Language', value: language },
          { label: 'Lines', value: lines.length, monospace: true },
        ]}
      />

      {meta.content ? (
        <ReferenceCodeBlock content={truncateLines(meta.content, 80)} language={language} />
      ) : null}
    </div>
  );
};

export default FileWriteRenderer;
