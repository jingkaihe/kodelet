import React from 'react';
import { ToolResult, WebFetchMetadata } from '../../types';
import {
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
  const processedType = 'Fetched';

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[{ text: processedType.toLowerCase(), variant: 'success' }]}
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
        <pre className="overflow-x-auto rounded-lg border border-kodelet-light-gray bg-kodelet-light p-3 text-xs font-mono text-kodelet-dark">
          {truncateLines(meta.content, 80)}
        </pre>
      ) : null}
    </div>
  );
};

export default WebFetchRenderer;
