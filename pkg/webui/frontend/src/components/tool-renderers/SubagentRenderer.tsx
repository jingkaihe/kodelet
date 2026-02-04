import React, { useState } from 'react';
import { ToolResult, SubagentMetadata } from '../../types';
import { StatusBadge } from './shared';
import { marked } from 'marked';

interface SubagentRendererProps {
  toolResult: ToolResult;
}

const SubagentRenderer: React.FC<SubagentRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as SubagentMetadata;
  const [showDetails, setShowDetails] = useState(false);
  if (!meta) return null;

  const formatMarkdown = (text: string): string => {
    if (!text) return '';
    marked.setOptions({ breaks: true, gfm: true });
    return marked.parse(text);
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs flex-wrap">
        <StatusBadge text="Delegated" variant="info" />
        {meta.workflow && (
          <span className="px-2 py-0.5 bg-kodelet-orange/10 text-kodelet-orange rounded-full font-medium border border-kodelet-orange/20">
            {meta.workflow}
          </span>
        )}
        {meta.cwd && (
          <span className="px-2 py-0.5 bg-kodelet-green/10 text-kodelet-green rounded-full font-mono text-[10px] border border-kodelet-green/20 truncate max-w-48" title={meta.cwd}>
            {meta.cwd}
          </span>
        )}
        {!showDetails && (
          <button
            onClick={() => setShowDetails(true)}
            className="text-kodelet-blue hover:underline"
          >
            Show details
          </button>
        )}
      </div>

      {showDetails && (
        <div className="space-y-2 text-xs">
          {meta.workflow && (
            <div className="flex items-center gap-2">
              <span className="font-medium text-kodelet-mid-gray">Workflow:</span>
              <code className="px-1.5 py-0.5 bg-kodelet-orange/10 text-kodelet-orange rounded text-xs">
                {meta.workflow}
              </code>
            </div>
          )}
          {meta.cwd && (
            <div className="flex items-center gap-2">
              <span className="font-medium text-kodelet-mid-gray">Directory:</span>
              <code className="px-1.5 py-0.5 bg-kodelet-light-gray/50 rounded text-xs font-mono truncate max-w-md" title={meta.cwd}>
                {meta.cwd}
              </code>
            </div>
          )}
          {meta.question && (
            <div>
              <div className="font-medium text-kodelet-mid-gray mb-1">Question:</div>
              <div
                className="bg-kodelet-blue/5 p-2 rounded border border-kodelet-blue/20 prose-enhanced text-sm"
                dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.question) }}
              />
            </div>
          )}
          {meta.response && (
            <div>
              <div className="font-medium text-kodelet-mid-gray mb-1">Response:</div>
              <div
                className="bg-kodelet-light-gray/30 p-2 rounded border border-kodelet-mid-gray/20 prose-enhanced text-sm max-h-64 overflow-y-auto"
                dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.response) }}
              />
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default SubagentRenderer;