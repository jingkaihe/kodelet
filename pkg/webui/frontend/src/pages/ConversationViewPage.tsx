import React from 'react';
import { useParams, Link } from 'react-router-dom';
import { useConversation } from '../hooks/useConversation';
import ConversationHeader from '../components/ConversationHeader';
import ConversationMetadata from '../components/ConversationMetadata';
import MessageList from '../components/MessageList';
import LoadingSpinner from '../components/LoadingSpinner';
import ErrorAlert from '../components/ErrorAlert';
import EmptyState from '../components/EmptyState';
import { showToast } from '../utils';

const ConversationViewPage: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const conversationId = id || '';

  const {
    conversation,
    loading,
    error,
    deleteConversation,
    exportConversation,
    refresh,
  } = useConversation(conversationId);

  const handleDeleteConversation = async () => {
    if (!confirm('Are you sure you want to delete this conversation?')) {
      return;
    }

    try {
      await deleteConversation();
      showToast('Conversation deleted successfully', 'success');
    } catch (err) {
      showToast(`Failed to delete conversation: ${err instanceof Error ? err.message : 'Unknown error'}`, 'error');
    }
  };

  if (loading) {
    return <LoadingSpinner message="Loading conversation..." />;
  }

  if (error) {
    return (
      <div className="container mx-auto px-4 py-8">
        <div className="mb-4">
          <Link to="/" className="link link-hover">← Back to Conversations</Link>
        </div>
        <ErrorAlert message={error} onRetry={refresh} />
      </div>
    );
  }

  if (!conversation) {
    return (
      <div className="container mx-auto px-4 py-8">
        <div className="mb-4">
          <Link to="/" className="link link-hover">← Back to Conversations</Link>
        </div>
        <EmptyState
          icon="💭"
          title="Conversation not found"
          description="The conversation you're looking for doesn't exist or has been deleted"
        />
      </div>
    );
  }

  return (
    <div className="container mx-auto px-4 py-8">
      {/* Breadcrumb */}
      <div className="breadcrumbs text-sm mb-6">
        <ul>
          <li><Link to="/" className="link link-hover">Conversations</Link></li>
          <li className="text-base-content/70">{conversation.id}</li>
        </ul>
      </div>

      {/* Header */}
      <ConversationHeader
        conversation={conversation}
        onExport={exportConversation}
        onDelete={handleDeleteConversation}
      />

      {/* Metadata */}
      <ConversationMetadata conversation={conversation} />

      {/* Messages */}
      <MessageList 
        messages={conversation.messages || []}
        toolResults={conversation.toolResults || {}}
      />

      {/* Empty State for no messages */}
      {(!conversation.messages || conversation.messages.length === 0) && (
        <EmptyState
          icon="💬"
          title="No messages found"
          description="This conversation appears to be empty"
        />
      )}
    </div>
  );
};

export default ConversationViewPage;