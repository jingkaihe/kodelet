import React from 'react';
import { ToolResult, GlobMetadata, FileInfo } from '../../types';
import { ToolCard, MetadataRow, Collapsible } from './shared';
import { formatFileSize } from './utils';

interface GlobRendererProps {
  toolResult: ToolResult;
}

const GlobRenderer: React.FC<GlobRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GlobMetadata;
  if (!meta) return null;

  const files = meta.files || [];
  const totalSize = files.reduce((sum, file) => sum + (file.size || 0), 0);

  const getBadge = () => {
    if (meta.truncated) {
      return { 
        text: 'Truncated', 
        className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-orange/10 text-kodelet-orange border border-kodelet-orange/20' 
      };
    }
    return { 
      text: `${files.length} files`, 
      className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20' 
    };
  };

  const renderFileList = (files: FileInfo[]) => {
    const fileContent = files.map((file, index) => {
      const sizeText = file.size ? formatFileSize(file.size) : '';
      const modTime = file.modTime || file.modified ?
        new Date(file.modTime || file.modified!).toLocaleDateString() : '';

      return (
        <div key={index} className="flex items-center justify-between py-2 hover:bg-kodelet-light-gray/30 rounded px-2">
          <div className="flex items-center gap-2">
            <span className="font-mono text-sm text-kodelet-dark">{file.path || file.name}</span>
          </div>
          <div className="flex items-center gap-4 text-xs text-kodelet-mid-gray">
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
        badge={{ text: `${files.length} files`, className: 'px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
      >
        <div>{fileContent}</div>
      </Collapsible>
    );
  };

  return (
    <ToolCard
      title="File Listing"
      badge={getBadge()}
    >
      <div className="text-xs text-kodelet-dark/60 mb-3 font-mono">
        <div className="flex items-center gap-4 flex-wrap">
          <MetadataRow label="Pattern" value={meta.pattern} monospace />
          {meta.path && <MetadataRow label="Path" value={meta.path} monospace />}
          {totalSize > 0 && <MetadataRow label="Total Size" value={formatFileSize(totalSize)} />}
        </div>
      </div>

      {files.length > 0 ? (
        renderFileList(files)
      ) : (
        <div className="text-sm text-kodelet-dark/50 font-body">No files found</div>
      )}
    </ToolCard>
  );
};

export default GlobRenderer;