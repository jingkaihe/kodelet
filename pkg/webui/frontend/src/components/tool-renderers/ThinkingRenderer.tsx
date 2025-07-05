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
    return marked.parse(thought);
  };

  return (
    <div className="card bg-blue-50 border border-blue-200">
      <div className="card-body">
        <div className="flex items-center gap-2 mb-3">
          <h4 className="font-semibold text-blue-700">🧠 Thinking</h4>
          <div className="badge badge-info badge-sm">Internal Process</div>
        </div>

        <div className="bg-white p-4 rounded-lg border border-blue-100">
          <div 
            className="text-sm text-gray-700 italic leading-relaxed prose prose-sm max-w-none"
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