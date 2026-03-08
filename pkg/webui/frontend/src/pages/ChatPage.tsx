import React, { startTransition, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import ChatSidebar from '../components/chat/ChatSidebar';
import ChatTranscript from '../components/chat/ChatTranscript';
import { applyChatStreamEvent, conversationToChatMessages } from '../features/chat/state';
import apiService from '../services/api';
import type { ChatStreamEvent, Conversation } from '../types';
import { cn, formatCost, formatDate, showToast } from '../utils';

const normalizeConversation = (conversation: Conversation): Conversation => ({
  ...conversation,
  messages: (conversation.messages || []).map((message) => ({
    role: message.role || 'user',
    content: message.content || '',
    toolCalls: message.toolCalls || message.tool_calls || [],
    thinkingText: message.thinkingText,
  })),
  toolResults: conversation.toolResults || {},
});

const getGreeting = (): string => {
  const hour = new Date().getHours();
  if (hour < 12) {
    return 'Good morning';
  }
  if (hour < 18) {
    return 'Good afternoon';
  }
  return 'Good evening';
};

const ChatPage: React.FC = () => {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();
  const conversationId = id || null;
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversation, setConversation] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState(() => conversationToChatMessages(null));
  const [draft, setDraft] = useState('');
  const [sidebarLoading, setSidebarLoading] = useState(true);
  const [conversationLoading, setConversationLoading] = useState(false);
  const [conversationError, setConversationError] = useState<string | null>(null);
  const [streamError, setStreamError] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const transcriptEndRef = useRef<HTMLDivElement | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);

  const refreshConversations = async () => {
    setSidebarLoading(true);
    try {
      const response = await apiService.getConversations({
        limit: 40,
        sortBy: 'updated',
        sortOrder: 'desc',
      });
      setConversations(response.conversations || []);
    } catch (error) {
      console.error('Failed to load conversations', error);
    } finally {
      setSidebarLoading(false);
    }
  };

  useEffect(() => {
    void refreshConversations();
  }, []);

  useEffect(() => {
    return () => {
      abortControllerRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (!conversationId) {
      setConversation(null);
      setMessages([]);
      setConversationError(null);
      return;
    }

    setConversationLoading(true);
    setConversationError(null);

    void apiService
      .getConversation(conversationId)
      .then((data) => {
        const normalizedConversation = normalizeConversation(data);
        setConversation(normalizedConversation);
        setMessages(conversationToChatMessages(normalizedConversation));
      })
      .catch((error: unknown) => {
        const message =
          error instanceof Error ? error.message : 'Failed to load conversation';
        setConversationError(message);
      })
      .finally(() => {
        setConversationLoading(false);
      });
  }, [conversationId]);

  useEffect(() => {
    transcriptEndRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' });
  }, [messages, sending]);

  const handleNewChat = () => {
    if (sending) {
      return;
    }

    setConversation(null);
    setMessages([]);
    setConversationError(null);
    setStreamError(null);
    startTransition(() => navigate('/'));
  };

  const handleSelectConversation = (nextConversationId: string) => {
    if (sending || nextConversationId === conversationId) {
      return;
    }

    setStreamError(null);
    startTransition(() => navigate(`/c/${nextConversationId}`));
  };

  const handleSubmit = async () => {
    const prompt = draft.trim();
    if (!prompt || sending) {
      return;
    }

    setDraft('');
    setStreamError(null);
    setMessages((currentMessages) => [
      ...currentMessages,
      {
        role: 'user',
        content: prompt,
      },
    ]);
    setSending(true);

    const controller = new AbortController();
    abortControllerRef.current = controller;

    let streamedConversationId = conversationId;
    let streamedError: string | null = null;

    try {
      await apiService.streamChat(
        {
          message: prompt,
          conversationId: conversationId || undefined,
        },
        {
          signal: controller.signal,
          onEvent: (event: ChatStreamEvent) => {
            if (event.kind === 'conversation' && event.conversation_id) {
              streamedConversationId = event.conversation_id;
            }

            if (event.kind === 'error') {
              streamedError = event.error || 'Chat request failed';
              setStreamError(streamedError);
              return;
            }

            setMessages((currentMessages) =>
              applyChatStreamEvent(currentMessages, event)
            );
          },
        }
      );

      if (streamedError) {
        showToast(streamedError, 'error');
        await refreshConversations();
        return;
      }

      if (streamedConversationId) {
        const latestConversation = normalizeConversation(
          await apiService.getConversation(streamedConversationId)
        );
        setConversation(latestConversation);
        setMessages(conversationToChatMessages(latestConversation));
        if (streamedConversationId !== conversationId) {
          startTransition(() => navigate(`/c/${streamedConversationId}`));
        }
      }

      await refreshConversations();
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') {
        return;
      }

      const message =
        error instanceof Error ? error.message : 'Failed to send message';
      setStreamError(message);
      showToast(message, 'error');
    } finally {
      abortControllerRef.current = null;
      setSending(false);
    }
  };

  const handleDraftKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      void handleSubmit();
    }
  };

  const heading = useMemo(() => {
    if (conversation?.summary) {
      return conversation.summary;
    }
    return getGreeting();
  }, [conversation?.summary]);

  return (
    <div className="min-h-screen bg-transparent">
      <div className="grid min-h-screen grid-cols-1 lg:grid-cols-[320px_minmax(0,1fr)]">
        <ChatSidebar
          activeConversationId={conversationId}
          conversations={conversations}
          disabled={sending}
          loading={sidebarLoading}
          onNewChat={handleNewChat}
          onSelectConversation={handleSelectConversation}
        />

        <main className="relative flex min-h-screen flex-col">
          <header className="border-b border-black/8 px-4 py-5 md:px-8">
            <div className="max-w-5xl">
              <p className="mb-2 text-xs font-heading uppercase tracking-[0.2em] text-kodelet-mid-gray">
                {conversation ? 'Conversation' : 'Workspace assistant'}
              </p>
              <h2 className="text-3xl font-heading font-bold tracking-tight text-kodelet-dark md:text-4xl">
                {heading}
              </h2>
              <div className="mt-3 flex flex-wrap gap-2 text-xs uppercase tracking-[0.12em] text-kodelet-mid-gray">
                {conversation?.provider ? (
                  <span className="rounded-full bg-white/80 px-3 py-1">
                    {conversation.provider}
                  </span>
                ) : null}
                {conversation?.id ? (
                  <span className="rounded-full bg-white/80 px-3 py-1">
                    {conversation.id}
                  </span>
                ) : null}
                {conversation?.updatedAt ? (
                  <span className="rounded-full bg-white/80 px-3 py-1">
                    Updated {formatDate(conversation.updatedAt)}
                  </span>
                ) : null}
                {conversation?.usage ? (
                  <span className="rounded-full bg-white/80 px-3 py-1">
                    {formatCost(conversation.usage)}
                  </span>
                ) : null}
              </div>
            </div>
          </header>

          <div className="flex-1 overflow-y-auto">
            {conversationLoading ? (
              <div className="flex min-h-[50vh] items-center justify-center px-6 py-12">
                <div className="rounded-2xl border border-black/8 bg-white/85 px-6 py-5 text-sm text-kodelet-dark/70">
                  Loading conversation…
                </div>
              </div>
            ) : conversationError ? (
              <div className="px-4 py-8 md:px-8">
                <div className="max-w-3xl rounded-3xl border border-kodelet-orange/20 bg-white/85 px-6 py-5 text-kodelet-dark shadow-[0_20px_60px_rgba(20,20,19,0.06)]">
                  <p className="font-heading text-sm font-semibold uppercase tracking-[0.16em] text-kodelet-orange">
                    Load error
                  </p>
                  <p className="mt-3 text-sm leading-7">{conversationError}</p>
                </div>
              </div>
            ) : (
              <>
                <ChatTranscript isStreaming={sending} messages={messages} />
                <div ref={transcriptEndRef} />
              </>
            )}
          </div>

          <div className="border-t border-black/8 bg-kodelet-light/85 px-4 py-4 backdrop-blur md:px-8">
            <div className="mx-auto max-w-5xl">
              {streamError ? (
                <div className="mb-3 rounded-2xl border border-kodelet-orange/20 bg-white/85 px-4 py-3 text-sm text-kodelet-dark">
                  {streamError}
                </div>
              ) : null}

              <div className="rounded-[1.75rem] border border-black/10 bg-white/90 p-3 shadow-[0_18px_50px_rgba(20,20,19,0.07)]">
                <textarea
                  className="min-h-[88px] w-full resize-none border-0 bg-transparent px-3 py-3 font-body text-base leading-7 text-kodelet-dark outline-none placeholder:text-kodelet-dark/40"
                  disabled={sending}
                  onChange={(event) => setDraft(event.target.value)}
                  onKeyDown={handleDraftKeyDown}
                  placeholder="Ask kodelet anything..."
                  value={draft}
                />

                <div className="flex items-center justify-between gap-3 border-t border-black/8 px-3 pt-3">
                  <p className="text-xs uppercase tracking-[0.14em] text-kodelet-mid-gray">
                    Enter to send. Shift + Enter for a new line.
                  </p>

                  <button
                    className={cn(
                      'rounded-full px-4 py-2 font-heading text-sm font-semibold text-white transition',
                      sending || !draft.trim()
                        ? 'cursor-not-allowed bg-kodelet-mid-gray'
                        : 'bg-kodelet-dark hover:bg-black'
                    )}
                    disabled={sending || !draft.trim()}
                    onClick={() => void handleSubmit()}
                    type="button"
                  >
                    {sending ? 'Working…' : 'Send'}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </main>
      </div>
    </div>
  );
};

export default ChatPage;
