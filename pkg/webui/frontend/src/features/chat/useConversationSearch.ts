import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import apiService from '../../services/api';
import type { Conversation } from '../../types';
import { debounce } from '../../utils';

interface ConversationSearchOptions {
  conversations: Conversation[];
  total: number;
  loading: boolean;
  limit: number;
  setCWDOptions: Dispatch<SetStateAction<string[]>>;
}

const emptySearch = {
  term: '',
  cwd: '',
  results: [] as Conversation[],
  error: null as string | null,
  hasMore: false,
  loading: false,
  loadingMore: false,
  offset: 0,
  total: 0,
};

export const useConversationSearch = ({
  conversations,
  total,
  loading,
  limit,
  setCWDOptions,
}: ConversationSearchOptions) => {
  const [isOpen, setIsOpen] = useState(false);
  const [search, setSearch] = useState(emptySearch);
  const requestRef = useRef(0);
  const criteriaRef = useRef({ term: '', cwd: '' });

  const refresh = useCallback(
    async (offset = 0) => {
      const requestId = ++requestRef.current;
      const searchTerm = criteriaRef.current.term.trim();
      const cwd = criteriaRef.current.cwd.trim();
      const loadingMore = offset > 0;
      setSearch((current) => ({
        ...current,
        error: null,
        ...(loadingMore ? { loadingMore: true } : { loading: true }),
      }));
      try {
        const response = await apiService.getConversations({
          searchTerm,
          cwd,
          limit,
          offset: offset || undefined,
          sortBy: 'updated',
          sortOrder: 'desc',
        });
        if (requestRef.current !== requestId) return;

        const nextConversations = response.conversations || [];
        const nextOffset = offset + nextConversations.length;
        const nextTotal = response.total ?? nextOffset;
        setSearch((current) => {
          const seen = new Set(current.results.map((conversation) => conversation.id));
          const results = loadingMore
            ? [
                ...current.results,
                ...nextConversations.filter((conversation) => {
                  if (seen.has(conversation.id)) return false;
                  seen.add(conversation.id);
                  return true;
                }),
              ]
            : nextConversations;
          return {
            ...current,
            results,
            offset: nextOffset,
            total: nextTotal,
            hasMore: response.hasMore ?? nextOffset < nextTotal,
          };
        });
        const responseCWDs = (
          response.cwds?.length
            ? response.cwds
            : nextConversations.map((conversation) => conversation.cwd)
        )
          .map((path) => path?.trim())
          .filter((path): path is string => Boolean(path));
        setCWDOptions((current) =>
          Array.from(new Set([...current, cwd, ...responseCWDs].filter(Boolean)))
        );
      } catch (error) {
        if (requestRef.current !== requestId) return;
        console.error('Failed to search conversations', error);
        setSearch((current) => ({
          ...current,
          ...(!loadingMore ? { results: [], hasMore: false, offset: 0, total: 0 } : {}),
          error: error instanceof Error ? error.message : 'Failed to search conversations',
        }));
      } finally {
        if (requestRef.current === requestId) {
          setSearch((current) => ({
            ...current,
            ...(loadingMore ? { loadingMore: false } : { loading: false }),
          }));
        }
      }
    },
    [limit, setCWDOptions]
  );

  const requestRefresh = useMemo(() => debounce(() => void refresh(), 200), [refresh]);
  useEffect(
    () => () => {
      requestRefresh.cancel();
      requestRef.current += 1;
    },
    [requestRefresh]
  );

  const changeFilter = (field: 'term' | 'cwd', value: string) => {
    criteriaRef.current = { ...criteriaRef.current, [field]: value };
    requestRef.current += 1;
    requestRefresh.cancel();
    const filtered = Boolean(criteriaRef.current.term.trim() || criteriaRef.current.cwd.trim());
    setSearch((current) => ({
      ...current,
      [field]: value,
      error: null,
      hasMore: false,
      loadingMore: false,
      offset: 0,
      results: filtered ? [] : conversations,
      loading: filtered || loading,
      total: filtered ? 0 : total,
    }));
    if (filtered) {
      if (field === 'term') requestRefresh();
      else void refresh();
    }
  };

  const close = useCallback(() => {
    setIsOpen(false);
    requestRefresh.cancel();
    requestRef.current += 1;
    criteriaRef.current = { term: '', cwd: '' };
    setSearch(emptySearch);
  }, [requestRefresh]);

  const open = () => {
    setSearch((current) => ({
      ...current,
      results: conversations,
      error: null,
      hasMore: false,
      loading: false,
      loadingMore: false,
      offset: conversations.length,
      total,
    }));
    setIsOpen(true);
  };

  useEffect(() => {
    if (!isOpen || search.term.trim() || search.cwd.trim()) return;
    setSearch((current) => ({
      ...current,
      results: conversations,
      error: null,
      hasMore: false,
      loading,
      loadingMore: false,
      offset: conversations.length,
      total,
    }));
  }, [conversations, isOpen, loading, search.cwd, search.term, total]);

  const loadMore = () => {
    if (
      search.loading ||
      search.loadingMore ||
      !search.hasMore ||
      (!criteriaRef.current.term.trim() && !criteriaRef.current.cwd.trim())
    )
      return;
    void refresh(search.offset);
  };

  return {
    isOpen,
    open,
    close,
    results: search.results,
    dialogProps: {
      conversations: search.results,
      cwdFilter: search.cwd,
      error: search.error,
      hasMore: search.hasMore,
      loading: search.loading,
      loadingMore: search.loadingMore,
      searchTerm: search.term,
      total: search.total,
      onClose: close,
      onCwdFilterChange: (value: string) => changeFilter('cwd', value),
      onSearchTermChange: (value: string) => changeFilter('term', value),
      onLoadMore: loadMore,
    },
  };
};
