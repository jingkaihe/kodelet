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
          <span className="inline-flex items-center rounded-full border border-kodelet-orange/20 bg-kodelet-orange/10 px-2 py-1 text-[0.68rem] font-heading font-semibold uppercase tracking-[0.12em] text-kodelet-orange">
            {meta.workflow}
          </span>
        )}
        {meta.cwd && (
          <span className="inline-flex max-w-48 items-center truncate rounded-full border border-kodelet-green/20 bg-kodelet-green/10 px-2 py-1 font-mono text-[10px] text-kodelet-green" title={meta.cwd}>
            {meta.cwd}
          </span>
        )}
        {!showDetails && (
          <button
            onClick={() => setShowDetails(true)}
            className="tool-action-link"
          >
            Show details
          </button>
        )}
      </div>

      {showDetails && (
        <div className="space-y-3 text-xs">
          {meta.workflow && (
            <div className="flex items-center gap-2">
              <span className="tool-meta-label">Workflow:</span>
              <code className="rounded-md border border-kodelet-orange/20 bg-kodelet-orange/10 px-2 py-1 text-xs text-kodelet-orange">
                {meta.workflow}
              </code>
            </div>
          )}
          {meta.cwd && (
            <div className="flex items-center gap-2">
              <span className="tool-meta-label">Directory:</span>
              <code className="truncate rounded-md border border-black/8 bg-kodelet-light-gray/40 px-2 py-1 text-xs font-mono max-w-md" title={meta.cwd}>
                {meta.cwd}
              </code>
            </div>
          )}
          {meta.question && (
            <div>
              <div className="tool-meta-label mb-2">Question:</div>
              <div
                className="tool-detail-panel prose-enhanced subagent-response text-sm"
                dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.question) }}
              />
            </div>
          )}
          {meta.response && (
            <div>
              <div className="tool-meta-label mb-2">Response:</div>
              <div
                className="tool-detail-panel prose-enhanced subagent-response max-h-64 overflow-y-auto text-sm"
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
