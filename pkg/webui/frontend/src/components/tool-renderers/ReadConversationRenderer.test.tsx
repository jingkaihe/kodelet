import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { ToolResult } from '../../types';
import ReadConversationRenderer from './ReadConversationRenderer';

describe('ReadConversationRenderer', () => {
  it('renders a compact conversation summary', () => {
    const toolResult: ToolResult = {
      toolName: 'read_conversation',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        conversationID: 'conv-123',
        goal: 'Extract the implementation detail',
        content: '# Saved conversation summary\n\n## Details\n\nFound the bug.',
      },
    };

    render(<ReadConversationRenderer toolResult={toolResult} />);

    expect(screen.getByText('conversation loaded')).toBeInTheDocument();
    expect(screen.getByText('conv-123')).toBeInTheDocument();
    expect(screen.getByText('goal')).toBeInTheDocument();
    expect(screen.getByText('Extract the implementation detail')).toBeInTheDocument();
    expect(
      screen.queryByRole('heading', { name: 'Saved conversation summary' })
    ).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Details' })).toBeInTheDocument();
    expect(screen.getByText('Found the bug.')).toBeInTheDocument();
  });

  it('hides generated default goals', () => {
    const toolResult: ToolResult = {
      toolName: 'read_conversation',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        conversationId: 'conv-456',
        goal: 'Summarize what this saved conversation contains: include implementation details.',
        content: 'Useful implementation detail.',
      },
    };

    render(<ReadConversationRenderer toolResult={toolResult} />);

    expect(screen.getByText('conv-456')).toBeInTheDocument();
    expect(
      screen.queryByText(/Summarize what this saved conversation contains/i)
    ).not.toBeInTheDocument();
    expect(screen.getByText('Useful implementation detail.')).toBeInTheDocument();
  });

  it('escapes raw HTML and rejects unsafe links in conversation summaries', () => {
    const toolResult: ToolResult = {
      toolName: 'read_conversation',
      success: true,
      metadata: {
        conversationID: 'conv-123',
        content:
          '<img src=x onerror="alert(1)">\n\n[unsafe](jav&#x61;script&colon;alert(1))\n\n**Safe** [docs](https://example.com)',
      },
    };

    const { container } = render(<ReadConversationRenderer toolResult={toolResult} />);

    expect(container.querySelector('img')).toBeNull();
    expect(screen.queryByRole('link', { name: 'unsafe' })).not.toBeInTheDocument();
    expect(screen.getByText('unsafe')).toBeInTheDocument();
    expect(container.querySelector('strong')).toHaveTextContent('Safe');
    expect(screen.getByRole('link', { name: 'docs' })).toHaveAttribute(
      'href',
      'https://example.com'
    );
  });
});
