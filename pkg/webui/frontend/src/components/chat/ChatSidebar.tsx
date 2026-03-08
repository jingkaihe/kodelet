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
}) => {
  return (
    <aside className="relative overflow-visible border-b border-black/8 bg-kodelet-light-gray px-4 py-5 lg:flex lg:min-h-screen lg:flex-col lg:border-b-0 lg:border-r">
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

      <div className="min-h-0 flex-1">
        <button
          className="sidebar-action-link"
          disabled={disabled}
          onClick={onNewChat}
          type="button"
        >
          <span className="sidebar-action-plus">+</span>
          <span className="sidebar-action-label">New chat</span>
        </button>

        <div className="sidebar-section-title">Recents</div>

        <div className="conversation-list max-h-[calc(100vh-12rem)] overflow-y-auto">
          {conversations.length === 0 && !loading ? (
            <div className="px-2 py-2 text-sm text-kodelet-dark/65">
              No saved conversations yet.
            </div>
          ) : null}

          {loading ? <div className="px-2 py-2 text-sm text-kodelet-dark/65">Loading…</div> : null}

          {conversations.map((conversation) => {
            const isActive = conversation.id === activeConversationId;

            return (
              <button
                key={conversation.id}
                className={cn(
                  'conversation-link',
                  isActive && 'active'
                )}
                disabled={disabled}
                onClick={() => onSelectConversation(conversation.id)}
                type="button"
              >
                <span className="conversation-link-title">
                  {truncateText(previewConversation(conversation), 80)}
                </span>
                <span className="conversation-link-more">•••</span>
              </button>
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
