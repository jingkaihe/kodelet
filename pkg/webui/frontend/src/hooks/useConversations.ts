import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { Conversation, ConversationStats, SearchFilters } from '../types';
import { apiService } from '../services/api';

interface UseConversationsResult {
  conversations: Conversation[];
  stats: ConversationStats | null;
  loading: boolean;
  error: string | null;
  currentPage: number;
  totalPages: number;
  loadConversations: () => Promise<void>;
  deleteConversation: (id: string) => Promise<void>;
  refresh: () => Promise<void>;
}

interface UseConversationsOptions {
  filters: SearchFilters;
}

export const useConversations = (options: UseConversationsOptions): UseConversationsResult => {
  const { filters } = options;
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [stats, setStats] = useState<ConversationStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [currentPage, setCurrentPage] = useState(1);
  const [totalPages, setTotalPages] = useState(1);
  
  // Use refs to track the latest values without causing re-renders
  const loadingRef = useRef(false);
  const abortControllerRef = useRef<AbortController | null>(null);
  const lastFiltersRef = useRef<string>('');

  // Memoize individual filter properties to prevent unnecessary re-renders
  const searchTerm = useMemo(() => filters.searchTerm, [filters.searchTerm]);
  const sortBy = useMemo(() => filters.sortBy, [filters.sortBy]);
  const sortOrder = useMemo(() => filters.sortOrder, [filters.sortOrder]);
  const limit = useMemo(() => filters.limit, [filters.limit]);
  const offset = useMemo(() => filters.offset, [filters.offset]);

  const loadConversations = useCallback(async () => {
    // Create a stable filter key
    const filterKey = `${searchTerm}-${sortBy}-${sortOrder}-${limit}-${offset}`;
    
    // Prevent duplicate requests with the same filters
    if (loadingRef.current || lastFiltersRef.current === filterKey) {
      return;
    }

    // Cancel any pending request
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
    }

    // Create new abort controller for this request
    const abortController = new AbortController();
    abortControllerRef.current = abortController;
    
    loadingRef.current = true;
    lastFiltersRef.current = filterKey;
    setLoading(true);
    setError(null);

    try {
      const response = await apiService.getConversations({
        searchTerm,
        sortBy,
        sortOrder,
        limit,
        offset,
      });
      
      // Check if request was aborted
      if (abortController.signal.aborted) {
        return;
      }
      
      // Ensure conversations is always an array
      setConversations(Array.isArray(response.conversations) ? response.conversations : []);
      
      // Update stats from the response
      if (response.stats) {
        setStats(response.stats);
      }
      
      // Calculate pagination
      const totalItems = response.total || response.conversations.length;
      const calculatedTotalPages = Math.ceil(totalItems / limit);
      setTotalPages(calculatedTotalPages);
      
      // Update current page based on offset
      const calculatedCurrentPage = Math.floor(offset / limit) + 1;
      setCurrentPage(calculatedCurrentPage);
      
    } catch (err) {
      // Don't set error if request was aborted
      if (!abortController.signal.aborted) {
        setError(err instanceof Error ? err.message : 'Failed to load conversations');
      }
    } finally {
      loadingRef.current = false;
      setLoading(false);
      abortControllerRef.current = null;
    }
  }, [searchTerm, sortBy, sortOrder, limit, offset]);

  const deleteConversation = useCallback(async (id: string) => {
    try {
      await apiService.deleteConversation(id);
      setConversations(prev => prev.filter(c => c.id !== id));
      // Reset the filter ref to allow reloading
      lastFiltersRef.current = '';
      await loadConversations();
    } catch (err) {
      throw new Error(err instanceof Error ? err.message : 'Failed to delete conversation');
    }
  }, [loadConversations]);

  const refresh = useCallback(async () => {
    // Reset the filter ref to force reload
    lastFiltersRef.current = '';
    await loadConversations();
  }, [loadConversations]);

  // Load conversations when filters change (with minimal dependencies)
  useEffect(() => {
    loadConversations();
    
    // Cleanup function to cancel any pending requests
    return () => {
      if (abortControllerRef.current) {
        abortControllerRef.current.abort();
      }
    };
  }, [searchTerm, sortBy, sortOrder, limit, offset, loadConversations]);

  return {
    conversations,
    stats,
    loading,
    error,
    currentPage,
    totalPages,
    loadConversations,
    deleteConversation,
    refresh,
  };
};