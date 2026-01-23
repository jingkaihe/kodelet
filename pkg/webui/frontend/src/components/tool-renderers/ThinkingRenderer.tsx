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
    <div className="bg-kodelet-blue/5 border border-kodelet-blue/20 rounded p-3">
      <div className="flex items-center gap-2 mb-3">
        <h4 className="font-heading font-semibold text-sm text-kodelet-blue">Thinking</h4>
        <div className="px-2 py-0.5 rounded text-xs font-heading font-medium bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20">
          Internal Process
        </div>
      </div>

      <div className="bg-kodelet-light-gray/30 p-3 rounded border border-kodelet-mid-gray/20">
        <div 
          className="prose-enhanced text-sm italic leading-relaxed text-kodelet-dark"
          dangerouslySetInnerHTML={{
            __html: formatThoughtContent(meta.thought)
          }}
        />
      </div>
    </div>
  );
};

export default ThinkingRenderer;