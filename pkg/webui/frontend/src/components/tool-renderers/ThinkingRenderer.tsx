import React from 'react';
import { ToolResult, ThinkingMetadata } from '../../types';
import { marked } from 'marked';

interface ThinkingRendererProps {
  toolResult: ToolResult;
}

const ThinkingRenderer: React.FC<ThinkingRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ThinkingMetadata;
  if (!meta) return null;

  const formatThoughtContent = (thought: string): string => {
    if (!thought) return '';
    // Configure marked for better code rendering
    marked.setOptions({
      breaks: true,
      gfm: true,
    });
    return marked.parse(thought);
  };

  return (
    <div className="card bg-secondary/10 border border-secondary/20">
      <div className="card-body">
        <div className="flex items-center gap-2 mb-3">
          <h4 className="font-semibold text-secondary">🧠 Thinking</h4>
          <div className="badge badge-secondary badge-sm">Internal Process</div>
        </div>

        <div className="bg-base-200 p-4 rounded-lg border">
          <div 
            className="prose-enhanced text-sm italic leading-relaxed"
            dangerouslySetInnerHTML={{
              __html: formatThoughtContent(meta.thought)
            }}
          />
        </div>
      </div>
    </div>
  );
};

export default ThinkingRenderer;