import React from 'react';
import { ToolResult, SubagentMetadata } from '../../types';
import { ToolCard } from './shared';
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
    // Configure marked for better code rendering
    marked.setOptions({
      breaks: true,
      gfm: true,
    });
    return marked.parse(text);
  };

  return (
    <ToolCard
      title="🤖 Sub-agent"
      badge={{ text: `${modelStrength} model`, className: 'badge-info' }}
    >
      <div className="space-y-4">
        <div>
          <div className="text-xs text-base-content/60 mb-2">
            <strong>Question:</strong>
          </div>
          <div className="bg-primary/10 p-3 rounded-lg border border-primary/20">
            <div 
              className="prose-enhanced subagent-response text-sm"
              dangerouslySetInnerHTML={{
                __html: formatMarkdown(meta.question)
              }}
            />
          </div>
        </div>

        {meta.response && (
          <div>
            <div className="text-xs text-base-content/60 mb-2">
              <strong>Response:</strong>
            </div>
            <div className="bg-base-200 p-4 rounded-lg border">
              <div 
                className="prose-enhanced subagent-response"
                dangerouslySetInnerHTML={{
                  __html: formatMarkdown(meta.response)
                }}
              />
            </div>
          </div>
        )}
      </div>
    </ToolCard>
  );
};

export default SubagentRenderer;