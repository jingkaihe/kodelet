import React, { useState } from 'react';
import { ToolResult, GlobMetadata, FileInfo } from '../../types';
import { StatusBadge } from './shared';

interface GlobRendererProps {
  toolResult: ToolResult;
}

const GlobRenderer: React.FC<GlobRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GlobMetadata;
  const [showAll, setShowAll] = useState(false);
  if (!meta) return null;

  const files = meta.files || [];
  const displayFiles = showAll || files.length <= 10 ? files : files.slice(0, 8);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 flex-wrap text-xs font-mono text-kodelet-dark/80">
        <code className="font-medium">{meta.pattern}</code>
        <StatusBadge 
          text={`${files.length} files`} 
          variant={meta.truncated ? 'warning' : 'success'} 
        />
        {meta.path && <span className="text-kodelet-mid-gray">in {meta.path}</span>}
      </div>

      {files.length > 0 ? (
        <div className="text-xs font-mono space-y-0.5">
          {displayFiles.map((file: FileInfo, index: number) => (
            <div key={index} className="text-kodelet-dark/80">{file.path || file.name}</div>
          ))}
          {files.length > 10 && !showAll && (
            <button 
              onClick={() => setShowAll(true)}
              className="text-kodelet-blue hover:underline"
            >
              +{files.length - 8} more files
            </button>
          )}
        </div>
      ) : (
        <div className="text-xs text-kodelet-mid-gray">No files found</div>
      )}
    </div>
  );
};

export default GlobRenderer;