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
      title="Image Recognition"
      badge={{ text: 'Analyzed', className: 'bg-kodelet-green/10 text-kodelet-green border border-kodelet-green/20' }}
    >
      <div className="mb-3">
        <div className="space-y-1">
          {imagePath && <MetadataRow label="Image" value={imagePath} monospace />}
          {meta.prompt && <MetadataRow label="Prompt" value={meta.prompt} />}
        </div>
      </div>

      {analysis && (
        <div className="bg-kodelet-light p-3 rounded border border-kodelet-mid-gray/20">
          <div className="text-sm font-body text-kodelet-dark">{escapeHtml(analysis)}</div>
        </div>
      )}
    </ToolCard>
  );
};

export default ImageRecognitionRenderer;