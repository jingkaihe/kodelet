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

  return (
    <div className="card bg-base-200 shadow-xl mb-6">
      <div className="card-body">
        <h2 className="card-title mb-4">Conversation Details</h2>
        
        {/* Basic Stats */}
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          <div className="stat">
            <div className="stat-title">Messages</div>
            <div className="stat-value text-primary">{conversation.messageCount || 0}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Model</div>
            <div className="stat-value text-secondary text-sm">
              {conversation.modelType || 'Unknown'}
            </div>
          </div>
          <div className="stat">
            <div className="stat-title">Created</div>
            <div className="stat-desc">
              {formatDate(conversation.createdAt)}
            </div>
          </div>
          <div className="stat">
            <div className="stat-title">Updated</div>
            <div className="stat-desc">
              {formatDate(conversation.updatedAt)}
            </div>
          </div>
        </div>

        {/* Usage Statistics */}
        {hasUsage && (
          <div className="mt-4">
            <h3 className="font-semibold mb-2">Token Usage</h3>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2 text-sm">
              <div className="bg-base-100 p-2 rounded">
                <div className="text-xs text-base-content/60">Input</div>
                <div className="font-mono">
                  {conversation.usage?.inputTokens?.toLocaleString() || 0}
                </div>
              </div>
              <div className="bg-base-100 p-2 rounded">
                <div className="text-xs text-base-content/60">Output</div>
                <div className="font-mono">
                  {conversation.usage?.outputTokens?.toLocaleString() || 0}
                </div>
              </div>
              <div className="bg-base-100 p-2 rounded">
                <div className="text-xs text-base-content/60">Cache Read</div>
                <div className="font-mono">
                  {conversation.usage?.cacheReadInputTokens?.toLocaleString() || 0}
                </div>
              </div>
              <div className="bg-base-100 p-2 rounded">
                <div className="text-xs text-base-content/60">Total Cost</div>
                <div className="font-mono">{conversation.usage ? formatCost(conversation.usage) : '$0.0000'}</div>
              </div>
            </div>
          </div>
        )}

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