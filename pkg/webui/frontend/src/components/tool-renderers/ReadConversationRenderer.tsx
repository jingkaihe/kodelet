import React from 'react';
import { ReadConversationMetadata, ToolResult } from '../../types';
import { renderMarkdown } from './reference';

interface ReadConversationRendererProps {
  toolResult: ToolResult;
}

const GENERATED_GOAL_PREFIXES = [
  'summarize what this saved conversation contains',
  'summarise what this saved conversation contains',
];

const normalizeWhitespace = (value: string): string => value.replace(/\s+/g, ' ').trim();

const getVisibleGoal = (goal?: string): string => {
  if (!goal) {
    return '';
  }

  const normalizedGoal = normalizeWhitespace(goal);
  if (!normalizedGoal) {
    return '';
  }

  if (GENERATED_GOAL_PREFIXES.some((prefix) => normalizedGoal.toLowerCase().startsWith(prefix))) {
    return '';
  }

  return normalizedGoal.length > 180 ? `${normalizedGoal.slice(0, 177)}…` : normalizedGoal;
};

const stripGeneratedHeading = (content?: string): string => {
  if (!content) {
    return '';
  }

  return content
    .trim()
    .replace(/^#{1,3}\s+(saved conversation summary|summary)\s*\n+/i, '')
    .trim();
};

const ReadConversationRenderer: React.FC<ReadConversationRendererProps> = ({ toolResult }) => {
  const meta = toolResult.metadata as ReadConversationMetadata;
  if (!meta) return null;

  const conversationID = meta.conversationID || meta.conversationId;
  const visibleGoal = getVisibleGoal(meta.goal);
  const content = stripGeneratedHeading(meta.content);

  return (
    <div className="quiet-tool-detail read-conversation-detail">
      <div className="quiet-tool-line">
        <span className="quiet-tool-emphasis">conversation loaded</span>
        {conversationID ? <span className="quiet-tool-muted mono">{conversationID}</span> : null}
      </div>

      {visibleGoal ? (
        <div className="read-conversation-goal">
          <span>goal</span>
          <span>{visibleGoal}</span>
        </div>
      ) : null}

      {content ? (
        <div
          className="tool-compact-markdown read-conversation-summary"
          dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }}
        />
      ) : (
        <div className="quiet-tool-empty">No conversation summary returned.</div>
      )}
    </div>
  );
};

export default ReadConversationRenderer;
