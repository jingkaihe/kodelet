import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import type { GrepMetadata, ToolResult } from '../../types';
import GrepRenderer from './GrepRenderer';

describe('GrepRenderer', () => {
  const createToolResult = (metadata: Partial<GrepMetadata>): ToolResult => ({
    toolName: 'grep_tool',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as GrepMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(
      <GrepRenderer toolResult={{ ...toolResult, metadata: undefined }} />
    );

    expect(container.firstChild).toBeNull();
  });

  it('renders the search summary and per-file matches', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      path: '/src',
      include: '*.ts',
      results: [
        {
          filePath: 'app.ts',
          matches: [
            { lineNumber: 10, content: 'console.error("failed")' },
            { lineNumber: 11, content: 'return error' },
          ],
        },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('2 matches')).toBeInTheDocument();
    expect(screen.getByText('1 files')).toBeInTheDocument();
    expect(screen.getByText('/src')).toBeInTheDocument();
    expect(screen.getByText('*.ts')).toBeInTheDocument();
    expect(screen.getByText('app.ts')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('highlights matched text and keeps context lines muted', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        {
          filePath: 'app.ts',
          matches: [
            { lineNumber: 9, content: 'function handleError() {', isContext: true },
            { lineNumber: 10, content: '  console.error("failed")' },
          ],
        },
      ],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    expect(container.querySelector('mark.grep-mark')?.textContent).toBe('error');
    expect(container.querySelector('.grep-line.context')).toBeInTheDocument();
  });

  it('supports flat fallback results and unknown files', () => {
    const toolResult = createToolResult({
      pattern: 'TODO',
      results: [{ filePath: '', lineNumber: 1, content: 'TODO: follow up' }],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(container.querySelector('.grep-line')?.textContent).toContain('TODO: follow up');
  });

  it.each([
    '[todo]',
    '&',
    '.*',
    '',
  ])('renders HTML as text while highlighting literal %j', (pattern) => {
    const content = '[todo] & .* <img src=x onerror="alert(1)">';
    const toolResult = createToolResult({
      pattern,
      results: [{ filePath: 'app.ts', matches: [{ lineNumber: 1, content }] }],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);
    const line = container.querySelector('.grep-line > span:last-child');

    expect(line?.textContent).toBe(content);
    expect(line?.querySelectorAll('mark')).toHaveLength(pattern ? 1 : 0);
    if (pattern) {
      expect(line?.querySelector('mark')).toHaveTextContent(pattern);
    }
    expect(container.querySelector('img')).toBeNull();
  });

  it('highlights every occurrence without changing its case', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [{ filePath: 'app.ts', matches: [{ lineNumber: 1, content: 'Error error ERROR' }] }],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    expect(Array.from(container.querySelectorAll('mark'), (mark) => mark.textContent)).toEqual([
      'Error',
      'error',
      'ERROR',
    ]);
  });

  it('keeps flat results from the same file keyed by source line when reordered', () => {
    const results = [
      { filePath: 'app.ts', lineNumber: 1, content: 'TODO: first' },
      { filePath: 'app.ts', lineNumber: 2, content: 'TODO: second' },
    ];
    const { container, rerender } = render(
      <GrepRenderer toolResult={createToolResult({ pattern: 'TODO', results })} />
    );
    const blocks = container.querySelectorAll('.grep-block');

    rerender(
      <GrepRenderer
        toolResult={createToolResult({ pattern: 'TODO', results: [...results].reverse() })}
      />
    );

    const reordered = container.querySelectorAll('.grep-block');
    expect(reordered[0]).toBe(blocks[1]);
    expect(reordered[1]).toBe(blocks[0]);
  });

  it('renders truncation details when results are capped', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      truncated: true,
      truncationReason: 'file_limit',
      maxResults: 25,
      results: [{ filePath: 'file.ts', matches: [{ lineNumber: 1, content: 'test value' }] }],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Truncated: max 25 files')).toBeInTheDocument();
    expect(container.querySelector('.quiet-tool-warning')).toBeInTheDocument();
  });

  it('shows an empty state when no matches exist', () => {
    const toolResult = createToolResult({
      pattern: 'nonexistent',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('No matches found')).toBeInTheDocument();
  });
});
