import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import GrepRenderer from './GrepRenderer';
import { GrepMetadata, ToolResult } from '../../types';

describe('GrepRenderer', () => {
  const createToolResult = (metadata: Partial<GrepMetadata>): ToolResult => ({
    toolName: 'grep_tool',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as GrepMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<GrepRenderer toolResult={{ ...toolResult, metadata: undefined }} />);

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

    expect(screen.getByText(':: Search Results')).toBeInTheDocument();
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
      results: [
        { filePath: '', lineNumber: 1, content: 'TODO: follow up' },
      ],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(container.querySelector('.grep-line')?.textContent).toContain('TODO: follow up');
  });

  it('renders truncation details when results are capped', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      truncated: true,
      truncationReason: 'file_limit',
      maxResults: 25,
      results: [
        { filePath: 'file.ts', matches: [{ lineNumber: 1, content: 'test value' }] },
      ],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Truncated: max 25 files')).toBeInTheDocument();
    expect(container.querySelector('.tool-badge-warning')).toBeInTheDocument();
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
