import React from 'react';
import { useConversations } from '../hooks/useConversations';
import ConversationList from '../components/ConversationList';
import SearchAndFilters from '../components/SearchAndFilters';
import StatsCard from '../components/StatsCard';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorAlert from '../components/ErrorAlert';
import EmptyState from '../components/EmptyState';
import { showToast } from '../utils';

const ConversationListPage: React.FC = () => {
  const {
    conversations,
    stats,
    loading,
    error,
    hasMore,
    filters,
    setFilters,
    loadMore,
    deleteConversation,
    refresh,
  } = useConversations();

  const handleDeleteConversation = async (conversationId: string) => {
    if (!confirm('Are you sure you want to delete this conversation?')) {
      return;
    }

    try {
      await deleteConversation(conversationId);
      showToast('Conversation deleted successfully', 'success');
    } catch (err) {
      showToast(`Failed to delete conversation: ${err instanceof Error ? err.message : 'Unknown error'}`, 'error');
    }
  };

  const handleSearch = (searchTerm: string) => {
    setFilters({ searchTerm, offset: 0 });
  };

  const handleClearFilters = () => {
    setFilters({
      searchTerm: '',
      sortBy: 'updated',
      sortOrder: 'desc',
      limit: 25,
      offset: 0,
    });
  };

  return (
    <div className="container mx-auto px-4 py-8">
      {/* Header */}
      <div className="mb-8">
        <h1 className="text-4xl font-bold text-base-content mb-2">Conversations</h1>
        <p className="text-base-content/70">Browse and search your conversation history</p>
      </div>

      {/* Search and Filters */}
      <SearchAndFilters
        filters={filters}
        onFiltersChange={setFilters}
        onSearch={handleSearch}
        onClearFilters={handleClearFilters}
      />

      {/* Statistics Card */}
      {stats && <StatsCard stats={stats} />}

      {/* Error State */}
      {error && <ErrorAlert message={error} onRetry={refresh} />}

      {/* Loading State */}
      {loading && conversations.length === 0 && <LoadingSpinner message="Loading conversations..." />}

      {/* Empty State */}
      {!loading && conversations.length === 0 && !error && (
        <EmptyState
          icon="💬"
          title="No conversations found"
          description="Try adjusting your search criteria or filters"
        />
      )}

      {/* Conversation List */}
      {conversations.length > 0 && (
        <ConversationList
          conversations={conversations}
          loading={loading}
          hasMore={hasMore}
          onLoadMore={loadMore}
          onDelete={handleDeleteConversation}
        />
      )}
    </div>
  );
};

export default ConversationListPage;