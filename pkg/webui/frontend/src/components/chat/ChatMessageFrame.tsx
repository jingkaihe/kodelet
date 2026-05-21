import React from 'react';
import { SquareTerminal, UserRound } from 'lucide-react';
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
          'chat-message-panel group w-full rounded-[1.5rem]',
          isUser ? 'px-5 py-4' : 'px-5 py-5'
        )}
      >
        <div className="mb-4 flex items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <div
              aria-hidden="true"
              className={cn(
                'message-avatar',
                isUser ? 'message-avatar-user' : 'message-avatar-kodelet'
              )}
            >
              {isUser ? (
                <UserRound className="h-5 w-5" strokeWidth={1.8} />
              ) : (
                <SquareTerminal className="h-5 w-5" strokeWidth={1.9} />
              )}
            </div>
            <div>
              <p className="font-heading text-sm font-semibold tracking-tight text-kodelet-dark">
                {getRoleLabel(role)}
              </p>
            </div>
          </div>

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
