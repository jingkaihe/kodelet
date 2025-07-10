import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import FallbackRenderer from './FallbackRenderer';
import { ToolResult } from '../../types';

// Mock shared components
interface MockToolCardProps {
  title: string;
  badge?: { text: string; className: string };
  children: React.ReactNode;
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
  Collapsible: ({ title, badge, children, collapsed }: MockCollapsibleProps) => (
    <div data-testid="collapsible" data-collapsed={collapsed}>
      <h4>{title}</h4>
      {badge && <span className={badge.className}>{badge.text}</span>}
      {children}
    </div>
  ),
}));

describe('FallbackRenderer', () => {
  const createToolResult = (toolName: string, metadata: Record<string, unknown> | null | undefined): ToolResult => ({
    toolName,
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as Record<string, unknown> | undefined,
  });

  it('renders with tool name', () => {
    const toolResult = createToolResult('custom-tool', { key: 'value' });
    render(<FallbackRenderer toolResult={toolResult} />);
    
    expect(screen.getByText('🔧 custom-tool')).toBeInTheDocument();
  });

  it('shows Unknown Tool badge', () => {
    const toolResult = createToolResult('mystery-tool', {});
    render(<FallbackRenderer toolResult={toolResult} />);
    
    expect(screen.getByText('Unknown Tool')).toBeInTheDocument();
    const badge = screen.getByText('Unknown Tool');
    expect(badge.className).toContain('badge-info');
  });

  it('renders collapsible with debug info', () => {
    const toolResult = createToolResult('test-tool', {});
    render(<FallbackRenderer toolResult={toolResult} />);
    
    const collapsible = screen.getByTestId('collapsible');
    expect(collapsible).toBeInTheDocument();
    expect(screen.getByText('Raw Metadata')).toBeInTheDocument();
    expect(screen.getByText('Debug Info')).toBeInTheDocument();
    const debugBadge = screen.getByText('Debug Info');
    expect(debugBadge.className).toContain('badge-warning');
  });

  it('renders collapsible as collapsed by default', () => {
    const toolResult = createToolResult('test-tool', {});
    render(<FallbackRenderer toolResult={toolResult} />);
    
    const collapsible = screen.getByTestId('collapsible');
    expect(collapsible).toHaveAttribute('data-collapsed', 'true');
  });

  it('displays metadata as formatted JSON', () => {
    const metadata = {
      stringValue: 'test',
      numberValue: 42,
      booleanValue: true,
      nestedObject: {
        key1: 'value1',
        key2: 'value2',
      },
      arrayValue: ['item1', 'item2'],
    };
    
    const toolResult = createToolResult('complex-tool', metadata);
    const { container } = render(<FallbackRenderer toolResult={toolResult} />);
    
    const pre = container.querySelector('pre');
    expect(pre).toBeInTheDocument();
    expect(pre).toHaveClass('text-xs', 'overflow-x-auto', 'bg-base-100', 'p-2', 'rounded');
    
    const code = pre?.querySelector('code');
    expect(code?.textContent).toBe(JSON.stringify(metadata, null, 2));
  });

  it('handles null metadata', () => {
    const toolResult = createToolResult('null-tool', null);
    const { container } = render(<FallbackRenderer toolResult={toolResult} />);
    
    const code = container.querySelector('pre code');
    expect(code?.textContent).toBe('null');
  });

  it('handles undefined metadata', () => {
    const toolResult = createToolResult('undefined-tool', undefined);
    const { container } = render(<FallbackRenderer toolResult={toolResult} />);
    
    const code = container.querySelector('pre code');
    expect(code?.textContent).toBe('');
  });

  it('handles empty object metadata', () => {
    const toolResult = createToolResult('empty-tool', {});
    const { container } = render(<FallbackRenderer toolResult={toolResult} />);
    
    const code = container.querySelector('pre code');
    expect(code?.textContent).toBe('{}');
  });

  it('handles circular reference in metadata gracefully', () => {
    const metadata: Record<string, unknown> = { key: 'value' };
    metadata.circular = metadata; // Create circular reference
    
    const toolResult = createToolResult('circular-tool', metadata);
    
    // Component should handle circular references gracefully by showing [Circular]
    render(<FallbackRenderer toolResult={toolResult} />);
    
    // Should show [Circular] instead of throwing an error
    expect(screen.getByText(/\[Circular\]/)).toBeInTheDocument();
  });

  it('handles very long tool names', () => {
    const longName = 'very-long-tool-name-that-might-overflow-the-ui-boundaries';
    const toolResult = createToolResult(longName, {});
    render(<FallbackRenderer toolResult={toolResult} />);
    
    expect(screen.getByText(`🔧 ${longName}`)).toBeInTheDocument();
  });

  it('handles special characters in tool name', () => {
    const specialName = 'tool-with-<special>&"characters"';
    const toolResult = createToolResult(specialName, {});
    render(<FallbackRenderer toolResult={toolResult} />);
    
    expect(screen.getByText(`🔧 ${specialName}`)).toBeInTheDocument();
  });
});