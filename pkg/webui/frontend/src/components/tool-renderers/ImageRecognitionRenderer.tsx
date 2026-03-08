import React from 'react';
import { ToolResult, ImageRecognitionMetadata } from '../../types';
import { getMetadataAny } from './utils';
import {
  renderMarkdown,
  ReferenceToolHeader,
  ReferenceToolKVGrid,
  TOOL_ICONS,
} from './reference';

interface ImageRecognitionRendererProps {
  toolResult: ToolResult;
}

const ImageRecognitionRenderer: React.FC<ImageRecognitionRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ImageRecognitionMetadata;
  if (!meta) return null;

  const imagePath = getMetadataAny(toolResult, ['imagePath', 'image_path', 'path']) as string;
  const analysis = meta.analysis || meta.result;

  return (
    <div className="space-y-2">
      <ReferenceToolHeader
        badges={[{ text: 'image', variant: 'success' }]}
        subtitle={imagePath}
        title={`${TOOL_ICONS.image_recognition} Image Analysis`}
      />

      <ReferenceToolKVGrid
        items={[{ label: 'Prompt', value: meta.prompt }]}
      />

      {analysis ? (
        <div
          className="prose prose-sm max-w-none text-kodelet-dark"
          dangerouslySetInnerHTML={{ __html: renderMarkdown(analysis) }}
        />
      ) : null}
    </div>
  );
};

export default ImageRecognitionRenderer;
