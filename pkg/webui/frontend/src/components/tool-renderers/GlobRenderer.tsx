import React from 'react';
import { ToolResult, GlobMetadata, FileInfo } from '../../types';
import {
  formatReferenceSize,
  ReferenceFileList,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  ReferenceToolNote,
  TOOL_ICONS,
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
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[
          {
            text: `${files.length} entries`,
            variant: meta.truncated ? 'warning' : 'success',
          },
        ]}
        subtitle={meta.pattern}
        title={`${TOOL_ICONS.glob_tool} File Discovery`}
      />

      <ReferenceToolKVGrid items={[{ label: 'Path', value: meta.path, monospace: true }]} />

      {files.length > 0 ? (
        <ReferenceFileList
          items={displayFiles.map((file: FileInfo) => {
            const path = file.path || file.name || '';
            const metaText = [formatReferenceSize(file.size), file.modified || file.modTime]
              .filter(Boolean)
              .join(' · ');
            return { path, meta: metaText || undefined };
          })}
        />
      ) : (
        <div className="text-xs text-kodelet-mid-gray">No files found</div>
      )}

      {files.length > 24 ? (
        <ReferenceToolNote text={`Showing first 24 of ${files.length} matched entries.`} />
      ) : null}
    </div>
  );
};

export default GlobRenderer;
