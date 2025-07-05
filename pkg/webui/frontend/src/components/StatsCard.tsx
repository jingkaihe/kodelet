import React from 'react';
import { ConversationStats } from '../types';

interface StatsCardProps {
  stats: ConversationStats;
}

const StatsCard: React.FC<StatsCardProps> = ({ stats }) => {
  const formatDateRange = (): string => {
    if (!stats.oldestConversation || !stats.newestConversation) {
      return 'N/A';
    }

    const oldest = new Date(stats.oldestConversation).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short'
    });
    const newest = new Date(stats.newestConversation).toLocaleDateString('en-US', {
      year: 'numeric',
      month: 'short'
    });

    return oldest === newest ? oldest : `${oldest} - ${newest}`;
  };

  return (
    <div className="card bg-base-200 shadow-xl mb-6">
      <div className="card-body">
        <h2 className="card-title mb-4">Statistics</h2>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="stat">
            <div className="stat-title">Total Conversations</div>
            <div className="stat-value text-primary">{stats.totalConversations || 0}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Total Messages</div>
            <div className="stat-value text-secondary">{stats.totalMessages || 0}</div>
          </div>
          <div className="stat">
            <div className="stat-title">Date Range</div>
            <div className="stat-desc">{formatDateRange()}</div>
          </div>
        </div>
      </div>
    </div>
  );
};

export default StatsCard;