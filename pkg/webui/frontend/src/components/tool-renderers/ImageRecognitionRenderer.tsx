import React, { useState } from 'react';
import { ToolResult, ImageRecognitionMetadata } from '../../types';
import { StatusBadge } from './shared';
import { getMetadataAny } from './utils';

interface ImageRecognitionRendererProps {
  toolResult: ToolResult;
}

const ImageRecognitionRenderer: React.FC<ImageRecognitionRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ImageRecognitionMetadata;
  const [showAnalysis, setShowAnalysis] = useState(false);
  if (!meta) return null;

  const imagePath = getMetadataAny(toolResult, ['imagePath', 'image_path', 'path']) as string;
  const analysis = meta.analysis || meta.result;

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text="Analyzed" variant="success" />
        {imagePath && <span className="font-mono text-kodelet-dark/70">{imagePath}</span>}
      </div>

      {meta.prompt && (
        <div className="text-xs text-kodelet-mid-gray">Prompt: {meta.prompt}</div>
      )}

      {analysis && (
        <>
          {!showAnalysis ? (
            <button 
              onClick={() => setShowAnalysis(true)}
              className="text-xs text-kodelet-blue hover:underline"
            >
              Show analysis
            </button>
          ) : (
            <div className="bg-kodelet-light p-2 rounded border border-kodelet-mid-gray/20 text-sm max-h-64 overflow-y-auto">
              {analysis}
            </div>
          )}
        </>
      )}
    </div>
  );
};

export default ImageRecognitionRenderer;