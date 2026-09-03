import React from 'react';
import { ToolResult, WebFetchMetadata } from '../../types';
import {
  formatReferenceSize,
  ReferenceCodeBlock,
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
        {meta.contentType ? <span className="quiet-tool-muted">{meta.contentType}</span> : null}
      </div>

      {savedPath ? (
        <div className="quiet-tool-line">
          <span className="quiet-tool-emphasis">Saved path</span>
          <span className="quiet-tool-muted mono" title={savedPath}>
            {savedPath}
          </span>
        </div>
      ) : null}

      {meta.prompt ? (
        <div className="quiet-tool-line">
          <span className="quiet-tool-emphasis">Prompt</span>
          <span>{meta.prompt}</span>
        </div>
      ) : null}

      {meta.content ? (
        <div className="web-fetch-code-preview">
          <ReferenceCodeBlock content={truncateLines(meta.content, 80)} language="markdown" />
        </div>
      ) : null}
    </div>
  );
};

export default WebFetchRenderer;
