import React from 'react';
import type { Conversation } from '../../types';
import { cn, truncateText } from '../../utils';

interface ChatSidebarProps {
  conversations: Conversation[];
  activeConversationId: string | null;
  loading: boolean;
  disabled?: boolean;
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
  onNewChat,
  onSelectConversation,
}) => {
  return (
    <aside className="border-b border-black/8 bg-kodelet-light-gray px-4 py-5 lg:min-h-screen lg:border-b-0 lg:border-r">
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

      <div className="conversation-list max-h-[calc(100vh-8rem)] overflow-y-auto">
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
    </aside>
  );
};

export default ChatSidebar;
