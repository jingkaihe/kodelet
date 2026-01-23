import React from 'react';
import { ConversationStats } from '../types';

interface StatsCardProps {
  stats: ConversationStats;
}

const StatsCard: React.FC<StatsCardProps> = ({ stats }) => {
  const formatNumber = (num: number): string => {
    return num.toLocaleString('en-US');
  };

  const formatCost = (cost: number): string => {
    return `$${cost.toFixed(4)}`;
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
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Total Conversations</div>
            <div className="text-xl font-heading font-bold text-kodelet-dark">{formatNumber(stats.totalConversations || 0)}</div>
          </div>
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Total Messages</div>
            <div className="text-xl font-heading font-bold text-kodelet-dark">{formatNumber(stats.totalMessages || 0)}</div>
          </div>
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Total Tokens</div>
            <div className="text-xl font-heading font-bold text-kodelet-dark">{formatNumber(stats.totalTokens || 0)}</div>
          </div>
          <div className="space-y-0.5">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Total Cost</div>
            <div className="text-xl font-heading font-bold text-kodelet-orange">{formatCost(stats.totalCost || 0)}</div>
          </div>

          {/* Token Breakdown */}
          <div className="space-y-0.5 border-l-2 border-kodelet-blue pl-3">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Input Tokens</div>
            <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(stats.inputTokens || 0)}</div>
            <div className="text-xs font-body text-kodelet-mid-gray">Cost: {formatCost(stats.inputCost || 0)}</div>
          </div>
          <div className="space-y-0.5 border-l-2 border-kodelet-orange pl-3">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Output Tokens</div>
            <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(stats.outputTokens || 0)}</div>
            <div className="text-xs font-body text-kodelet-mid-gray">Cost: {formatCost(stats.outputCost || 0)}</div>
          </div>
          <div className="space-y-0.5 border-l-2 border-kodelet-green pl-3">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Cache Read Tokens</div>
            <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(stats.cacheReadTokens || 0)}</div>
            <div className="text-xs font-body text-kodelet-mid-gray">Cost: {formatCost(stats.cacheReadCost || 0)}</div>
          </div>
          <div className="space-y-0.5 border-l-2 border-kodelet-blue pl-3">
            <div className="text-xs font-body text-kodelet-mid-gray uppercase tracking-wide">Cache Write Tokens</div>
            <div className="text-lg font-heading font-semibold text-kodelet-dark">{formatNumber(stats.cacheWriteTokens || 0)}</div>
            <div className="text-xs font-body text-kodelet-mid-gray">Cost: {formatCost(stats.cacheWriteCost || 0)}</div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default StatsCard;