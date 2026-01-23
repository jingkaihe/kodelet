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
      title="Sub-agent"
      badge={{ text: 'Delegated', className: 'bg-kodelet-blue/10 text-kodelet-blue border border-kodelet-blue/20' }}
    >
      <div className="space-y-3">
        <div>
          <div className="text-xs font-heading font-medium text-kodelet-mid-gray mb-2">
            Question:
          </div>
          <div className="bg-kodelet-blue/5 p-3 rounded border border-kodelet-blue/20">
            <div 
              className="prose-enhanced subagent-response text-sm text-kodelet-dark"
              dangerouslySetInnerHTML={{
                __html: formatMarkdown(meta.question)
              }}
            />
          </div>
        </div>

        {meta.response && (
          <div>
            <div className="text-xs font-heading font-medium text-kodelet-mid-gray mb-2">
              Response:
            </div>
            <div className="bg-kodelet-light-gray/30 p-3 rounded border border-kodelet-mid-gray/20">
              <div 
                className="prose-enhanced subagent-response text-kodelet-dark"
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