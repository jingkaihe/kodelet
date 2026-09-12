import { fireEvent, render, screen } from '@testing-library/react';
import { marked } from 'marked';
import { afterEach, describe, expect, it } from 'vitest';
import type { ToolResult } from '../../types';
import ThinkingRenderer from './ThinkingRenderer';

const markdownDefaults = { ...marked.defaults };

describe('ThinkingRenderer', () => {
  afterEach(() => {
    marked.setOptions(markdownDefaults);
  });

  it('reveals Markdown with line breaks using a non-submit button', () => {
    const toolResult: ToolResult = {
      toolName: 'thinking',
      success: true,
      metadata: { thought: '**First line**\nSecond line' },
    };
    const { container } = render(<ThinkingRenderer toolResult={toolResult} />);
    const button = screen.getByRole('button', { name: 'Show thinking' });

    expect(button).toHaveAttribute('type', 'button');
    expect(container.querySelector('.tool-detail-panel')).toBeNull();
    fireEvent.click(button);

    expect(container.querySelector('strong')).toHaveTextContent('First line');
    expect(container.querySelector('br')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Show thinking' })).not.toBeInTheDocument();
  });

  it('escapes raw HTML and rejects unsafe links in thinking output', () => {
    const toolResult: ToolResult = {
      toolName: 'thinking',
      success: true,
      metadata: {
        thought:
          '<img src=x onerror="alert(1)">\n\n[unsafe](javascript:alert(1))\n\n[docs](https://example.com)',
      },
    };
    const { container } = render(<ThinkingRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByRole('button', { name: 'Show thinking' }));

    expect(container.querySelector('img')).toBeNull();
    expect(screen.queryByRole('link', { name: 'unsafe' })).not.toBeInTheDocument();
    expect(screen.getByText('unsafe')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'docs' })).toHaveAttribute(
      'href',
      'https://example.com'
    );
  });
});
