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
    <div className="card bg-base-200 shadow-xl mb-6">
      <div className="card-body">
        <h2 className="card-title mb-4">Statistics</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {/* Basic Stats */}
          <div className="stat">
            <div className="stat-title">Total Conversations</div>
            <div className="stat-value">{formatNumber(stats.totalConversations || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Total Messages</div>
            <div className="stat-value">{formatNumber(stats.totalMessages || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Total Tokens</div>
            <div className="stat-value">{formatNumber(stats.totalTokens || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Total Cost</div>
            <div className="stat-value">{formatCost(stats.totalCost || 0)}</div>
          </div>
          
          {/* Token Breakdown */}
          <div className="stat">
            <div className="stat-title">Input Tokens</div>
            <div className="stat-value">{formatNumber(stats.inputTokens || 0)}</div>
            <div className="stat-desc">Cost: {formatCost(stats.inputCost || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Output Tokens</div>
            <div className="stat-value">{formatNumber(stats.outputTokens || 0)}</div>
            <div className="stat-desc">Cost: {formatCost(stats.outputCost || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Cache Read Tokens</div>
            <div className="stat-value">{formatNumber(stats.cacheReadTokens || 0)}</div>
            <div className="stat-desc">Cost: {formatCost(stats.cacheReadCost || 0)}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Cache Write Tokens</div>
            <div className="stat-value">{formatNumber(stats.cacheWriteTokens || 0)}</div>
            <div className="stat-desc">Cost: {formatCost(stats.cacheWriteCost || 0)}</div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default StatsCard;