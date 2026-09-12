import { marked } from 'marked';
import type React from 'react';
import { useState } from 'react';
import type { ThinkingMetadata, ToolResult } from '../../types';
import { renderSafeMarkdown } from './reference';

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
    return renderSafeMarkdown(thought);
  };

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">internal</span>
        {!showThought && (
          <button type="button" onClick={() => setShowThought(true)} className="tool-action-link">
            Show thinking
          </button>
        )}
      </div>

      {showThought && (
        <div
          className="tool-detail-panel prose-enhanced max-h-64 overflow-y-auto text-sm italic"
          // biome-ignore lint/security/noDangerouslySetInnerHtml: formatThoughtContent uses renderSafeMarkdown to escape HTML and reject unsafe URLs.
          dangerouslySetInnerHTML={{ __html: formatThoughtContent(meta.thought) }}
        />
      )}
    </div>
  );
};

export default ThinkingRenderer;
