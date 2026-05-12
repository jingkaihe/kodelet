import React from 'react';
import { ReadConversationMetadata, ToolResult } from '../../types';
import { renderMarkdown } from './reference';

interface ReadConversationRendererProps {
  toolResult: ToolResult;
}

const ReadConversationRenderer: React.FC<ReadConversationRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ReadConversationMetadata;
  if (!meta) return null;

  const conversationID = meta.conversationID || meta.conversationId;

  return (
    <div className="quiet-tool-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">conversation loaded</span>
        {conversationID ? <span className="quiet-tool-muted mono">{conversationID}</span> : null}
      </div>

      {meta.goal ? (
        <div className="quiet-tool-keyline">
          <span className="quiet-tool-key">Goal</span>
          <span>{meta.goal}</span>
        </div>
      ) : null}

      {meta.content?.trim() ? (
        <div
          className="tool-detail-panel prose-enhanced read-conversation-content text-sm"
          dangerouslySetInnerHTML={{ __html: renderMarkdown(meta.content) }}
        />
      ) : (
        <div className="quiet-tool-empty">No conversation content returned.</div>
      )}
    </div>
  );
};

export default ReadConversationRenderer;
