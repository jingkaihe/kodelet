import React from 'react';
import { ToolResult, ViewImageMetadata } from '../../types';
import { getMetadataAny } from './utils';
import { ReferenceToolKVGrid } from './reference';

interface ViewImageRendererProps {
  toolResult: ToolResult;
}

const ViewImageRenderer: React.FC<ViewImageRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ViewImageMetadata;
  if (!meta) return null;

  const imagePath = getMetadataAny(toolResult, ['path']) as string;
  const mimeType = getMetadataAny(toolResult, ['mimeType', 'mime_type']) as string;
  const dimensions =
    meta.imageSize?.width && meta.imageSize?.height
      ? `${meta.imageSize.width} x ${meta.imageSize.height}`
      : '';

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">image inspected</span>
        {mimeType ? <span className="quiet-tool-muted">{mimeType}</span> : null}
      </div>
      <div className="quiet-tool-path">{imagePath}</div>

      <ReferenceToolKVGrid
        items={[
          { label: 'Dimensions', value: dimensions, monospace: true },
          { label: 'Detail', value: meta.detail, monospace: true },
        ]}
      />
    </div>
  );
};

export default ViewImageRenderer;
