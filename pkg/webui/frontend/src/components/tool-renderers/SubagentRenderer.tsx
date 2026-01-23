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
      <div className="flex items-center gap-2 text-xs">
        <StatusBadge text="Delegated" variant="info" />
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
          <div>
            <div className="font-medium text-kodelet-mid-gray mb-1">Question:</div>
            <div
              className="bg-kodelet-blue/5 p-2 rounded border border-kodelet-blue/20 prose-enhanced text-sm"
              dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.question) }}
            />
          </div>
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