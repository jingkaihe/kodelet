import React from 'react';
import { ToolResult, BrowserMetadata } from '../../types';
import { ToolCard, MetadataRow, ExternalLink } from './shared';
import { getMetadataAny, escapeUrl, isImageFile } from './utils';

interface BrowserRendererProps {
  toolResult: ToolResult;
}

const BrowserRenderer: React.FC<BrowserRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as BrowserMetadata;
  if (!meta) return null;

  const isScreenshot = toolResult.toolName === 'browser_screenshot';
  const filePath = getMetadataAny(toolResult, ['filePath', 'file_path', 'path']) as string;
  const safeUrl = escapeUrl(meta.url);

  if (isScreenshot) {
    const dimensions = meta.dimensions || meta.size;

    return (
      <ToolCard
        title="📸 Browser Screenshot"
        badge={{ text: 'Captured', className: 'badge-success' }}
      >
        <div className="text-xs text-base-content/60 mb-3 font-mono">
          <div className="space-y-1">
            {filePath && <MetadataRow label="File" value={filePath} monospace />}
            {dimensions && <MetadataRow label="Dimensions" value={dimensions} />}
          </div>
        </div>

        {filePath && isImageFile(filePath) && (
          <div className="mt-3">
            <img
              src={escapeUrl('file://' + filePath)}
              alt="Screenshot"
              className="max-w-full h-auto rounded border"
              onError={(e) => {
                e.currentTarget.style.display = 'none';
                if (e.currentTarget.nextElementSibling) {
                  (e.currentTarget.nextElementSibling as HTMLElement).style.display = 'block';
                }
              }}
            />
            <div style={{ display: 'none' }} className="text-sm text-base-content/60">
              Unable to load screenshot
            </div>
          </div>
        )}
      </ToolCard>
    );
  }

  // Browser navigation
  const title = meta.title || meta.pageTitle;

  return (
    <ToolCard
      title="🌐 Browser Navigation"
      badge={{ text: 'Success', className: 'badge-success' }}
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
      <div className="text-xs text-base-content/60 font-mono">
        <div className="space-y-1">
          <MetadataRow label="URL" value={meta.url} monospace />
          {title && <MetadataRow label="Title" value={title} />}
        </div>
      </div>
    </ToolCard>
  );
};

export default BrowserRenderer;