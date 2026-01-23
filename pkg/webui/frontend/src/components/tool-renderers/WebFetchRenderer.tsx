import React from 'react';
import { ToolResult, WebFetchMetadata } from '../../types';
import { ToolCard, MetadataRow, Collapsible, CodeBlock, ExternalLink } from './shared';
import { escapeUrl } from './utils';

interface WebFetchRendererProps {
  toolResult: ToolResult;
}

const WebFetchRenderer: React.FC<WebFetchRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as WebFetchMetadata;
  if (!meta || !meta.url) return null;

  const hasPrompt = meta.prompt && meta.prompt.trim();
  const savedPath = meta.savedPath || meta.filePath;
  const safeUrl = escapeUrl(meta.url);

  const detectContentLanguage = (contentType?: string) => {
    if (!contentType) return '';
    if (contentType.includes('json')) return 'json';
    if (contentType.includes('xml')) return 'xml';
    if (contentType.includes('html')) return 'html';
    if (contentType.includes('css')) return 'css';
    if (contentType.includes('javascript')) return 'javascript';
    return '';
  };

  const renderWebContent = (meta: WebFetchMetadata) => {
    if (meta.contentType && meta.contentType.includes('image')) {
      return safeUrl !== '#' ? (
        <img src={safeUrl} alt="Fetched image" className="max-w-full h-auto rounded" />
      ) : (
        <div className="text-sm font-body text-kodelet-mid-gray">Invalid image URL</div>
      );
    }

    if (meta.content) {
      const language = detectContentLanguage(meta.contentType);
      return <CodeBlock code={meta.content} language={language} showLineNumbers={true} maxHeight={400} />;
    }

    return <div className="text-sm font-body text-kodelet-mid-gray">Content preview not available</div>;
  };

  return (
    <ToolCard
      title="Web Fetch"
      badge={{ text: 'Success', className: 'bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20' }}
      actions={
        safeUrl !== '#' ? (
          <ExternalLink href={safeUrl} className="btn btn-ghost btn-xs">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth="2"
                d="M10 6H6a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-4M14 4h6m0 0v6m0-6L10 14"
              />
            </svg>
          </ExternalLink>
        ) : undefined
      }
    >
      <div className="mb-3">
        <div className="space-y-1">
          <MetadataRow label="URL" value={meta.url} monospace />
          {meta.contentType && <MetadataRow label="Content Type" value={meta.contentType} />}
          {savedPath && <MetadataRow label="Saved to" value={savedPath} monospace />}
          {hasPrompt && <MetadataRow label="Extraction Prompt" value={meta.prompt} />}
        </div>
      </div>

      {meta.content && (
        <Collapsible
          title="Fetched Content"
          collapsed={true}
          badge={{ text: 'View Content', className: 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
        >
          {renderWebContent(meta)}
        </Collapsible>
      )}
    </ToolCard>
  );
};

export default WebFetchRenderer;