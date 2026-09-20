import type { KeyboardEvent, RefObject } from 'react';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { CWDHint, Runner } from '../../types';
import { debounce } from '../../utils';

interface CWDSuggestionsOptions {
  conversationId: string | null;
  viewedConversationIdRef: RefObject<string | null>;
  open: boolean;
  runnerId: string;
  environmentProfile: string;
  profile: string;
  runners: Runner[];
}

// Own the query, keyboard selection, and request lifetime together: a change of
// runner/profile, dialog lifetime, or conversation must invalidate old suggestions.
export const useCWDSuggestions = ({
  conversationId,
  viewedConversationIdRef,
  open,
  runnerId,
  environmentProfile,
  profile,
  runners,
}: CWDSuggestionsOptions) => {
  const [query, setQuery] = useState('');
  const [suggestions, setSuggestions] = useState<CWDHint[]>([]);
  const [suggestionsOpen, setSuggestionsOpen] = useState(false);
  const [index, setIndex] = useState(-1);
  const requestRef = useRef(0);
  const focusedRef = useRef(false);
  const skipQueryRef = useRef<string | null>(null);

  const clearSuggestions = useCallback(() => {
    setSuggestions([]);
    setSuggestionsOpen(false);
    setIndex(-1);
  }, []);

  const requestSuggestions = useMemo(
    () =>
      debounce((value: string) => {
        const requestId = ++requestRef.current;
        void apiService
          .getCWDHints(value, { runnerId, environmentProfile, profile })
          .then((response) => {
            if (requestRef.current !== requestId || viewedConversationIdRef.current) return;
            setSuggestions(response.hints || []);
            setSuggestionsOpen(focusedRef.current && (response.hints || []).length > 0);
            setIndex(-1);
          })
          .catch((error) => {
            if (requestRef.current !== requestId || viewedConversationIdRef.current) return;
            console.error('Failed to load cwd suggestions', error);
            setSuggestions([]);
            setSuggestionsOpen(false);
          });
      }, 150),
    [runnerId, environmentProfile, profile, viewedConversationIdRef]
  );

  const cancel = useCallback(() => {
    requestSuggestions.cancel();
    requestRef.current += 1;
    clearSuggestions();
  }, [requestSuggestions, clearSuggestions]);

  useEffect(() => {
    return () => {
      requestSuggestions.cancel();
      requestRef.current += 1;
    };
  }, [requestSuggestions]);

  useEffect(() => {
    if (conversationId) requestRef.current += 1;
  }, [conversationId]);

  useEffect(() => {
    requestRef.current += 1;
    clearSuggestions();
    const runner = runners.find((candidate) => candidate.id === runnerId);
    const discoveryAvailable =
      runnerId &&
      runner?.connected &&
      runner.workspaceDiscovery &&
      (runner.status === 'idle' || runner.status === 'busy');
    if (!open || conversationId || !discoveryAvailable) {
      requestSuggestions.cancel();
      focusedRef.current = false;
      return;
    }
    if (!query.trim() || skipQueryRef.current === query) {
      if (!query.trim()) skipQueryRef.current = null;
      requestSuggestions.cancel();
      requestRef.current += 1;
      return;
    }
    skipQueryRef.current = null;
    requestSuggestions(query);
  }, [conversationId, query, runnerId, open, runners, requestSuggestions, clearSuggestions]);

  const reset = (value: string, skipQuery: string | null = null) => {
    skipQueryRef.current = skipQuery;
    cancel();
    setQuery(value);
  };

  const selectSuggestion = (path: string) => reset(path, path);

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (suggestionsOpen && suggestions.length > 0) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        setIndex((current) =>
          event.key === 'ArrowDown'
            ? current >= suggestions.length - 1
              ? 0
              : current + 1
            : current <= 0
              ? suggestions.length - 1
              : current - 1
        );
        return;
      }
      if (!event.shiftKey && event.key === 'Tab') {
        event.preventDefault();
        const suggestion = suggestions[index >= 0 ? index : 0];
        if (suggestion) selectSuggestion(suggestion.path);
        return;
      }
      if (event.key === 'Enter' && index >= 0) {
        event.preventDefault();
        selectSuggestion(suggestions[index].path);
        return;
      }
    }
    if (event.key === 'Enter') {
      event.preventDefault();
      selectSuggestion(query.trim());
    } else if (event.key === 'Escape' && suggestionsOpen) {
      setSuggestionsOpen(false);
      setIndex(-1);
    }
  };

  return {
    query,
    reset,
    cancel,
    // Opening context preserves focus until the dialog's initial focus takes over.
    setQuery,
    props: {
      cwdQuery: query,
      cwdSuggestions: suggestions,
      cwdSuggestionsOpen: suggestionsOpen,
      cwdSuggestionIndex: index,
      onSelectCwdSuggestion: selectSuggestion,
      onCwdInputKeyDown: onKeyDown,
      onCwdInputChange: (value: string) => {
        skipQueryRef.current = null;
        setQuery(value);
        setSuggestionsOpen(false);
        setIndex(-1);
      },
      onCwdInputFocus: () => {
        focusedRef.current = true;
        setSuggestionsOpen(query.trim().length > 0 && suggestions.length > 0);
      },
      onCwdInputBlur: () => {
        focusedRef.current = false;
        window.setTimeout(() => {
          setSuggestionsOpen(false);
          setIndex(-1);
        }, 120);
      },
    },
  };
};
