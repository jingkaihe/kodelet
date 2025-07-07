import React from 'react';
import { Conversation } from '../types';
import { formatDate, formatCost } from '../utils';

interface ConversationMetadataProps {
  conversation: Conversation;
}

const ConversationMetadata: React.FC<ConversationMetadataProps> = ({ conversation }) => {
  const hasUsage = conversation.usage && (
    conversation.usage.inputTokens ||
    conversation.usage.outputTokens ||
    conversation.usage.cacheReadInputTokens
  );

  const formatNumber = (num: number): string => {
    return num.toLocaleString('en-US');
  };

  const calculateContextUsage = (): string => {
    if (!conversation.usage || !conversation.usage.maxContextWindow || conversation.usage.maxContextWindow === 0) {
      return 'N/A';
    }
    const current = conversation.usage.currentContextWindow || 0;
    const max = conversation.usage.maxContextWindow;
    const percentage = Math.round((current / max) * 100);
    return `${percentage}%`;
  };

  return (
    <div className="card bg-base-200 shadow-xl mb-6">
      <div className="card-body">
        <h2 className="card-title mb-4">Conversation Details</h2>

        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {/* Basic Stats */}
          <div className="stat">
            <div className="stat-title">Messages</div>
            <div className="stat-value">{formatNumber(conversation.messageCount || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Model</div>
            <div className="stat-value">
              {conversation.modelType || 'Unknown'}
            </div>
          </div>
          <div className="stat">
            <div className="stat-title">Created</div>
            <div className="stat-value">
              {formatDate(conversation.createdAt)}
            </div>
          </div>
          <div className="stat">
            <div className="stat-title">Updated</div>
            <div className="stat-value">
              {formatDate(conversation.updatedAt)}
            </div>
          </div>

          {/* Usage Statistics */}
          {hasUsage && (
            <>
              <div className="stat">
                <div className="stat-title">Context Usage</div>
                <div className="stat-value">{calculateContextUsage()}</div>
                <div className="stat-desc">
                  {conversation.usage?.currentContextWindow ?
                    `${formatNumber(conversation.usage.currentContextWindow)} / ${formatNumber(conversation.usage?.maxContextWindow || 0)}` :
                    'Context info unavailable'
                  }
                </div>
              </div>
              <div className="stat">
                <div className="stat-title">Total Cost</div>
                <div className="stat-value">{conversation.usage ? formatCost(conversation.usage) : '$0.0000'}</div>
              </div>
              <div className="stat">
                <div className="stat-title">Input Tokens</div>
                <div className="stat-value">{formatNumber(conversation.usage?.inputTokens || 0)}</div>
              </div>
              <div className="stat">
                <div className="stat-title">Output Tokens</div>
                <div className="stat-value">{formatNumber(conversation.usage?.outputTokens || 0)}</div>
              </div>
              <div className="stat">
                <div className="stat-title">Cache Read Tokens</div>
                <div className="stat-value">{formatNumber(conversation.usage?.cacheReadInputTokens || 0)}</div>
              </div>
              {conversation.usage?.cacheCreationInputTokens && (
                <div className="stat">
                  <div className="stat-title">Cache Creation Tokens</div>
                  <div className="stat-value">{formatNumber(conversation.usage.cacheCreationInputTokens)}</div>
                </div>
              )}
            </>
          )}
        </div>

        {/* Additional Metadata */}
        {conversation.summary && (
          <div className="mt-4">
            <h3 className="font-semibold mb-2">Summary</h3>
            <p className="text-sm text-base-content/70 bg-base-100 p-3 rounded">
              {conversation.summary}
            </p>
          </div>
        )}
      </div>
    </div>
  );
};

export default ConversationMetadata;
