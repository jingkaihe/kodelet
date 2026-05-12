import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import ReadConversationRenderer from './ReadConversationRenderer';
import { ToolResult } from '../../types';

describe('ReadConversationRenderer', () => {
  it('renders conversation goal and markdown content', () => {
    const toolResult: ToolResult = {
      toolName: 'read_conversation',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        conversationID: 'conv-123',
        goal: 'Extract the implementation detail',
        content: '## Summary\n\nFound the bug.',
      },
    };

    render(<ReadConversationRenderer toolResult={toolResult} />);

    expect(screen.getByText('conversation loaded')).toBeInTheDocument();
    expect(screen.getByText('conv-123')).toBeInTheDocument();
    expect(screen.getByText('Extract the implementation detail')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Summary' })).toBeInTheDocument();
    expect(screen.getByText('Found the bug.')).toBeInTheDocument();
  });
});
