import React, { useState } from 'react';
import { ToolResult, SubagentMetadata } from '../../types';
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
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">delegated</span>
        {meta.workflow && (
          <span className="quiet-tool-muted">{meta.workflow}</span>
        )}
        {meta.cwd && (
          <span className="quiet-tool-muted mono" title={meta.cwd}>
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
        <div className="quiet-tool-sections">
          {meta.workflow && (
            <div className="quiet-tool-keyline">
              <span className="quiet-tool-key">Workflow</span>
              <code>{meta.workflow}</code>
            </div>
          )}
          {meta.cwd && (
            <div className="quiet-tool-keyline">
              <span className="quiet-tool-key">Directory</span>
              <code title={meta.cwd}>{meta.cwd}</code>
            </div>
          )}
          {meta.question && (
            <div>
              <div className="quiet-tool-section-title">Question</div>
              <div
                className="tool-detail-panel prose-enhanced subagent-response text-sm"
                dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.question) }}
              />
            </div>
          )}
          {meta.response && (
            <div>
              <div className="quiet-tool-section-title">Response</div>
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
