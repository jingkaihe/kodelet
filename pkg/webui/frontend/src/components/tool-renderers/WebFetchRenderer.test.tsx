import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import WebFetchRenderer from './WebFetchRenderer';
import { ToolResult } from '../../types';

describe('WebFetchRenderer', () => {
  it('uses a polished extracted-summary label and renders fetched content as escaped code', () => {
    const toolResult: ToolResult = {
      toolName: 'web_fetch',
      success: true,
      timestamp: '2026-05-12T00:00:00Z',
      metadata: {
        url: 'https://example.com/news',
        processedType: 'ai_extracted',
        contentType: 'text/html',
        size: 3072,
        content: '## Top stories\n\n<img src=x onerror=alert(1)>\n\n[Story one](javascript:alert(1))',
      },
    };

    const { container } = render(<WebFetchRenderer toolResult={toolResult} />);

    expect(screen.getByText('extracted summary')).toBeInTheDocument();
    expect(screen.queryByText('ai extracted')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Top stories' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Story one' })).not.toBeInTheDocument();
    expect(container.querySelector('.tool-compact-markdown.web-fetch-content')).not.toBeInTheDocument();
    expect(container.querySelector('.web-fetch-code-preview')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block')).toBeInTheDocument();
    expect(container.querySelector('img')).not.toBeInTheDocument();
    expect(screen.getByText(/<img src=x onerror=alert\(1\)>/)).toBeInTheDocument();
    expect(screen.getByText(/\[Story one\]\(javascript:alert\(1\)\)/)).toBeInTheDocument();
  });
});
