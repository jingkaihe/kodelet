import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import ConversationHeader from './ConversationHeader';
import { Conversation } from '../types';

describe('ConversationHeader', () => {
  const mockConversation: Conversation = {
    id: 'conv-1234567890',
    messages: [],
    toolResults: {},
    usage: {},
    createdAt: '2023-01-01T00:00:00Z',
    updatedAt: '2023-01-01T00:00:00Z',
    messageCount: 0,
    summary: 'This is a test conversation summary',
  };

  const mockOnExport = vi.fn();
  const mockOnDelete = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('displays conversation information', () => {
    render(
      <ConversationHeader
        conversation={mockConversation}
        onExport={mockOnExport}
        onDelete={mockOnDelete}
      />
    );

    expect(screen.getByText('This is a test conversation summary')).toBeInTheDocument();
    expect(screen.getByText('conv-1234567890')).toBeInTheDocument();
  });

  it('shows ID as heading when no summary is available', () => {
    const conversationWithoutSummary = {
      ...mockConversation,
      summary: undefined,
    };

    render(
      <ConversationHeader
        conversation={conversationWithoutSummary}
        onExport={mockOnExport}
        onDelete={mockOnDelete}
      />
    );

    expect(screen.getByRole('heading', { name: 'conv-1234567890' })).toBeInTheDocument();
  });

  it('allows user to export conversation', () => {
    render(
      <ConversationHeader
        conversation={mockConversation}
        onExport={mockOnExport}
        onDelete={mockOnDelete}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /export/i }));
    expect(mockOnExport).toHaveBeenCalledTimes(1);
  });

  it('allows user to delete conversation', () => {
    render(
      <ConversationHeader
        conversation={mockConversation}
        onExport={mockOnExport}
        onDelete={mockOnDelete}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /delete/i }));
    expect(mockOnDelete).toHaveBeenCalledTimes(1);
  });

  it('disables actions during loading state', () => {
    const loadingConversation = {
      ...mockConversation,
      id: '',
      summary: undefined,
    };

    render(
      <ConversationHeader
        conversation={loadingConversation}
        onExport={mockOnExport}
        onDelete={mockOnDelete}
      />
    );

    expect(screen.getByRole('heading', { name: 'Loading...' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /export/i })).toBeDisabled();
    expect(screen.getByRole('button', { name: /delete/i })).toBeDisabled();
  });
});