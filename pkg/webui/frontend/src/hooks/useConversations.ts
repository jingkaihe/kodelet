import { useState, useEffect, useCallback } from 'react';
import { Conversation, ConversationStats, SearchFilters } from '../types';
import { apiService } from '../services/api';

interface UseConversationsResult {
  conversations: Conversation[];
  stats: ConversationStats | null;
  loading: boolean;
  error: string | null;
  hasMore: boolean;
  filters: SearchFilters;
  setFilters: (filters: Partial<SearchFilters>) => void;
  loadConversations: () => Promise<void>;
  loadMore: () => Promise<void>;
  deleteConversation: (id: string) => Promise<void>;
  refresh: () => Promise<void>;
}

const initialFilters: SearchFilters = {
  searchTerm: '',
  sortBy: 'updated',
  sortOrder: 'desc',
  limit: 25,
  offset: 0,
};

export const useConversations = (): UseConversationsResult => {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [stats, setStats] = useState<ConversationStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [filters, setFiltersState] = useState<SearchFilters>(initialFilters);

  const setFilters = useCallback((newFilters: Partial<SearchFilters>) => {
    setFiltersState(prev => ({
      ...prev,
      ...newFilters,
      // Reset offset if other filters change
      offset: newFilters.offset !== undefined ? newFilters.offset : 0,
    }));
  }, []);

  const loadConversations = useCallback(async (append = false) => {
    if (loading) return;
    
    setLoading(true);
    setError(null);

    try {
      const currentFilters = append ? { ...filters, offset: conversations.length } : filters;
      const response = await apiService.getConversations(currentFilters);
      
      if (append) {
        setConversations(prev => [...prev, ...response.conversations]);
      } else {
        setConversations(response.conversations);
      }
      
      setHasMore(response.hasMore);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load conversations');
    } finally {
      setLoading(false);
    }
  }, [filters, loading, conversations.length]);

  const loadMore = useCallback(async () => {
    if (hasMore && !loading) {
      await loadConversations(true);
    }
  }, [hasMore, loading, loadConversations]);

  const loadStats = useCallback(async () => {
    try {
      const statsData = await apiService.getConversationStats();
      setStats(statsData);
    } catch (err) {
      console.error('Failed to load stats:', err);
    }
  }, []);

  const deleteConversation = useCallback(async (id: string) => {
    try {
      await apiService.deleteConversation(id);
      setConversations(prev => prev.filter(c => c.id !== id));
      // Refresh stats after deletion
      await loadStats();
    } catch (err) {
      throw new Error(err instanceof Error ? err.message : 'Failed to delete conversation');
    }
  }, [loadStats]);

  const refresh = useCallback(async () => {
    await Promise.all([
      loadConversations(),
      loadStats(),
    ]);
  }, [loadConversations, loadStats]);

  // Load conversations when filters change
  useEffect(() => {
    loadConversations();
  }, [filters.searchTerm, filters.sortBy, filters.sortOrder, filters.limit]);

  // Load initial data
  useEffect(() => {
    refresh();
  }, []);

  return {
    conversations,
    stats,
    loading,
    error,
    hasMore,
    filters,
    setFilters,
    loadConversations,
    loadMore,
    deleteConversation,
    refresh,
  };
};