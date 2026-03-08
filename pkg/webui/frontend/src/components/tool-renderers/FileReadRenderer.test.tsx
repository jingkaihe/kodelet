import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileReadRenderer from './FileReadRenderer';
import { FileMetadata, ToolResult } from '../../types';

describe('FileReadRenderer', () => {
  const createToolResult = (metadata: Partial<FileMetadata>): ToolResult => ({
    toolName: 'file_read',
    success: true,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as FileMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(
      <FileReadRenderer toolResult={{ ...toolResult, metadata: undefined }} />
    );

    expect(container.firstChild).toBeNull();
  });

  it('renders the file path, language, and line count badge', () => {
    const toolResult = createToolResult({
      filePath: '/src/main.js',
      lines: ['const x = 1;', 'const y = 2;'],
    });

    render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('/src/main.js')).toBeInTheDocument();
    expect(screen.getByText('javascript')).toBeInTheDocument();
    expect(screen.getByText('2 lines')).toBeInTheDocument();
  });

  it('renders code in the shared code block style', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', '', 'line 3', '', ''],
    });

    const { container } = render(<FileReadRenderer toolResult={toolResult} />);

    const codeBlock = container.querySelector('.tool-code-block code');
    expect(codeBlock?.textContent).toBe('line 1\n\nline 3');
  });

  it('shows warning and continuation details for truncated reads', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      lines: ['line 1', 'line 2'],
      offset: 10,
      lineLimit: 2,
      remainingLines: 25,
      truncated: true,
    });

    const { container } = render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('25 more')).toBeInTheDocument();
    expect(screen.getByText('Use offset=12 to continue reading this file.')).toBeInTheDocument();
    expect(container.querySelector('.tool-badge-warning')).toBeInTheDocument();
  });

  it('renders an empty file cleanly', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      lines: [],
    });

    const { container } = render(<FileReadRenderer toolResult={toolResult} />);

    expect(screen.getByText('/empty.txt')).toBeInTheDocument();
    expect(screen.getByText('0 lines')).toBeInTheDocument();
    expect(container.querySelector('.tool-code-block')).toBeInTheDocument();
  });
});
