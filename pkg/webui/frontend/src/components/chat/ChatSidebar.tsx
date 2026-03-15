import React from 'react';
import type { Conversation } from '../../types';
import { cn, truncateText } from '../../utils';

interface ChatSidebarProps {
  conversations: Conversation[];
  activeConversationId: string | null;
  loading: boolean;
  disabled?: boolean;
  onHide?: () => void;
  onNewChat: () => void;
  onSelectConversation: (conversationId: string) => void;
  onForkConversation: (conversationId: string) => void;
  onDeleteConversation: (conversationId: string) => void;
}

const previewConversation = (conversation: Conversation): string => {
  return (
    conversation.summary ||
    conversation.preview ||
    conversation.firstMessage ||
    'Untitled conversation'
  );
};

const ChatSidebar: React.FC<ChatSidebarProps> = ({
  conversations,
  activeConversationId,
  loading,
  disabled = false,
  onHide,
  onNewChat,
  onSelectConversation,
  onForkConversation,
  onDeleteConversation,
}) => {
  const [openMenuConversationId, setOpenMenuConversationId] = React.useState<string | null>(null);
  const menuRef = React.useRef<HTMLDivElement | null>(null);

  React.useEffect(() => {
    if (!openMenuConversationId) {
      return undefined;
    }

    const handlePointerDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) {
        setOpenMenuConversationId(null);
      }
    };

    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpenMenuConversationId(null);
      }
    };

    document.addEventListener('mousedown', handlePointerDown);
    document.addEventListener('keydown', handleEscape);

    return () => {
      document.removeEventListener('mousedown', handlePointerDown);
      document.removeEventListener('keydown', handleEscape);
    };
  }, [openMenuConversationId]);

  return (
    <aside className="relative overflow-visible border-b border-black/8 bg-kodelet-light-gray px-6 py-6 lg:flex lg:h-screen lg:flex-col lg:border-b-0 lg:border-r">
      {onHide ? (
        <button
          aria-label="Hide panel"
          className="sidebar-toggle-button sidebar-toggle-button-open"
          data-testid="sidebar-hide-button"
          disabled={disabled}
          onClick={onHide}
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
              d="M15 6 9 12l6 6"
              stroke="currentColor"
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth="1.8"
            />
          </svg>
        </button>
      ) : null}

      <div className="min-h-0 flex-1 pt-8 lg:pt-2">
        <button
          className="sidebar-action-link"
          disabled={disabled}
          onClick={onNewChat}
          type="button"
        >
          <span className="sidebar-action-icon">
            <span className="sidebar-action-plus">+</span>
          </span>
          <span className="sidebar-action-label">New chat</span>
        </button>

        <div className="sidebar-section-title">Recents</div>

        <div className="conversation-list max-h-[calc(100vh-13.5rem)] overflow-y-auto pr-1">
          {conversations.length === 0 && !loading ? (
            <div className="px-2 py-2 text-sm text-kodelet-dark/65">
              No saved conversations yet.
            </div>
          ) : null}

          {loading ? <div className="px-2 py-2 text-sm text-kodelet-dark/65">Loading…</div> : null}

          {conversations.map((conversation) => {
            const isActive = conversation.id === activeConversationId;
            const isMenuOpen = conversation.id === openMenuConversationId;
            const preview = previewConversation(conversation);

            return (
              <div
                key={conversation.id}
                className={cn('conversation-link-row', isActive && 'active', isMenuOpen && 'menu-open')}
                ref={isMenuOpen ? menuRef : undefined}
              >
                <button
                  className={cn('conversation-link', isActive && 'active')}
                  disabled={disabled}
                  onClick={() => {
                    setOpenMenuConversationId(null);
                    onSelectConversation(conversation.id);
                  }}
                  type="button"
                >
                  <span className="conversation-link-title">
                    {truncateText(preview, 80)}
                  </span>
                </button>

                <div className="conversation-actions">
                  <button
                    aria-expanded={isMenuOpen}
                    aria-haspopup="menu"
                    aria-label={`More actions for ${preview}`}
                    className="conversation-link-more-button"
                    disabled={disabled}
                    onClick={() => {
                      setOpenMenuConversationId((currentId) =>
                        currentId === conversation.id ? null : conversation.id
                      );
                    }}
                    type="button"
                  >
                    <span className="conversation-link-more">•••</span>
                  </button>

                  {isMenuOpen ? (
                    <div className="conversation-action-menu" role="menu">
                      <button
                        className="conversation-action-menu-item"
                        onClick={() => {
                          setOpenMenuConversationId(null);
                          onForkConversation(conversation.id);
                        }}
                        role="menuitem"
                        type="button"
                      >
                        Copy
                      </button>
                      <button
                        className="conversation-action-menu-item danger"
                        onClick={() => {
                          setOpenMenuConversationId(null);
                          onDeleteConversation(conversation.id);
                        }}
                        role="menuitem"
                        type="button"
                      >
                        Delete
                      </button>
                    </div>
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      <div className="sidebar-footer">
        {activeConversationId ? (
          <div className="sidebar-caption">
            ID: <code>{activeConversationId}</code>
          </div>
        ) : null}
        <div className="sidebar-caption">
          Mode: <code>kodelet serve</code>
        </div>
      </div>
    </aside>
  );
};

export default ChatSidebar;
