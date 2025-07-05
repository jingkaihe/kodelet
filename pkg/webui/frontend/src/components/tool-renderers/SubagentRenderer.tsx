import React from 'react';
import { ToolResult, SubagentMetadata } from '../../types';
import { ToolCard, escapeHtml } from './shared';
import { marked } from 'marked';

interface SubagentRendererProps {
  toolResult: ToolResult;
}

const SubagentRenderer: React.FC<SubagentRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as SubagentMetadata;
  if (!meta) return null;

  const modelStrength = meta.modelStrength || meta.model_strength || 'unknown';

  const formatMarkdown = (text: string): string => {
    if (!text) return '';
    return marked.parse(text);
  };

  return (
    <ToolCard
      title="🤖 Sub-agent"
      badge={{ text: `${modelStrength} model`, className: 'badge-info' }}
    >
      <div className="space-y-3">
        <div>
          <div className="text-xs text-base-content/60 mb-1">
            <strong>Question:</strong>
          </div>
          <div className="bg-blue-50 p-3 rounded text-sm">
            {escapeHtml(meta.question)}
          </div>
        </div>

        {meta.response && (
          <div>
            <div className="text-xs text-base-content/60 mb-1">
              <strong>Response:</strong>
            </div>
            <div 
              className="bg-green-50 p-3 rounded text-sm prose prose-sm max-w-none"
              dangerouslySetInnerHTML={{
                __html: formatMarkdown(meta.response)
              }}
            />
          </div>
        )}
      </div>
    </ToolCard>
  );
};

export default SubagentRenderer;