import React from 'react';
import { ToolResult, GlobMetadata, FileInfo } from '../../types';
import {
  formatReferenceSize,
  ReferenceFileList,
  ReferenceToolKVGrid,
  ReferenceToolNote,
} from './reference';

interface GlobRendererProps {
  toolResult: ToolResult;
}

const GlobRenderer: React.FC<GlobRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as GlobMetadata;
  if (!meta) return null;

  const files = meta.files || [];
  const displayFiles = files.slice(0, 24);

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">{files.length} entries</span>
        {meta.truncated ? <span className="quiet-tool-warning">truncated</span> : null}
      </div>
      <div className="quiet-tool-path">{meta.pattern}</div>

      <ReferenceToolKVGrid items={[{ label: 'Path', value: meta.path, monospace: true }]} />

      {files.length > 0 ? (
        <ReferenceFileList
          items={displayFiles.map((file: FileInfo) => {
            const path = file.path || file.name || '';
            const metaText = [file.type, formatReferenceSize(file.size), file.language]
              .filter(Boolean)
              .join(' · ');
            return { path, meta: metaText || undefined };
          })}
        />
      ) : (
        <div className="quiet-tool-empty">No files found</div>
      )}

      {files.length > 24 ? (
        <ReferenceToolNote text={`Showing first 24 of ${files.length} matched entries.`} />
      ) : null}
    </div>
  );
};

export default GlobRenderer;
