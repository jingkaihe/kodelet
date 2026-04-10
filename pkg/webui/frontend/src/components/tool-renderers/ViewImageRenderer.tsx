import React from 'react';
import { ToolResult, ViewImageMetadata } from '../../types';
import { getMetadataAny } from './utils';
import { ReferenceToolHeader, ReferenceToolKVGrid, TOOL_ICONS } from './reference';

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
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={mimeType ? [{ text: mimeType, variant: 'success' }] : []}
        subtitle={imagePath}
        title={`${TOOL_ICONS.view_image} View Image`}
      />

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
