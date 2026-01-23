import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import GrepRenderer from './GrepRenderer';
import { ToolResult, GrepMetadata } from '../../types';

// Mock shared components
interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
  ),
}));

describe('GrepRenderer', () => {
  const createToolResult = (metadata: Partial<GrepMetadata>): ToolResult => ({
    toolName: 'grep',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as GrepMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<GrepRenderer toolResult={{ ...toolResult, metadata: undefined }} />);
    
    expect(container.firstChild).toBeNull();
  });

  it('renders pattern', () => {
    const toolResult = createToolResult({
      pattern: 'TODO',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('TODO')).toBeInTheDocument();
  });

  it('shows no matches message when results are empty', () => {
    const toolResult = createToolResult({
      pattern: 'nonexistent',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('No matches found')).toBeInTheDocument();
  });

  it('shows match count', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        { filePath: 'file1.js', lineNumber: 1, content: 'test line' },
        { filePath: 'file2.js', lineNumber: 5, content: 'another test' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('2 in 2 files')).toBeInTheDocument();
  });

  it('shows warning variant when truncated', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [{ filePath: 'file.js', lineNumber: 1, content: 'test' }],
      truncated: true,
    });

    render(<GrepRenderer toolResult={toolResult} />);

    const badge = screen.getByTestId('status-badge');
    expect(badge).toHaveAttribute('data-variant', 'warning');
  });

  it('shows path when provided', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      path: '/src',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('in /src')).toBeInTheDocument();
  });

  it('renders file names', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        { filePath: 'app.js', lineNumber: 10, content: 'console.error("failed")' },
        { filePath: 'test.js', lineNumber: 25, content: 'throw new Error()' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('app.js')).toBeInTheDocument();
    expect(screen.getByText('test.js')).toBeInTheDocument();
  });

  it('renders line numbers', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        { filePath: 'app.js', lineNumber: 10, content: 'console.error("failed")' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('renders multiple matches per file', () => {
    const toolResult = createToolResult({
      pattern: 'log',
      results: [
        {
          filePath: 'debug.js',
          matches: [
            { lineNumber: 1, content: 'console.log("start")' },
            { lineNumber: 5, content: 'console.log("middle")' },
            { lineNumber: 10, content: 'console.log("end")' },
          ],
        },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('debug.js')).toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
  });

  it('shows expand button for files with more than 3 matches', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        {
          filePath: 'large.js',
          matches: Array(5).fill(null).map((_, i) => ({
            lineNumber: i + 1,
            content: `test ${i}`,
          })),
        },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('+3 more')).toBeInTheDocument();
  });

  it('expands to show all matches when button clicked', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        {
          filePath: 'large.js',
          matches: Array(5).fill(null).map((_, i) => ({
            lineNumber: i + 1,
            content: `test ${i}`,
          })),
        },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('+3 more'));

    // All line numbers should be visible
    expect(screen.getByText('5')).toBeInTheDocument();
  });

  it('highlights pattern in content', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        { filePath: 'app.js', lineNumber: 1, content: 'An error occurred' },
      ],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    const highlightedText = container.querySelector('mark');
    expect(highlightedText).toBeInTheDocument();
    expect(highlightedText?.textContent).toBe('error');
  });

  it('handles missing filePath gracefully', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        { filePath: '', lineNumber: 1, content: 'test' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it('escapes regex special characters in pattern', () => {
    const toolResult = createToolResult({
      pattern: 'test[0-9]+',
      results: [
        { filePath: 'regex.js', lineNumber: 1, content: 'test[0-9]+ pattern' },
      ],
    });

    expect(() => render(<GrepRenderer toolResult={toolResult} />)).not.toThrow();
  });

  it('groups multiple results by file', () => {
    const toolResult = createToolResult({
      pattern: 'import',
      results: [
        { filePath: 'index.js', lineNumber: 1, content: 'import React' },
        { filePath: 'index.js', lineNumber: 2, content: 'import { useState }' },
        { filePath: 'app.js', lineNumber: 1, content: 'import styles' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    // Should show file headers
    const indexHeaders = screen.getAllByText('index.js');
    expect(indexHeaders).toHaveLength(1);
    expect(screen.getByText('app.js')).toBeInTheDocument();
  });

  it('renders context lines with different styling', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        {
          filePath: 'app.js',
          matches: [
            { lineNumber: 9, content: 'function handleError() {', isContext: true },
            { lineNumber: 10, content: '  console.error("failed")', isContext: false },
            { lineNumber: 11, content: '}', isContext: true },
          ],
        },
      ],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    // Context lines should have opacity-50 class
    const contextLines = container.querySelectorAll('.opacity-50');
    expect(contextLines).toHaveLength(2);
  });

  it('does not highlight pattern in context lines', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        {
          filePath: 'app.js',
          matches: [
            { lineNumber: 9, content: 'error handling code', isContext: true },
            { lineNumber: 10, content: 'console.error("failed")', isContext: false },
          ],
        },
      ],
    });

    const { container } = render(<GrepRenderer toolResult={toolResult} />);

    // Only the match line should have highlighting
    const highlightedMarks = container.querySelectorAll('mark');
    expect(highlightedMarks).toHaveLength(1);
    expect(highlightedMarks[0].textContent).toBe('error');
  });
});
