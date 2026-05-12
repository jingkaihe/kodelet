import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import WebFetchRenderer from './WebFetchRenderer';
import { ToolResult } from '../../types';

describe('WebFetchRenderer', () => {
  it('uses a polished extracted-summary label and renders markdown content', () => {
    const toolResult: ToolResult = {
      toolName: 'web_fetch',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        url: 'https://example.com/news',
        processedType: 'ai_extracted',
        contentType: 'text/html',
        size: 3072,
        content: '## Top stories\n\n| Rank | Headline |\n|---:|---|\n| 1 | [Story one](https://example.com/story) |',
      },
    };

    const { container } = render(<WebFetchRenderer toolResult={toolResult} />);

    expect(screen.getByText('extracted summary')).toBeInTheDocument();
    expect(screen.queryByText('ai extracted')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Top stories' })).toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Story one' })).toHaveAttribute(
      'href',
      'https://example.com/story'
    );
    expect(container.querySelector('.tool-compact-markdown.web-fetch-content')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block')).not.toBeInTheDocument();
  });
});
