import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import GlobRenderer from './GlobRenderer';
import { ToolResult, GlobMetadata } from '../../types';

interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
  ),
}));

describe('GlobRenderer', () => {
  const createToolResult = (metadata: Partial<GlobMetadata>): ToolResult => ({
    toolName: 'glob',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as GlobMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<GlobRenderer toolResult={{ ...toolResult, metadata: undefined }} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders pattern', () => {
    const toolResult = createToolResult({
      pattern: '*.js',
      files: [],
    });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('*.js')).toBeInTheDocument();
  });

  it('shows no files message when files are empty', () => {
    const toolResult = createToolResult({
      pattern: '*.txt',
      files: [],
    });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('No files found')).toBeInTheDocument();
  });

  it('shows file count in badge', () => {
    const toolResult = createToolResult({
      pattern: '*',
      files: [
        { path: 'file1.js' },
        { path: 'file2.js' },
        { path: 'file3.js' },
      ],
    });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('3 files')).toBeInTheDocument();
  });

  it('shows warning variant when truncated', () => {
    const toolResult = createToolResult({
      pattern: '*',
      files: [{ path: 'file.js' }],
      truncated: true,
    });

    render(<GlobRenderer toolResult={toolResult} />);
    const badge = screen.getByTestId('status-badge');
    expect(badge).toHaveAttribute('data-variant', 'warning');
  });

  it('shows path when provided', () => {
    const toolResult = createToolResult({
      pattern: '*.js',
      path: '/src',
      files: [],
    });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('in /src')).toBeInTheDocument();
  });

  it('renders file names', () => {
    const toolResult = createToolResult({
      pattern: '*',
      files: [
        { path: 'script.js' },
        { path: 'image.png' },
        { path: 'readme.md' },
      ],
    });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('script.js')).toBeInTheDocument();
    expect(screen.getByText('image.png')).toBeInTheDocument();
    expect(screen.getByText('readme.md')).toBeInTheDocument();
  });

  it('shows expand button for more than 10 files', () => {
    const files = Array(15).fill(null).map((_, i) => ({ path: `file${i}.js` }));
    const toolResult = createToolResult({ pattern: '*', files });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('+7 more files')).toBeInTheDocument();
  });

  it('expands to show all files when button clicked', () => {
    const files = Array(15).fill(null).map((_, i) => ({ path: `file${i}.js` }));
    const toolResult = createToolResult({ pattern: '*', files });

    render(<GlobRenderer toolResult={toolResult} />);
    fireEvent.click(screen.getByText('+7 more files'));
    expect(screen.getByText('file14.js')).toBeInTheDocument();
  });

  it('handles files with name property instead of path', () => {
    const toolResult = createToolResult({
      pattern: '*',
      files: [
        { path: '', name: 'file1.js' },
        { path: '', name: 'file2.js' },
      ],
    });

    render(<GlobRenderer toolResult={toolResult} />);
    expect(screen.getByText('file1.js')).toBeInTheDocument();
    expect(screen.getByText('file2.js')).toBeInTheDocument();
  });
});
