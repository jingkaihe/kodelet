import { type RefObject, useCallback, useEffect, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { Conversation } from '../../types';

export const SIDEBAR_CONVERSATION_LIMIT = 100;
const CONVERSATION_POLL_INTERVAL_MS = 5000;
const CONVERSATION_POLL_MAX_DELAY_MS = 30000;

const getConversationTimestamp = (conversation: Conversation): number => {
  const timestamp =
    conversation.updatedAt ??
    conversation.updated_at ??
    conversation.createdAt ??
    conversation.created_at;

  return timestamp ? new Date(timestamp).getTime() : 0;
};

export const upsertConversationSummary = (
  conversations: Conversation[],
  nextConversation: Conversation
): Conversation[] => {
  const merged = conversations.filter((conversation) => conversation.id !== nextConversation.id);
  merged.unshift(nextConversation);

  merged.sort((left, right) => {
    const leftTime = getConversationTimestamp(left);
    const rightTime = getConversationTimestamp(right);
    return rightTime - leftTime;
  });

  return merged;
};

interface ConversationListOptions {
  sendControllersRef: RefObject<Record<string, AbortController>>;
  optimisticRemoteConversationRef: RefObject<{
    conversationId: string;
    confirmed: boolean;
  } | null>;
}

export const useConversationList = ({
  sendControllersRef,
  optimisticRemoteConversationRef,
}: ConversationListOptions) => {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [total, setTotal] = useState(0);
  const [cwdOptions, setCWDOptions] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const conversationListControllerRef = useRef<AbortController | null>(null);
  const runningUpdatesRef = useRef<Record<string, boolean>>({});

  const setRunning = useCallback((id: string, isRunning: boolean) => {
    // Preserve live transitions when an older list snapshot arrives.
    runningUpdatesRef.current[id] = isRunning;
    setConversations((currentConversations) =>
      currentConversations.map((currentConversation) =>
        currentConversation.id === id ? { ...currentConversation, isRunning } : currentConversation
      )
    );
  }, []);

  const refreshConversations = useCallback(
    async ({ silent = false } = {}) => {
      if (silent && conversationListControllerRef.current) {
        return true;
      }

      // Explicit refreshes take precedence over an older background snapshot.
      conversationListControllerRef.current?.abort();
      const controller = new AbortController();
      conversationListControllerRef.current = controller;
      const runningUpdates: Record<string, boolean> = {};
      runningUpdatesRef.current = runningUpdates;

      if (!silent) {
        setLoading(true);
      }
      try {
        const response = await apiService.getConversations(
          {
            limit: SIDEBAR_CONVERSATION_LIMIT,
            sortBy: 'updated',
            sortOrder: 'desc',
          },
          controller.signal
        );
        if (controller.signal.aborted) {
          return true;
        }

        const nextConversations = response.conversations || [];
        setConversations((currentConversations) => {
          const listedIds = new Set(nextConversations.map((conversation) => conversation.id));
          const optimisticConversation = optimisticRemoteConversationRef.current;
          // Keep new local sends, including failed attempts still available for retry.
          const pendingConversations = currentConversations.filter(
            (conversation) =>
              !listedIds.has(conversation.id) &&
              (sendControllersRef.current[conversation.id] ||
                (optimisticConversation?.conversationId === conversation.id &&
                  !optimisticConversation.confirmed))
          );
          const mergedConversations = [
            ...pendingConversations,
            ...nextConversations.map((conversation) => {
              const isRunning = sendControllersRef.current[conversation.id]
                ? true
                : runningUpdates[conversation.id];
              return isRunning === undefined ? conversation : { ...conversation, isRunning };
            }),
          ];
          // Keep the same array for unchanged snapshots: no tree rebuild or subscription churn.
          return JSON.stringify(currentConversations) === JSON.stringify(mergedConversations)
            ? currentConversations
            : mergedConversations;
        });
        setTotal(response.total ?? nextConversations.length);
        const responseCWDs = (
          response.cwds?.length
            ? response.cwds
            : nextConversations.map((nextConversation) => nextConversation.cwd)
        )
          .map((cwd) => cwd?.trim())
          .filter((cwd): cwd is string => Boolean(cwd));
        const nextCWDs = Array.from(new Set(responseCWDs));
        setCWDOptions((currentCWDs) =>
          currentCWDs.length === nextCWDs.length &&
          currentCWDs.every((cwd, index) => cwd === nextCWDs[index])
            ? currentCWDs
            : nextCWDs
        );
        return true;
      } catch (error) {
        if (!controller.signal.aborted) {
          console.error('Failed to load conversations', error);
          return false;
        }
        return true;
      } finally {
        if (conversationListControllerRef.current === controller) {
          conversationListControllerRef.current = null;
          if (!silent) {
            setLoading(false);
          }
        }
      }
    },
    [optimisticRemoteConversationRef, sendControllersRef]
  );

  useEffect(() => {
    let disposed = false;
    let pending = false;
    let refreshQueued = false;
    let timer: number | undefined;
    let delay = CONVERSATION_POLL_INTERVAL_MS;

    const refresh = async (silent = true) => {
      if (disposed || pending || (silent && document.visibilityState === 'hidden')) {
        return;
      }

      pending = true;
      refreshQueued = false;
      const succeeded = await refreshConversations({ silent });
      pending = false;
      delay = succeeded
        ? CONVERSATION_POLL_INTERVAL_MS
        : Math.min(delay * 2, CONVERSATION_POLL_MAX_DELAY_MS);
      if (!disposed && document.visibilityState !== 'hidden') {
        if (refreshQueued) {
          void refresh();
        } else {
          timer = window.setTimeout(() => void refresh(), delay);
        }
      }
    };

    const handleVisibilityChange = () => {
      window.clearTimeout(timer);
      if (document.visibilityState !== 'hidden') {
        delay = CONVERSATION_POLL_INTERVAL_MS;
        refreshQueued = pending;
        void refresh();
      }
    };

    void refresh(false);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    return () => {
      disposed = true;
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
      conversationListControllerRef.current?.abort();
      conversationListControllerRef.current = null;
    };
  }, [refreshConversations]);

  return {
    conversations,
    setConversations,
    total,
    cwdOptions,
    setCWDOptions,
    loading,
    refresh: refreshConversations,
    setRunning,
  };
};
