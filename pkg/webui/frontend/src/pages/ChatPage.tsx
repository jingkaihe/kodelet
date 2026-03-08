import React, { startTransition, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import ChatSidebar from '../components/chat/ChatSidebar';
import ChatTranscript from '../components/chat/ChatTranscript';
import { applyChatStreamEvent, conversationToChatMessages } from '../features/chat/state';
import apiService from '../services/api';
import type {
  ChatStreamEvent,
  ContentBlock,
  Conversation,
  PendingImageAttachment,
} from '../types';
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

const DEFAULT_SIDEBAR_WIDTH = 320;
const MIN_SIDEBAR_WIDTH = 260;
const MAX_SIDEBAR_WIDTH = 520;
const SIDEBAR_WIDTH_STORAGE_KEY = 'kodelet.chat.sidebar.width';
const SIDEBAR_VISIBLE_STORAGE_KEY = 'kodelet.chat.sidebar.visible';
const MAX_IMAGE_ATTACHMENTS = 10;
const MAX_IMAGE_BYTES = 5 * 1024 * 1024;
const SUPPORTED_IMAGE_TYPES = new Set([
  'image/png',
  'image/jpeg',
  'image/gif',
  'image/webp',
]);

const attachmentId = (): string =>
  typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `attachment-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;

const readFileAsDataUrl = (file: File): Promise<string> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      if (typeof reader.result === 'string') {
        resolve(reader.result);
        return;
      }
      reject(new Error('Failed to read image data'));
    };
    reader.onerror = () => reject(reader.error || new Error('Failed to read image data'));
    reader.readAsDataURL(file);
  });

const fileToPendingAttachment = async (file: File): Promise<PendingImageAttachment> => {
  if (!SUPPORTED_IMAGE_TYPES.has(file.type)) {
    throw new Error('Only PNG, JPEG, GIF, and WebP images are supported');
  }

  if (file.size > MAX_IMAGE_BYTES) {
    throw new Error('Each image must be 5MB or smaller');
  }

  const dataUrl = await readFileAsDataUrl(file);
  const [, base64 = ''] = dataUrl.split(',', 2);

  return {
    id: attachmentId(),
    name: file.name || 'Pasted image',
    mediaType: file.type,
    data: base64,
    previewUrl: dataUrl,
    size: file.size,
  };
};

const buildUserContent = (
  prompt: string,
  attachments: PendingImageAttachment[]
): ContentBlock[] => [
  ...(prompt ? [{ type: 'text' as const, text: prompt }] : []),
  ...attachments.map((attachment) => ({
    type: 'image' as const,
    source: {
      data: attachment.data,
      media_type: attachment.mediaType,
    },
  })),
];

const clampSidebarWidth = (width: number): number =>
  Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, width));

const readStoredSidebarWidth = (): number => {
  if (typeof window === 'undefined') {
    return DEFAULT_SIDEBAR_WIDTH;
  }

  const storedWidth = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
  if (storedWidth === null) {
    return DEFAULT_SIDEBAR_WIDTH;
  }

  const parsedWidth = Number(storedWidth);
  return Number.isFinite(parsedWidth)
    ? clampSidebarWidth(parsedWidth)
    : DEFAULT_SIDEBAR_WIDTH;
};

const readStoredSidebarVisible = (): boolean => {
  if (typeof window === 'undefined') {
    return true;
  }

  return window.localStorage.getItem(SIDEBAR_VISIBLE_STORAGE_KEY) !== 'false';
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
  const [attachments, setAttachments] = useState<PendingImageAttachment[]>([]);
  const [dragActive, setDragActive] = useState(false);
  const [sidebarVisible, setSidebarVisible] = useState(readStoredSidebarVisible);
  const [sidebarWidth, setSidebarWidth] = useState(readStoredSidebarWidth);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  const transcriptEndRef = useRef<HTMLDivElement | null>(null);
  const abortControllerRef = useRef<AbortController | null>(null);
  const sidebarResizeStartRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

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
    return () => {
      attachments.forEach((attachment) => {
        if (attachment.previewUrl.startsWith('blob:')) {
          URL.revokeObjectURL(attachment.previewUrl);
        }
      });
    };
  }, [attachments]);

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_VISIBLE_STORAGE_KEY, String(sidebarVisible));
  }, [sidebarVisible]);

  useEffect(() => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(sidebarWidth));
  }, [sidebarWidth]);

  useEffect(() => {
    if (!isResizingSidebar) {
      return undefined;
    }

    const previousUserSelect = document.body.style.userSelect;
    const previousCursor = document.body.style.cursor;
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'col-resize';

    const handleMouseMove = (event: MouseEvent) => {
      const resizeStart = sidebarResizeStartRef.current;
      if (!resizeStart) {
        return;
      }

      const nextWidth = clampSidebarWidth(
        resizeStart.startWidth + (event.clientX - resizeStart.startX)
      );
      setSidebarWidth(nextWidth);
    };

    const stopResizing = () => {
      sidebarResizeStartRef.current = null;
      setIsResizingSidebar(false);
    };

    window.addEventListener('mousemove', handleMouseMove);
    window.addEventListener('mouseup', stopResizing);

    return () => {
      document.body.style.userSelect = previousUserSelect;
      document.body.style.cursor = previousCursor;
      window.removeEventListener('mousemove', handleMouseMove);
      window.removeEventListener('mouseup', stopResizing);
    };
  }, [isResizingSidebar]);

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

  const handleSidebarToggle = () => {
    if (sending) {
      return;
    }

    setSidebarVisible((currentValue) => !currentValue);
  };

  const handleSidebarResizeStart = (
    event: React.MouseEvent<HTMLElement>
  ) => {
    event.preventDefault();
    sidebarResizeStartRef.current = {
      startX: event.clientX,
      startWidth: sidebarWidth,
    };
    setIsResizingSidebar(true);
  };

  const handleSubmit = async () => {
    const prompt = draft.trim();
    if ((!prompt && attachments.length === 0) || sending) {
      return;
    }

    setDraft('');
    setStreamError(null);
    const attachmentsForSend = attachments;
    setAttachments([]);
    setMessages((currentMessages) => [
      ...currentMessages,
      {
        role: 'user',
        content: buildUserContent(prompt, attachmentsForSend),
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
          content: buildUserContent(prompt, attachmentsForSend),
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

      setAttachments(attachmentsForSend);
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

  const appendAttachments = async (files: File[]) => {
    if (files.length === 0) {
      return;
    }

    const remainingSlots = Math.max(MAX_IMAGE_ATTACHMENTS - attachments.length, 0);
    if (remainingSlots === 0) {
      showToast(`You can attach up to ${MAX_IMAGE_ATTACHMENTS} images`, 'error');
      return;
    }

    try {
      const nextAttachments = await Promise.all(
        files.slice(0, remainingSlots).map(fileToPendingAttachment)
      );
      setAttachments((currentAttachments) => [...currentAttachments, ...nextAttachments]);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to add image';
      showToast(message, 'error');
    }
  };

  const handleFileInputChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    await appendAttachments(files);
    event.target.value = '';
  };

  const handleRemoveAttachment = (attachmentIdToRemove: string) => {
    setAttachments((currentAttachments) =>
      currentAttachments.filter((attachment) => attachment.id !== attachmentIdToRemove)
    );
  };

  const handlePaste = async (event: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const items = Array.from(event.clipboardData?.items || []);
    const imageFiles = items
      .filter((item) => item.kind === 'file' && item.type.startsWith('image/'))
      .map((item) => item.getAsFile())
      .filter((file): file is File => file !== null);

    if (imageFiles.length === 0) {
      return;
    }

    event.preventDefault();
    await appendAttachments(imageFiles);
  };

  const handleDragOver = (event: React.DragEvent<HTMLDivElement>) => {
    if (sending) {
      return;
    }

    if (Array.from(event.dataTransfer.items || []).some((item) => item.kind === 'file')) {
      event.preventDefault();
      setDragActive(true);
    }
  };

  const handleDragLeave = (event: React.DragEvent<HTMLDivElement>) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      setDragActive(false);
    }
  };

  const handleDrop = async (event: React.DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setDragActive(false);

    if (sending) {
      return;
    }

    const files = Array.from(event.dataTransfer.files || []).filter((file) =>
      file.type.startsWith('image/')
    );
    await appendAttachments(files);
  };

  const heading = useMemo(() => {
    if (conversation?.summary) {
      return conversation.summary;
    }
    return getGreeting();
  }, [conversation?.summary]);

  return (
    <div className="min-h-screen bg-transparent">
      {sidebarVisible ? (
        <button
          aria-label="Hide sidebar overlay"
          className="fixed inset-0 z-30 bg-black/20 lg:hidden"
          onClick={handleSidebarToggle}
          type="button"
        />
      ) : null}

      <div className={cn('min-h-screen lg:flex', isResizingSidebar && 'select-none')}>
        {sidebarVisible ? (
          <div
            className="fixed inset-y-0 left-0 z-40 w-[min(85vw,360px)] max-w-full shrink-0 lg:sticky lg:top-0 lg:relative lg:z-auto lg:h-screen lg:self-start lg:w-[var(--sidebar-width)]"
            data-testid="chat-sidebar-shell"
            style={{ '--sidebar-width': `${sidebarWidth}px` } as React.CSSProperties}
          >
            <ChatSidebar
              activeConversationId={conversationId}
              conversations={conversations}
              disabled={sending}
              loading={sidebarLoading}
              onHide={handleSidebarToggle}
              onNewChat={handleNewChat}
              onSelectConversation={handleSelectConversation}
            />

            <div
              aria-label="Resize sidebar"
              aria-orientation="vertical"
              className={cn(
                'sidebar-splitter absolute inset-y-0 right-0 z-10 hidden translate-x-1/2 cursor-col-resize items-center justify-center lg:flex',
                isResizingSidebar && 'is-resizing'
              )}
              data-testid="chat-sidebar-resizer"
              onMouseDown={handleSidebarResizeStart}
              role="separator"
              tabIndex={-1}
            >
              <span className="sidebar-splitter-rail" />
              <span className="sidebar-splitter-grip" />
            </div>
          </div>
        ) : null}

        {!sidebarVisible ? (
          <>
            <div
              className="sidebar-collapsed-rail hidden lg:sticky lg:top-0 lg:flex lg:h-screen lg:self-start"
              data-testid="sidebar-collapsed-rail"
            >
              <button
                aria-label="Show panel"
                className="sidebar-toggle-button sidebar-toggle-button-collapsed"
                data-testid="sidebar-attached-toggle"
                disabled={sending}
                onClick={handleSidebarToggle}
                type="button"
              >
                <svg
                  aria-hidden="true"
                  className="h-4 w-4"
                  fill="none"
                  viewBox="0 0 24 24"
                  xmlns="http://www.w3.org/2000/svg"
                >
                  <path
                    d="m9 6 6 6-6 6"
                    stroke="currentColor"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                    strokeWidth="1.8"
                  />
                </svg>
              </button>
            </div>

            <button
              aria-label="Show panel"
              className="sidebar-toggle-button sidebar-toggle-button-mobile lg:hidden"
              data-testid="sidebar-attached-toggle-mobile"
              disabled={sending}
              onClick={handleSidebarToggle}
              type="button"
            >
              <svg
                aria-hidden="true"
                className="h-4 w-4"
                fill="none"
                viewBox="0 0 24 24"
                xmlns="http://www.w3.org/2000/svg"
              >
                <path
                  d="m9 6 6 6-6 6"
                  stroke="currentColor"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth="1.8"
                />
              </svg>
            </button>
          </>
        ) : null}

        <main className="relative flex min-h-screen min-w-0 flex-1 flex-col">
          <header className="border-b border-black/8 px-4 py-5 md:px-8">
            <div className="flex items-start gap-4">
              <div className="min-w-0 max-w-5xl flex-1">
                <p className="eyebrow-label">
                  {conversation ? 'Conversation' : 'Workspace assistant'}
                </p>

                {conversation ? (
                  <>
                    <h2 className="balanced-title conversation-title mt-2 text-3xl font-heading font-bold tracking-tight text-kodelet-dark md:text-4xl">
                      {heading}
                    </h2>
                    <div className="mt-3 flex flex-wrap gap-2 text-kodelet-mid-gray">
                      {conversation.provider ? (
                        <span className="meta-chip">
                          {conversation.provider}
                        </span>
                      ) : null}
                      {conversation.id ? (
                        <span className="meta-chip">
                          {conversation.id}
                        </span>
                      ) : null}
                      {conversation.updatedAt ? (
                        <span className="meta-chip">
                          Updated {formatDate(conversation.updatedAt)}
                        </span>
                      ) : null}
                      {conversation.usage ? (
                        <span className="meta-chip">
                          {formatCost(conversation.usage)}
                        </span>
                      ) : null}
                    </div>
                  </>
                ) : null}
              </div>
            </div>
          </header>

          <div className="flex-1 overflow-y-auto">
            {conversationLoading ? (
              <div className="flex min-h-[50vh] items-center justify-center px-6 py-12">
                <div className="surface-panel rounded-2xl px-6 py-5 text-sm text-kodelet-dark/70">
                  Loading conversation…
                </div>
              </div>
            ) : conversationError ? (
              <div className="px-4 py-8 md:px-8">
                <div className="surface-panel max-w-3xl rounded-3xl border-kodelet-orange/20 px-6 py-5 text-kodelet-dark">
                  <p className="eyebrow-label text-kodelet-orange">
                    Load error
                  </p>
                  <p className="mt-3 text-sm leading-7">{conversationError}</p>
                </div>
              </div>
            ) : (
              <>
                <ChatTranscript
                  emptyStateTitle={heading}
                  isStreaming={sending}
                  messages={messages}
                />
                <div ref={transcriptEndRef} />
              </>
            )}
          </div>

          <div className="border-t border-black/8 bg-[color:var(--kodelet-panel-soft)] px-4 py-4 md:px-8">
            <div className="mx-auto w-full max-w-5xl px-4 md:px-8">
              {streamError ? (
                <div className="surface-panel mb-3 rounded-2xl border-kodelet-orange/20 px-4 py-3 text-sm text-kodelet-dark">
                  {streamError}
                </div>
              ) : null}

              <div
                className={cn(
                  'surface-panel w-full rounded-[1.75rem] p-3',
                  dragActive && 'border-kodelet-blue/35 bg-kodelet-blue/5'
                )}
                onDragLeave={handleDragLeave}
                onDragOver={handleDragOver}
                onDrop={handleDrop}
              >
                <input
                  accept="image/png,image/jpeg,image/gif,image/webp"
                  className="hidden"
                  data-testid="composer-image-input"
                  multiple
                  onChange={handleFileInputChange}
                  ref={fileInputRef}
                  type="file"
                />

                {attachments.length > 0 ? (
                  <div className="mb-3 flex flex-wrap gap-3 px-3 pt-2">
                    {attachments.map((attachment) => (
                      <div
                        key={attachment.id}
                        className="relative overflow-hidden rounded-2xl border border-black/8 bg-kodelet-light/80 p-2"
                      >
                        <img
                          alt={attachment.name}
                          className="h-20 w-20 rounded-xl object-cover"
                          src={attachment.previewUrl}
                        />
                        <button
                          aria-label={`Remove ${attachment.name}`}
                          className="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded-full border border-black/8 bg-white/92 text-xs font-heading font-semibold text-kodelet-dark"
                          onClick={() => handleRemoveAttachment(attachment.id)}
                          type="button"
                        >
                          ×
                        </button>
                      </div>
                    ))}
                  </div>
                ) : null}

                <textarea
                  className="min-h-[88px] w-full resize-none border-0 bg-transparent px-3 py-3 font-body text-base leading-7 text-kodelet-dark outline-none placeholder:text-kodelet-dark/40"
                  disabled={sending}
                  onChange={(event) => setDraft(event.target.value)}
                  onKeyDown={handleDraftKeyDown}
                  onPaste={handlePaste}
                  placeholder="Ask kodelet anything..."
                  value={draft}
                />

                <div className="flex items-center justify-between gap-3 border-t border-black/8 px-3 pt-3">
                  <div className="flex items-center gap-3">
                    <button
                      className="composer-attachment-button"
                      disabled={sending}
                      onClick={() => fileInputRef.current?.click()}
                      type="button"
                    >
                      <svg
                        aria-hidden="true"
                        className="h-4 w-4"
                        fill="none"
                        viewBox="0 0 24 24"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="M12 16.5v-9"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="1.7"
                        />
                        <path
                          d="M7.5 12 12 7.5 16.5 12"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="1.7"
                        />
                        <path
                          d="M5.5 18.5h13"
                          stroke="currentColor"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                          strokeWidth="1.7"
                        />
                      </svg>
                      Add image
                    </button>
                    <p className="eyebrow-label text-kodelet-mid-gray">
                      Enter to send. Shift + Enter for a new line. Paste or drop images.
                    </p>
                  </div>

                  <button
                    className={cn(
                      'primary-pill-button',
                      sending || (!draft.trim() && attachments.length === 0)
                        ? 'cursor-not-allowed bg-kodelet-mid-gray'
                        : 'bg-kodelet-dark hover:bg-black'
                    )}
                    disabled={sending || (!draft.trim() && attachments.length === 0)}
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
