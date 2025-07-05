import React from 'react';
import { ToolResult, ImageRecognitionMetadata } from '../../types';
import { ToolCard, MetadataRow } from './shared';
import { getMetadataAny, escapeHtml } from './utils';

interface ImageRecognitionRendererProps {
  toolResult: ToolResult;
}

const ImageRecognitionRenderer: React.FC<ImageRecognitionRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ImageRecognitionMetadata;
  if (!meta) return null;

  const imagePath = getMetadataAny(toolResult, ['imagePath', 'image_path', 'path']) as string;
  const analysis = meta.analysis || meta.result;

  return (
    <ToolCard
      title="👁️ Image Recognition"
      badge={{ text: 'Analyzed', className: 'badge-success' }}
    >
      <div className="text-xs text-base-content/60 mb-3 font-mono">
        <div className="space-y-1">
          {imagePath && <MetadataRow label="Image" value={imagePath} monospace />}
          {meta.prompt && <MetadataRow label="Prompt" value={meta.prompt} />}
        </div>
      </div>

      {analysis && (
        <div className="bg-base-100 p-3 rounded">
          <div className="text-sm">{escapeHtml(analysis)}</div>
        </div>
      )}
    </ToolCard>
  );
};

export default ImageRecognitionRenderer;