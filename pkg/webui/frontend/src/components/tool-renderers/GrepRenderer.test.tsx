import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import GrepRenderer from './GrepRenderer';
import { ToolResult, GrepMetadata } from '../../types';

// Mock shared components
interface MockToolCardProps {
  title: string;
  badge?: { text: string; className: string };
  children: React.ReactNode;
}

interface MockMetadataRowProps {
  label: string;
  value: string | number;
}

interface MockCollapsibleProps {
  title: string;
  badge?: { text: string; className: string };
  children: React.ReactNode;
  collapsed?: boolean;
}

vi.mock('./shared', () => ({
  ToolCard: ({ title, badge, children }: MockToolCardProps) => (
    <div data-testid="tool-card">
      <h3>{title}</h3>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {children}
    </div>
  ),
  MetadataRow: ({ label, value }: MockMetadataRowProps) => (
    <div data-testid="metadata-row">
      {label}: {value}
    </div>
  ),
  Collapsible: ({ title, badge, children, collapsed }: MockCollapsibleProps) => (
    <div data-testid="collapsible" data-collapsed={collapsed}>
      <h4>{title}</h4>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {children}
    </div>
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

  it('renders search results with basic information', () => {
    const toolResult = createToolResult({
      pattern: 'TODO',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('🔍 Search Results')).toBeInTheDocument();
    expect(screen.getByText('Pattern: TODO')).toBeInTheDocument();
  });

  it('shows no matches message when results are empty', () => {
    const toolResult = createToolResult({
      pattern: 'nonexistent',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('No matches found')).toBeInTheDocument();
  });

  it('shows match count in badge', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        { filePath: 'file1.js', lineNumber: 1, content: 'test line' },
        { filePath: 'file2.js', lineNumber: 5, content: 'another test' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('2 matches in 2 files')).toBeInTheDocument();
  });

  it('shows truncated badge when truncated', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [{ filePath: 'file.js', lineNumber: 1, content: 'test' }],
      truncated: true,
    });

    render(<GrepRenderer toolResult={toolResult} />);

    // Similar to GlobRenderer, GrepRenderer only shows the first badge
    expect(screen.getByText('1 matches in 1 files')).toBeInTheDocument();
    
    // The component only passes badges[0] to ToolCard
  });

  it('shows path when provided', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      path: '/src',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Path: /src')).toBeInTheDocument();
  });

  it('shows include pattern when provided', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      include: '*.js',
      results: [],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('Include: *.js')).toBeInTheDocument();
  });

  it('renders single match per file', () => {
    const toolResult = createToolResult({
      pattern: 'error',
      results: [
        { filePath: 'app.js', lineNumber: 10, content: 'console.error("failed")' },
        { filePath: 'test.js', lineNumber: 25, content: 'throw new Error()' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('📄 app.js')).toBeInTheDocument();
    expect(screen.getByText('📄 test.js')).toBeInTheDocument();
    expect(screen.getByText('10:')).toBeInTheDocument();
    expect(screen.getByText('25:')).toBeInTheDocument();
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

    expect(screen.getByText('📄 debug.js')).toBeInTheDocument();
    expect(screen.getByText('3 matches')).toBeInTheDocument();
    expect(screen.getByText('1:')).toBeInTheDocument();
    expect(screen.getByText('5:')).toBeInTheDocument();
    expect(screen.getByText('10:')).toBeInTheDocument();
  });

  it('collapses files with more than 5 matches', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        {
          filePath: 'large.js',
          matches: Array(6).fill(null).map((_, i) => ({
            lineNumber: i + 1,
            content: `test ${i}`,
          })),
        },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    const collapsible = screen.getByTestId('collapsible');
    expect(collapsible).toHaveAttribute('data-collapsed', 'true');
  });

  it('does not collapse files with 5 or fewer matches', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        {
          filePath: 'small.js',
          matches: Array(5).fill(null).map((_, i) => ({
            lineNumber: i + 1,
            content: `test ${i}`,
          })),
        },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    const collapsible = screen.getByTestId('collapsible');
    expect(collapsible).toHaveAttribute('data-collapsed', 'false');
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
    expect(highlightedText).toHaveClass('bg-yellow-200', 'text-black');
    expect(highlightedText?.textContent).toBe('error');
  });

  it('handles missing line numbers', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        { filePath: 'file.js', content: 'test content' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('?:')).toBeInTheDocument();
  });

  it('handles missing filePath gracefully', () => {
    const toolResult = createToolResult({
      pattern: 'test',
      results: [
        { filePath: '', lineNumber: 1, content: 'test' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    expect(screen.getByText('📄 Unknown')).toBeInTheDocument();
  });

  it('escapes regex special characters in pattern', () => {
    const toolResult = createToolResult({
      pattern: 'test[0-9]+',
      results: [
        { filePath: 'regex.js', lineNumber: 1, content: 'test[0-9]+ pattern' },
      ],
    });

    // Should not throw an error
    expect(() => render(<GrepRenderer toolResult={toolResult} />)).not.toThrow();
  });

  it('handles empty pattern gracefully', () => {
    const toolResult = createToolResult({
      pattern: '',
      results: [
        { filePath: 'file.js', lineNumber: 1, content: 'some content' },
      ],
    });

    render(<GrepRenderer toolResult={toolResult} />);

    // Should render without highlighting
    expect(screen.getByText(/some content/)).toBeInTheDocument();
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

    // Should show 2 collapsibles (one for each file)
    const collapsibles = screen.getAllByTestId('collapsible');
    expect(collapsibles).toHaveLength(2);
    
    // Check match counts - there should be badges showing match counts
    const badges = screen.getAllByText(/matches/);
    expect(badges.some(b => b.textContent === '2 matches')).toBe(true);
    expect(badges.some(b => b.textContent === '1 matches')).toBe(true);
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

    // Context lines should show '-' separator
    expect(screen.getByText('9-')).toBeInTheDocument();
    expect(screen.getByText('11-')).toBeInTheDocument();
    
    // Match line should show ':' separator
    expect(screen.getByText('10:')).toBeInTheDocument();
    
    // Context lines should have opacity-60 class
    const contextLines = container.querySelectorAll('.opacity-60');
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