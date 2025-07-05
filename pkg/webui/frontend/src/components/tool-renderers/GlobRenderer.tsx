import React from 'react';
import { ToolResult, GlobMetadata, FileInfo } from '../../types';
import { ToolCard, MetadataRow, Collapsible, formatFileSize, getFileIcon } from './shared';

interface GlobRendererProps {
  toolResult: ToolResult;
}

const GlobRenderer: React.FC<GlobRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GlobMetadata;
  if (!meta) return null;

  const files = meta.files || [];
  const totalSize = files.reduce((sum, file) => sum + (file.size || 0), 0);

  const badges = [];
  badges.push({ text: `${files.length} files`, className: 'badge-info' });
  if (meta.truncated) badges.push({ text: 'Truncated', className: 'badge-warning' });

  const renderFileList = (files: FileInfo[]) => {
    const fileContent = files.map((file, index) => {
      const icon = getFileIcon(file.path || file.name || '');
      const sizeText = file.size ? formatFileSize(file.size) : '';
      const modTime = file.modTime || file.modified ?
        new Date(file.modTime || file.modified!).toLocaleDateString() : '';

      return (
        <div key={index} className="flex items-center justify-between py-2 hover:bg-base-100 rounded px-2">
          <div className="flex items-center gap-2">
            <span className="text-lg" aria-hidden="true">{icon}</span>
            <span className="font-mono text-sm">{file.path || file.name}</span>
          </div>
          <div className="flex items-center gap-4 text-xs text-base-content/60">
            {sizeText && <span>{sizeText}</span>}
            {modTime && <span>{modTime}</span>}
          </div>
        </div>
      );
    });

    return (
      <Collapsible
        title="Files"
        collapsed={files.length > 10}
        badge={{ text: `${files.length} files`, className: 'badge-info' }}
      >
        <div>{fileContent}</div>
      </Collapsible>
    );
  };

  return (
    <ToolCard
      title="📁 File Listing"
      badge={badges[0]}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Pattern" value={meta.pattern} monospace />
          {meta.path && <MetadataRow label="Path" value={meta.path} monospace />}
          {totalSize > 0 && <MetadataRow label="Total Size" value={formatFileSize(totalSize)} />}
        </div>
      </div>

      {files.length > 0 ? (
        renderFileList(files)
      ) : (
        <div className="text-sm text-base-content/60">No files found</div>
      )}
    </ToolCard>
  );
};

export default GlobRenderer;