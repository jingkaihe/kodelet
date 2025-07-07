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

  const calculateTotalTokens = (): number => {
    if (!conversation.usage) return 0;
    return (conversation.usage.inputTokens || 0) + 
           (conversation.usage.outputTokens || 0) + 
           (conversation.usage.cacheCreationInputTokens || 0) +
           (conversation.usage.cacheReadInputTokens || 0);
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
            <div className="stat-value text-sm">
              {conversation.modelType || 'Unknown'}
            </div>
          </div>
          <div className="stat">
            <div className="stat-title">Created</div>
            <div className="stat-value text-sm">
              {formatDate(conversation.createdAt)}
            </div>
          </div>
          <div className="stat">
            <div className="stat-title">Updated</div>
            <div className="stat-value text-sm">
              {formatDate(conversation.updatedAt)}
            </div>
          </div>
          
          {/* Usage Statistics */}
          {hasUsage && (
            <>
              <div className="stat">
                <div className="stat-title">Total Tokens</div>
                <div className="stat-value">{formatNumber(calculateTotalTokens())}</div>
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