import React from 'react';
import { Conversation } from '../types';

interface ConversationHeaderProps {
  conversation: Conversation;
  onExport: () => void;
  onDelete: () => void;
}

const ConversationHeader: React.FC<ConversationHeaderProps> = ({
  conversation,
  onExport,
  onDelete,
}) => {
  return (
    <div className="mb-6 animate-slide-up">
      <div className="flex items-start justify-between gap-4">
        <div className="flex-1">
          <h1 className="text-2xl font-heading font-bold text-kodelet-dark tracking-tight leading-tight">
            {conversation.summary || conversation.id || 'Loading...'}
          </h1>
          {conversation.summary && conversation.id && (
            <p className="text-xs font-mono text-kodelet-mid-gray mt-1">
              {conversation.id}
            </p>
          )}
        </div>

        <div className="flex gap-3">
          <button
            className="btn btn-sm bg-kodelet-blue text-white hover:bg-kodelet-dark border-none font-heading gap-2 transition-all"
            onClick={onExport}
            disabled={!conversation.id}
            aria-label="Export conversation"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth="2"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M12 10v6m0 0l-3-3m3 3l3-3m2 8H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
              />
            </svg>
            Export
          </button>
          <button
            className="btn btn-sm bg-kodelet-orange text-white hover:bg-kodelet-dark border-none font-heading gap-2 transition-all"
            onClick={onDelete}
            disabled={!conversation.id}
            aria-label="Delete conversation"
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
              strokeWidth="2"
              aria-hidden="true"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
              />
            </svg>
            Delete
          </button>
        </div>
      </div>
    </div>
  );
};

export default ConversationHeader;