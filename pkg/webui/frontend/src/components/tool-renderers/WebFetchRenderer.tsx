import React from 'react';
import { ToolResult, WebFetchMetadata } from '../../types';
import {
  formatReferenceSize,
  ReferenceCodeBlock,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  TOOL_ICONS,
  truncateLines,
} from './reference';

interface WebFetchRendererProps {
  toolResult: ToolResult;
}

const WebFetchRenderer: React.FC<WebFetchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as WebFetchMetadata;
  if (!meta || !meta.url) return null;

  const savedPath = meta.savedPath || meta.filePath;
  const processedType = meta.processedType || 'fetched';

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          { text: processedType.replace('_', ' '), variant: 'success' },
          { text: formatReferenceSize(meta.size), variant: 'neutral' },
        ]}
        subtitle={meta.url}
        title={`${TOOL_ICONS.web_fetch} Web Fetch`}
      />

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
