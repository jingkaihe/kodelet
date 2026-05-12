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
          {meta.question && (
            <div>
              <div className="quiet-tool-section-title">question</div>
              <div
                className="tool-compact-markdown subagent-response"
                dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.question) }}
              />
            </div>
          )}
          {meta.response && (
            <div
              className="tool-compact-markdown subagent-response"
              dangerouslySetInnerHTML={{ __html: formatMarkdown(meta.response) }}
            />
          )}
        </div>
      )}
    </div>
  );
};

export default SubagentRenderer;
