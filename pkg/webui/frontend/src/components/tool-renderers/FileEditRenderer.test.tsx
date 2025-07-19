import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import FileEditRenderer from './FileEditRenderer';
import { ToolResult } from '../../types';

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

describe('FileEditRenderer', () => {
  const createToolResult = (metadata: Record<string, unknown> | null | undefined, toolName = 'file_edit'): ToolResult => ({
    toolName,
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult(undefined);
    const { container } = render(<FileEditRenderer toolResult={toolResult} />);
    
    expect(container.firstChild).toBeNull();
  });

  describe('Single File Edit', () => {
    it('renders file edit with basic information', () => {
      const toolResult = createToolResult({
        filePath: '/src/app.js',
        edits: [],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('✏️ File Edit')).toBeInTheDocument();
      expect(screen.getByText('Path: /src/app.js')).toBeInTheDocument();
    });

    it('shows correct edit count in badge', () => {
      const toolResult = createToolResult({
        filePath: '/test.txt',
        edits: [
          { startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' },
          { startLine: 5, endLine: 6, oldContent: 'foo', newContent: 'bar' },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('2 edits')).toBeInTheDocument();
      const badge = screen.getByText('2 edits');
      expect(badge.className).toContain('badge-info');
    });

    it('shows singular edit for single edit', () => {
      const toolResult = createToolResult({
        filePath: '/test.txt',
        edits: [
          { startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('1 edit')).toBeInTheDocument();
    });

    it('renders edit details', () => {
      const toolResult = createToolResult({
        filePath: '/src/main.js',
        edits: [
          {
            startLine: 10,
            endLine: 12,
            oldContent: 'const x = 1;',
            newContent: 'const x = 2;',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('Edit 1: Lines 10-12')).toBeInTheDocument();
      expect(screen.getByText('View Changes')).toBeInTheDocument();
    });

    it('shows collapsible uncollapsed by default', () => {
      const toolResult = createToolResult({
        filePath: '/test.txt',
        edits: [{ startLine: 1, endLine: 1, oldContent: 'old', newContent: 'new' }],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      const collapsible = screen.getByTestId('collapsible');
      expect(collapsible).toHaveAttribute('data-collapsed', 'false');
    });
  });



  describe('Diff Rendering', () => {
    it('renders simple line change', () => {
      const toolResult = createToolResult({
        filePath: '/test.js',
        edits: [
          {
            startLine: 1,
            endLine: 1,
            oldContent: 'const x = 1;',
            newContent: 'const x = 2;',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('--- a/test.js')).toBeInTheDocument();
      expect(screen.getByText('+++ b/test.js')).toBeInTheDocument();
      expect(screen.getByText('@@ -1,1 +1,1 @@')).toBeInTheDocument();
    });

    it('renders multi-line changes', () => {
      const toolResult = createToolResult({
        filePath: '/app.js',
        edits: [
          {
            startLine: 5,
            endLine: 7,
            oldContent: 'line1\nline2\nline3',
            newContent: 'new1\nnew2\nnew3',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('@@ -5,3 +5,3 @@')).toBeInTheDocument();
    });

    it('handles empty old content (addition)', () => {
      const toolResult = createToolResult({
        filePath: '/new.txt',
        edits: [
          {
            startLine: 1,
            endLine: 1,
            oldContent: '',
            newContent: 'new line',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('@@ -1,0 +1,1 @@')).toBeInTheDocument();
      expect(screen.getByText('new line')).toBeInTheDocument();
    });

    it('handles empty new content (deletion)', () => {
      const toolResult = createToolResult({
        filePath: '/delete.txt',
        edits: [
          {
            startLine: 1,
            endLine: 1,
            oldContent: 'deleted line',
            newContent: '',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('@@ -1,1 +1,0 @@')).toBeInTheDocument();
      expect(screen.getByText('deleted line')).toBeInTheDocument();
    });

    it('renders multiple edits', () => {
      const toolResult = createToolResult({
        filePath: '/multi.js',
        edits: [
          {
            startLine: 1,
            endLine: 1,
            oldContent: 'first',
            newContent: 'FIRST',
          },
          {
            startLine: 10,
            endLine: 11,
            oldContent: 'second\nthird',
            newContent: 'SECOND\nTHIRD',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('Edit 1: Lines 1-1')).toBeInTheDocument();
      expect(screen.getByText('Edit 2: Lines 10-11')).toBeInTheDocument();
      expect(screen.getAllByText(/@@.*@@/)).toHaveLength(2);
    });

    it('handles insertions in middle of content', () => {
      const toolResult = createToolResult({
        filePath: '/insert.js',
        edits: [
          {
            startLine: 2,
            endLine: 3,
            oldContent: 'line2\nline3',
            newContent: 'line2\ninserted\nline3',
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('@@ -2,2 +2,3 @@')).toBeInTheDocument();
      // Check that diff prefixes are rendered
      const container = render(<FileEditRenderer toolResult={toolResult} />).container;
      expect(container.textContent).toContain('+');
    });

    it('preserves whitespace in content', () => {
      const toolResult = createToolResult({
        filePath: '/whitespace.py',
        edits: [
          {
            startLine: 1,
            endLine: 2,
            oldContent: 'def foo():\n  return 1',
            newContent: 'def foo():\n    return 2',
          },
        ],
      });

      const { container } = render(<FileEditRenderer toolResult={toolResult} />);

      // Check that whitespace-pre class is applied
      const diffLines = container.querySelectorAll('.whitespace-pre');
      expect(diffLines.length).toBeGreaterThan(0);
    });

    it('handles null content gracefully', () => {
      const toolResult = createToolResult({
        filePath: '/null.txt',
        edits: [
          {
            startLine: 1,
            endLine: 1,
            oldContent: null,
            newContent: null,
          },
        ],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.getByText('@@ -1,0 +1,0 @@')).toBeInTheDocument();
    });

    it('handles empty edits array', () => {
      const toolResult = createToolResult({
        filePath: '/empty.txt',
        edits: [],
      });

      render(<FileEditRenderer toolResult={toolResult} />);

      expect(screen.queryByText('View Changes')).not.toBeInTheDocument();
    });

    it('applies correct diff colors', () => {
      const toolResult = createToolResult({
        filePath: '/colors.js',
        edits: [
          {
            startLine: 1,
            endLine: 2,
            oldContent: 'removed line\ncontext line',
            newContent: 'context line\nadded line',
          },
        ],
      });

      const { container } = render(<FileEditRenderer toolResult={toolResult} />);

      // Check for background colors
      expect(container.querySelector('.bg-red-50')).toBeInTheDocument();
      expect(container.querySelector('.bg-green-50')).toBeInTheDocument();
      expect(container.querySelector('.bg-white')).toBeInTheDocument();
    });
  });
});