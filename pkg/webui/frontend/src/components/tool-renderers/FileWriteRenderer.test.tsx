import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileWriteRenderer from './FileWriteRenderer';
import { ToolResult } from '../../types';

// Mock shared components
interface MockToolCardProps {
  title: string;
  badge?: { text: string; className: string };
  actions?: React.ReactNode;
  children: React.ReactNode;
}

interface MockCopyButtonProps {
  content: string;
}

interface MockMetadataRowProps {
  label: string;
  value: string | number;
}

interface MockCollapsibleProps {
  title: string;
  badge?: { text: string; className: string };
  children: React.ReactNode;
}

interface MockCodeBlockProps {
  code: string;
  language?: string;
  showLineNumbers?: boolean;
  maxHeight?: number;
}

vi.mock('./shared', () => ({
  ToolCard: ({ title, badge, actions, children }: MockToolCardProps) => (
    <div data-testid="tool-card">
      <h3>{title}</h3>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {actions && <div data-testid="actions">{actions}</div>}
      {children}
    </div>
  ),
  CopyButton: ({ content }: MockCopyButtonProps) => (
    <button data-testid="copy-button" data-content={content}>Copy</button>
  ),
  MetadataRow: ({ label, value }: MockMetadataRowProps) => (
    <div data-testid="metadata-row">
      {label}: {value}
    </div>
  ),
  Collapsible: ({ title, badge, children }: MockCollapsibleProps) => (
    <div data-testid="collapsible">
      <h4>{title}</h4>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {children}
    </div>
  ),
  CodeBlock: ({ code, language, showLineNumbers, maxHeight }: MockCodeBlockProps) => (
    <div 
      data-testid="code-block" 
      data-language={language}
      data-show-line-numbers={showLineNumbers}
      style={{ maxHeight }}
    >
      <pre>{code}</pre>
    </div>
  ),
}));

// Mock utils
vi.mock('./utils', () => ({
  detectLanguageFromPath: vi.fn((path: string) => {
    if (path.endsWith('.js')) return 'javascript';
    if (path.endsWith('.py')) return 'python';
    if (path.endsWith('.json')) return 'json';
    return null;
  }),
  formatFileSize: vi.fn((size: number) => {
    if (size < 1024) return `${size} B`;
    if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
    return `${(size / (1024 * 1024)).toFixed(1)} MB`;
  }),
}));

describe('FileWriteRenderer', () => {
  const createToolResult = (metadata: Record<string, unknown> | null | undefined): ToolResult => ({
    toolName: 'file_write',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileWriteRenderer toolResult={toolResult} />);
    
    expect(container.firstChild).toBeNull();
  });

  it('renders file write with basic information', () => {
    const toolResult = createToolResult({
      filePath: '/home/user/output.txt',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('File Written')).toBeInTheDocument();
    expect(screen.getByText('Success')).toBeInTheDocument();
    const badge = screen.getByText('Success');
    expect(badge.className).toContain('font-heading');
    expect(screen.getByText('Path: /home/user/output.txt')).toBeInTheDocument();
  });

  it('shows file size when available', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      size: 1536,
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('Size: 1.5 KB')).toBeInTheDocument();
  });

  it('detects language from file path', () => {
    const toolResult = createToolResult({
      filePath: '/src/app.js',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('Language: javascript')).toBeInTheDocument();
  });

  it('uses provided language over detected language', () => {
    const toolResult = createToolResult({
      filePath: '/config.txt',
      language: 'yaml',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('Language: yaml')).toBeInTheDocument();
  });

  it('shows copy button when content is available', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      content: 'Hello, World!',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    const copyButton = screen.getByTestId('copy-button');
    expect(copyButton).toHaveAttribute('data-content', 'Hello, World!');
  });

  it('does not show copy button when content is missing', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.queryByTestId('copy-button')).not.toBeInTheDocument();
  });

  it('renders content in collapsible section', () => {
    const toolResult = createToolResult({
      filePath: '/test.js',
      content: 'const x = 1;\nconst y = 2;',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    const collapsible = screen.getByTestId('collapsible');
    expect(collapsible).toBeInTheDocument();
    expect(screen.getByText('Content')).toBeInTheDocument();
    expect(screen.getByText('Preview')).toBeInTheDocument();
  });

  it('passes correct props to CodeBlock', () => {
    const toolResult = createToolResult({
      filePath: '/app.py',
      content: 'def hello():\n    print("Hello")',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    const codeBlock = screen.getByTestId('code-block');
    expect(codeBlock).toHaveAttribute('data-language', 'python');
    expect(codeBlock).toHaveAttribute('data-show-line-numbers', 'true');
    expect(codeBlock).toHaveStyle({ maxHeight: '300px' });
    expect(codeBlock.textContent).toBe('def hello():\n    print("Hello")');
  });

  it('handles large files correctly', () => {
    const toolResult = createToolResult({
      filePath: '/large-file.json',
      size: 2 * 1024 * 1024, // 2MB
      content: '{"data": "large content"}',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.getByText('Size: 2.0 MB')).toBeInTheDocument();
    expect(screen.getByText('Language: json')).toBeInTheDocument();
  });

  it('handles files without extension', () => {
    const toolResult = createToolResult({
      filePath: '/usr/bin/script',
      content: '#!/bin/bash\necho "Hello"',
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    // Should not show language when it cannot be detected
    expect(screen.queryByText(/Language:/)).not.toBeInTheDocument();
  });

  it('handles empty content', () => {
    const toolResult = createToolResult({
      filePath: '/empty.txt',
      content: '',
      size: 0,
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    // Size 0 is not shown because the component checks if meta.size is truthy
    expect(screen.queryByText('Size: 0 B')).not.toBeInTheDocument();
    
    // Empty content (empty string) is falsy, so collapsible is not rendered
    expect(screen.queryByTestId('collapsible')).not.toBeInTheDocument();
    
    // No copy button for empty content
    expect(screen.queryByTestId('copy-button')).not.toBeInTheDocument();
  });

  it('does not render collapsible when content is not provided', () => {
    const toolResult = createToolResult({
      filePath: '/test.txt',
      size: 100,
    });

    render(<FileWriteRenderer toolResult={toolResult} />);

    expect(screen.queryByTestId('collapsible')).not.toBeInTheDocument();
  });
});