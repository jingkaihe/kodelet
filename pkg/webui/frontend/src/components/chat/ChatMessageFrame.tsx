import React from 'react';
import { cn } from '../../utils';
import { CopyButton } from '../tool-renderers/shared';

type ChatMessageRole = 'user' | 'assistant';

interface ChatMessageFrameProps {
  children: React.ReactNode;
  copyText?: string;
  role: ChatMessageRole;
}

const messageCopyButtonBaseClassName =
  'pointer-events-none px-3 py-2 opacity-0 transition-opacity duration-200 focus-visible:pointer-events-auto focus-visible:opacity-100';

const userMessageCopyButtonClassName = `${messageCopyButtonBaseClassName} group-hover:pointer-events-auto group-hover:opacity-100 group-focus-within:pointer-events-auto group-focus-within:opacity-100`;

const getRoleLabel = (role: ChatMessageRole): string =>
  role === 'user' ? 'You' : 'Kodelet';

const ChatMessageFrame: React.FC<ChatMessageFrameProps> = ({
  children,
  copyText = '',
  role,
}) => {
  const isUser = role === 'user';

  return (
    <article className="w-full">
      <div
        className={cn(
          'chat-message-panel group w-full',
          isUser ? 'chat-message-panel-user' : 'chat-message-panel-assistant'
        )}
      >
        <div className="chat-message-heading">
          <p className="chat-message-role">
            <span aria-hidden="true">{isUser ? '›' : '·'}</span> {getRoleLabel(role)}
          </p>

          {isUser && copyText.trim() ? (
            <CopyButton
              className={userMessageCopyButtonClassName}
              content={copyText}
            />
          ) : null}
        </div>

        {children}
      </div>
    </article>
  );
};

export default ChatMessageFrame;
