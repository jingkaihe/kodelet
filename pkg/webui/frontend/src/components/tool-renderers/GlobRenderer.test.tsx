import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import GlobRenderer from './GlobRenderer';
import { GlobMetadata, ToolResult } from '../../types';

describe('GlobRenderer', () => {
  const createToolResult = (metadata: Partial<GlobMetadata>): ToolResult => ({
    toolName: 'glob_tool',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as GlobMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<GlobRenderer toolResult={{ ...toolResult, metadata: undefined }} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders the glob summary and file metadata', () => {
    const toolResult = createToolResult({
      pattern: '*.ts',
      path: '/src',
      files: [
        { path: 'src/app.ts', type: 'file', size: 1024, language: 'typescript' },
        { path: 'src/lib', type: 'directory', size: 0 },
      ],
    });

    render(<GlobRenderer toolResult={toolResult} />);

    expect(screen.getByText('*.ts')).toBeInTheDocument();
    expect(screen.getByText('2 entries')).toBeInTheDocument();
    expect(screen.getByText('/src')).toBeInTheDocument();
    expect(screen.getByText('src/app.ts')).toBeInTheDocument();
    expect(screen.getByText('file · 1.0 KB · typescript')).toBeInTheDocument();
    expect(screen.getByText('src/lib')).toBeInTheDocument();
    expect(screen.getByText('directory')).toBeInTheDocument();
  });

  it('shows a warning badge when the results are truncated', () => {
    const toolResult = createToolResult({
      pattern: '*',
      truncated: true,
      files: [{ path: 'file.ts' }],
    });

    const { container } = render(<GlobRenderer toolResult={toolResult} />);

    expect(container.querySelector('.tool-badge-warning')).toBeInTheDocument();
  });

  it('shows a note when more than 24 files are matched', () => {
    const toolResult = createToolResult({
      pattern: '*',
      files: Array.from({ length: 25 }, (_, index) => ({ path: `file${index}.ts` })),
    });

    render(<GlobRenderer toolResult={toolResult} />);

    expect(screen.getByText('Showing first 24 of 25 matched entries.')).toBeInTheDocument();
  });

  it('shows an empty state when no files are found', () => {
    const toolResult = createToolResult({
      pattern: '*.txt',
      files: [],
    });

    render(<GlobRenderer toolResult={toolResult} />);

    expect(screen.getByText('No files found')).toBeInTheDocument();
  });
});
