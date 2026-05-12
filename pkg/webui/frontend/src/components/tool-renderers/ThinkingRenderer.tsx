import React, { useState } from 'react';
import { ToolResult, ThinkingMetadata } from '../../types';
import { marked } from 'marked';

interface ThinkingRendererProps {
  toolResult: ToolResult;
}

const ThinkingRenderer: React.FC<ThinkingRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ThinkingMetadata;
  const [showThought, setShowThought] = useState(false);
  if (!meta) return null;

  const formatThoughtContent = (thought: string): string => {
    if (!thought) return '';
    marked.setOptions({ breaks: true, gfm: true });
    return marked.parse(thought);
  };

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">internal</span>
        {!showThought && (
          <button
            onClick={() => setShowThought(true)}
            className="tool-action-link"
          >
            Show thinking
          </button>
        )}
      </div>

      {showThought && (
        <div
          className="tool-detail-panel prose-enhanced max-h-64 overflow-y-auto text-sm italic"
          dangerouslySetInnerHTML={{ __html: formatThoughtContent(meta.thought) }}
        />
      )}
    </div>
  );
};

export default ThinkingRenderer;
