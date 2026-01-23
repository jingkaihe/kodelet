import { useState, useEffect, useCallback } from 'react';
import { Conversation } from '../types';
import { apiService } from '../services/api';

interface UseConversationResult {
  conversation: Conversation | null;
  loading: boolean;
  error: string | null;
  deleteConversation: () => Promise<void>;
  exportConversation: () => void;
  refresh: () => Promise<void>;
}

export const useConversation = (conversationId: string): UseConversationResult => {
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadConversation = useCallback(async () => {
    if (!conversationId) return;

    setLoading(true);
    setError(null);

    try {
      const data = await apiService.getConversation(conversationId);

      // Ensure all messages have proper structure
      if (data.messages) {
        data.messages = data.messages.map(message => ({
          role: message.role || 'user',
          content: message.content || '',
          toolCalls: message.toolCalls || message.tool_calls || [],
          thinkingText: message.thinkingText
        }));
      }

      // Ensure toolResults is always an object
      if (!data.toolResults) {
        data.toolResults = {};
      }

      setConversation(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load conversation');
    } finally {
      setLoading(false);
    }
  }, [conversationId]);

  const deleteConversation = useCallback(async () => {
    if (!conversation?.id) return;

    try {
      await apiService.deleteConversation(conversation.id);
      // Navigate back to conversation list
      window.location.href = '/';
    } catch (err) {
      throw new Error(err instanceof Error ? err.message : 'Failed to delete conversation');
    }
  }, [conversation?.id]);

  const exportConversation = useCallback(() => {
    if (!conversation?.id) return;

    const exportData = {
      id: conversation.id,
      createdAt: conversation.createdAt,
      messages: conversation.messages,
      usage: conversation.usage,
      toolResults: conversation.toolResults
    };

    const blob = new Blob([JSON.stringify(exportData, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `conversation-${conversation.id.substring(0, 8)}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }, [conversation]);

  const refresh = useCallback(async () => {
    await loadConversation();
  }, [loadConversation]);

  // Load conversation when conversationId changes
  useEffect(() => {
    if (conversationId) {
      loadConversation();
    }
  }, [conversationId, loadConversation]);

  return {
    conversation,
    loading,
    error,
    deleteConversation,
    exportConversation,
    refresh,
  };
};