import React from 'react';
import { ToolResult, WebFetchMetadata } from '../../types';
import {
  formatReferenceSize,
  ReferenceCodeBlock,
  ReferenceToolKVGrid,
  truncateLines,
} from './reference';

interface WebFetchRendererProps {
  toolResult: ToolResult;
}

const processedTypeLabel = (processedType?: string): string => {
  switch (processedType) {
    case 'ai_extracted':
      return 'extracted summary';
    case 'markdown':
      return 'markdown content';
    case 'saved':
      return 'saved page';
    default:
      return 'fetched page';
  }
};

const WebFetchRenderer: React.FC<WebFetchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as WebFetchMetadata;
  if (!meta || !meta.url) return null;

  const savedPath = meta.savedPath || meta.filePath;
  const processedType = meta.processedType || 'fetched';
  const statusText = processedTypeLabel(processedType);
  const sizeText = formatReferenceSize(meta.size);

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{statusText}</span>
        {sizeText ? <span className="quiet-tool-muted">{sizeText}</span> : null}
      </div>
      <div className="quiet-tool-path">{meta.url}</div>

      <ReferenceToolKVGrid
        items={[
          { label: 'Content type', value: meta.contentType },
          { label: 'Saved path', value: savedPath, monospace: true },
          { label: 'Prompt', value: meta.prompt },
        ]}
      />

      {meta.content ? (
        <ReferenceCodeBlock content={truncateLines(meta.content, 80)} language="markdown" />
      ) : null}
    </div>
  );
};

export default WebFetchRenderer;
