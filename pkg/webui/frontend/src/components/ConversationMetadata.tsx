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
    <div className="card mb-4 animate-slide-up stagger-1">
      <div className="card-body p-4">
        <h2 className="font-heading text-lg font-semibold mb-3 text-kodelet-dark flex items-center gap-2">
          <span className="text-kodelet-orange">●</span> Statistics
        </h2>

        <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-4 gap-3">
          {/* Basic Stats */}
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Messages</div>
            <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(conversation.messageCount || 0)}</div>
          </div>
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Model</div>
            <div className="text-lg font-heading font-semibold text-kodelet-dark">
              {conversation.provider || 'Unknown'}
            </div>
          </div>
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Created</div>
            <div className="text-sm font-heading font-semibold text-kodelet-dark">
              {formatDate(conversation.createdAt)}
            </div>
          </div>
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Updated</div>
            <div className="text-sm font-heading font-semibold text-kodelet-dark">
              {formatDate(conversation.updatedAt)}
            </div>
          </div>

          {/* Usage Statistics */}
          {hasUsage && (
            <>
              <div className="space-y-0.5 border-l-2 border-kodelet-green pl-3">
                <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Context Usage</div>
                <div className="text-lg font-heading font-semibold text-kodelet-dark">{calculateContextUsage()}</div>
                <div className="text-xs font-body text-kodelet-mid-gray">
                  {conversation.usage?.currentContextWindow ?
                    `${formatNumber(conversation.usage.currentContextWindow)} / ${formatNumber(conversation.usage?.maxContextWindow || 0)}` :
                    'N/A'
                  }
                </div>
              </div>
              <div className="space-y-0.5">
                <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Total Cost</div>
                <div className="text-lg font-heading font-semibold text-kodelet-orange">{conversation.usage ? formatCost(conversation.usage) : '$0.0000'}</div>
              </div>
              <div className="space-y-0.5 border-l-2 border-kodelet-blue pl-3">
                <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Input Tokens</div>
                <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(conversation.usage?.inputTokens || 0)}</div>
              </div>
              <div className="space-y-0.5 border-l-2 border-kodelet-orange pl-3">
                <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Output Tokens</div>
                <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(conversation.usage?.outputTokens || 0)}</div>
              </div>
              <div className="space-y-0.5 border-l-2 border-kodelet-green pl-3">
                <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Cache Read Tokens</div>
                <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(conversation.usage?.cacheReadInputTokens || 0)}</div>
              </div>
              {conversation.usage?.cacheCreationInputTokens && (
                <div className="space-y-0.5 border-l-2 border-kodelet-blue pl-3">
                  <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Cache Creation Tokens</div>
                  <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(conversation.usage.cacheCreationInputTokens)}</div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
};

export default ConversationMetadata;
