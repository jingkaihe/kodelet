import React, { useCallback } from "react";
import { useConversations } from "../hooks/useConversations";
import { useUrlFilters } from "../hooks/useUrlFilters";
import ConversationList from "../components/ConversationList";
import SearchAndFilters from "../components/SearchAndFilters";
import StatsCard from "../components/StatsCard";
import LoadingSpinner from "../components/LoadingSpinner";
import ErrorAlert from "../components/ErrorAlert";
import EmptyState from "../components/EmptyState";
import { showToast } from "../utils";

const ConversationListPage: React.FC = () => {
  const { filters, updateFilters, clearFilters, goToPage, currentPage } =
    useUrlFilters();
  const {
    conversations,
    stats,
    loading,
    error,
    totalPages,
    deleteConversation,
    refresh,
  } = useConversations({ filters });

  const handleDeleteConversation = async (conversationId: string) => {
    if (!confirm("Are you sure you want to delete this conversation?")) {
      return;
    }

    try {
      await deleteConversation(conversationId);
      showToast("Conversation deleted successfully", "success");
    } catch (err) {
      showToast(
        `Failed to delete conversation: ${err instanceof Error ? err.message : "Unknown error"}`,
        "error",
      );
    }
  };

  const handleSearch = useCallback(
    (searchTerm: string) => {
      updateFilters({ searchTerm });
    },
    [updateFilters],
  );

  const handleClearFilters = useCallback(() => {
    clearFilters();
  }, [clearFilters]);

  return (
    <div className="container mx-auto px-4 py-6 relative z-10">
      {/* Editorial Header */}
      <div className="mb-4 animate-fade-in">
        <div className="flex items-center gap-3 mb-2">
          <div className="w-1 h-10 bg-gradient-to-b from-kodelet-orange via-kodelet-blue to-kodelet-green rounded-full"></div>
          <h1 className="text-4xl font-heading font-bold text-kodelet-dark tracking-tight">
            Conversations
          </h1>
        </div>
        <p className="text-base text-kodelet-dark/70 font-body ml-7 italic">
          Explore your conversation history with elegant clarity
        </p>
      </div>

      {/* Search and Filters */}
      <SearchAndFilters
        filters={filters}
        onFiltersChange={updateFilters}
        onSearch={handleSearch}
        onClearFilters={handleClearFilters}
      />

      {/* Statistics Card */}
      {stats && <StatsCard stats={stats} />}

      {/* Error State */}
      {error && <ErrorAlert message={error} onRetry={refresh} />}

      {/* Loading State */}
      {loading && (!conversations || conversations.length === 0) && (
        <LoadingSpinner message="Loading conversations..." />
      )}

      {/* Empty State */}
      {!loading && conversations && conversations.length === 0 && !error && (
        <EmptyState
          iconType="search"
          title="No conversations found"
          description="Try adjusting your search criteria or filters"
        />
      )}

      {/* Conversation List */}
      {conversations && conversations.length > 0 && (
        <ConversationList
          conversations={conversations}
          loading={loading}
          currentPage={currentPage}
          totalPages={totalPages}
          onPageChange={goToPage}
          onDelete={handleDeleteConversation}
        />
      )}
    </div>
  );
};

export default ConversationListPage;
