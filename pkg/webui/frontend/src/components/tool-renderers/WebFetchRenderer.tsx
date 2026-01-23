import React, { useState } from 'react';
import { ToolResult, WebFetchMetadata } from '../../types';
import { StatusBadge, ExternalLink } from './shared';
import { escapeUrl } from './utils';

interface WebFetchRendererProps {
  toolResult: ToolResult;
}

const WebFetchRenderer: React.FC<WebFetchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as WebFetchMetadata;
  const [showContent, setShowContent] = useState(false);
  if (!meta || !meta.url) return null;

  const savedPath = meta.savedPath || meta.filePath;
  const safeUrl = escapeUrl(meta.url);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text="Fetched" variant="success" />
        <ExternalLink href={safeUrl} className="font-mono text-kodelet-dark/70 truncate max-w-md">
          {meta.url}
        </ExternalLink>
      </div>

      {savedPath && (
        <div className="text-xs text-kodelet-mid-gray">Saved: {savedPath}</div>
      )}

      {meta.content && (
        <>
          {!showContent ? (
            <button 
              onClick={() => setShowContent(true)}
              className="text-xs text-kodelet-blue hover:underline"
            >
              Show content
            </button>
          ) : (
            <div 
              className="bg-kodelet-light text-xs font-mono p-2 rounded border border-kodelet-light-gray max-h-64 overflow-auto"
            >
              <pre className="whitespace-pre-wrap">{meta.content}</pre>
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default WebFetchRenderer;