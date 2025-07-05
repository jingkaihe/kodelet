import React from 'react';
import { Link } from 'react-router-dom';
import { Conversation } from '../types';
import { formatDate } from '../utils';
import LoadingSpinner from './LoadingSpinner';

interface ConversationListProps {
  conversations: Conversation[];
  loading: boolean;
  hasMore: boolean;
  onLoadMore: () => void;
  onDelete: (conversationId: string) => void;
}

const ConversationList: React.FC<ConversationListProps> = ({
  conversations,
  loading,
  hasMore,
  onLoadMore,
  onDelete,
}) => {
  const handleDeleteClick = (e: React.MouseEvent, conversationId: string) => {
    e.preventDefault();
    e.stopPropagation();
    onDelete(conversationId);
  };

  return (
    <div className="space-y-4">
      {/* Conversation Cards */}
      {conversations.map((conversation) => (
        <div 
          key={conversation.id} 
          className="card bg-base-100 shadow-lg hover:shadow-xl transition-shadow duration-200"
        >
          <div className="card-body">
            <div className="flex justify-between items-start">
              <div className="flex-1">
                <h3 className="card-title text-lg mb-2">
                  <Link
                    to={`/c/${conversation.id}`}
                    className="link link-hover text-primary"
                  >
                    {conversation.id.substring(0, 8)}...
                  </Link>
                </h3>
                <p className="text-base-content/70 mb-3">
                  {conversation.preview || conversation.summary || 'No preview available'}
                </p>

                <div className="flex flex-wrap gap-2 text-sm text-base-content/60">
                  <div className="badge badge-outline">
                    <span>{conversation.messageCount}</span> messages
                  </div>
                  <div className="badge badge-outline">
                    Created: <span>{formatDate(conversation.createdAt || conversation.created_at)}</span>
                  </div>
                  <div className="badge badge-outline">
                    Updated: <span>{formatDate(conversation.updatedAt || conversation.updated_at)}</span>
                  </div>
                  {conversation.modelType && (
                    <div className="badge badge-outline">
                      Model: <span>{conversation.modelType}</span>
                    </div>
                  )}
                </div>
              </div>

              <div className="dropdown dropdown-end">
                <div 
                  tabIndex={0} 
                  role="button" 
                  className="btn btn-ghost btn-sm"
                  aria-label="Conversation actions"
                >
                  <svg 
                    xmlns="http://www.w3.org/2000/svg" 
                    className="h-5 w-5" 
                    fill="none" 
                    viewBox="0 0 24 24" 
                    stroke="currentColor"
                    aria-hidden="true"
                  >
                    <path 
                      strokeLinecap="round" 
                      strokeLinejoin="round" 
                      strokeWidth="2" 
                      d="M12 5v.01M12 12v.01M12 19v.01M12 6a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2z" 
                    />
                  </svg>
                </div>
                <ul 
                  tabIndex={0} 
                  className="dropdown-content menu bg-base-100 rounded-box z-[1] w-52 p-2 shadow"
                >
                  <li>
                    <Link to={`/c/${conversation.id}`}>View</Link>
                  </li>
                  <li>
                    <button
                      className="text-error"
                      onClick={(e) => handleDeleteClick(e, conversation.id)}
                    >
                      Delete
                    </button>
                  </li>
                </ul>
              </div>
            </div>
          </div>
        </div>
      ))}

      {/* Load More Button */}
      {hasMore && (
        <div className="flex justify-center mt-8">
          <button
            className="btn btn-primary"
            onClick={onLoadMore}
            disabled={loading}
          >
            {loading ? (
              <>
                <span className="loading loading-spinner loading-sm"></span>
                Loading...
              </>
            ) : (
              'Load More'
            )}
          </button>
        </div>
      )}

      {/* Loading Indicator for Load More */}
      {loading && conversations.length > 0 && (
        <div className="text-center py-4">
          <LoadingSpinner size="md" message="Loading more conversations..." />
        </div>
      )}
    </div>
  );
};

export default ConversationList;