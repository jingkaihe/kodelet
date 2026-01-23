import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ToolCard, Collapsible, CopyButton, CodeBlock, MetadataRow, ExternalLink } from './shared';
import * as utils from '../../utils';

// Mock utils
vi.mock('../../utils', async () => {
  const actual = await vi.importActual('../../utils');
  return {
    ...actual,
    copyToClipboard: vi.fn(),
    escapeUrl: vi.fn((url) => url),
  };
});

describe('ToolCard', () => {
  it('renders tool result card with title and content', () => {
    render(
      <ToolCard title="File Edit">
        <div>File was successfully edited</div>
      </ToolCard>
    );

    expect(screen.getByText('File Edit')).toBeInTheDocument();
    expect(screen.getByText('File was successfully edited')).toBeInTheDocument();
  });

  it('displays status badge when provided', () => {
    render(
      <ToolCard title="Command" badge={{ text: 'Success', className: 'badge-success' }}>
        <div>Output</div>
      </ToolCard>
    );

    expect(screen.getByText('Success')).toBeInTheDocument();
  });

  it('renders action buttons when provided', () => {
    const onCopy = vi.fn();
    render(
      <ToolCard title="Code" actions={<button onClick={onCopy}>Copy</button>}>
        <div>Content</div>
      </ToolCard>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Copy' }));
    expect(onCopy).toHaveBeenCalled();
  });
});

describe('Collapsible', () => {
  it('provides expandable/collapsible functionality', () => {
    render(
      <Collapsible title="Tool Arguments" collapsed={true}>
        <div>JSON arguments here</div>
      </Collapsible>
    );

    expect(screen.getByText('Tool Arguments')).toBeInTheDocument();

    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).not.toBeChecked();

    // Toggle to expand
    fireEvent.click(checkbox);
    expect(checkbox).toBeChecked();
  });

  it('shows badge for additional context', () => {
    render(
      <Collapsible title="Output" badge={{ text: '1.2MB', className: 'badge-neutral' }}>
        <div>Large output</div>
      </Collapsible>
    );

    expect(screen.getByLabelText('1.2MB')).toBeInTheDocument();
  });
});

describe('CopyButton', () => {
  it('renders copy button', () => {
    render(<CopyButton content="Copy me" />);

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });
    expect(button).toBeInTheDocument();
    expect(button).toHaveClass('btn', 'btn-ghost', 'btn-xs');
  });

  it('calls copyToClipboard when clicked', () => {
    render(<CopyButton content="Copy this text" />);

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });
    fireEvent.click(button);

    expect(utils.copyToClipboard).toHaveBeenCalledWith('Copy this text');
  });

  it('applies custom className', () => {
    render(<CopyButton content="Copy me" className="btn-sm" />);

    const button = screen.getByRole('button', { name: 'Copy to clipboard' });
    expect(button).toHaveClass('btn', 'btn-ghost', 'btn-sm');
  });
});

describe('CodeBlock', () => {
  it('displays code with line numbers for readability', () => {
    const code = 'function hello() {\n  console.log("world");\n}';
    render(<CodeBlock code={code} language="javascript" />);

    // Verify line numbers exist
    expect(screen.getAllByText(/^\s*\d+\s*$/)).toHaveLength(3);

    // Verify code content
    expect(screen.getByText(/function hello/)).toBeInTheDocument();
    expect(screen.getByText(/console\.log/)).toBeInTheDocument();
  });

  it('respects maxHeight for long code blocks', () => {
    const longCode = Array(50).fill('console.log("line");').join('\n');
    render(<CodeBlock code={longCode} maxHeight={200} />);

    const codeBlock = screen.getByRole('region', { name: 'Code block' });
    expect(codeBlock).toHaveStyle({ maxHeight: '200px', overflowY: 'auto' });
  });

  it('handles empty lines in code', () => {
    const code = 'line1\n\nline3';
    const { container } = render(<CodeBlock code={code} />);

    const lineContentSpans = container.querySelectorAll('.line-content');
    expect(lineContentSpans).toHaveLength(3);
    expect(lineContentSpans[1].textContent).toBe(' ');
  });
});

describe('MetadataRow', () => {
  it('renders label and value', () => {
    render(<MetadataRow label="Key" value="Value" />);

    expect(screen.getByText('Key:')).toBeInTheDocument();
    expect(screen.getByText('Value')).toBeInTheDocument();
  });

  it('renders with monospace font when specified', () => {
    render(<MetadataRow label="Code" value="const x = 1" monospace />);

    const value = screen.getByText('const x = 1');
    expect(value).toHaveClass('font-mono');
  });

  it('renders numbers correctly', () => {
    render(<MetadataRow label="Count" value={42} />);

    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('returns null for null value', () => {
    const { container } = render(<MetadataRow label="Key" value={null} />);

    expect(container.firstChild).toBeNull();
  });

  it('returns null for undefined value', () => {
    const { container } = render(<MetadataRow label="Key" value={undefined} />);

    expect(container.firstChild).toBeNull();
  });
});

describe('ExternalLink', () => {
  it('renders link with children', () => {
    render(
      <ExternalLink href="https://example.com">
        Example Link
      </ExternalLink>
    );

    const link = screen.getByRole('link', { name: 'Open in new tab' });
    expect(link).toHaveAttribute('href', 'https://example.com');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    expect(screen.getByText('Example Link')).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(
      <ExternalLink href="https://example.com" className="custom-link">
        Link
      </ExternalLink>
    );

    const link = screen.getByRole('link');
    expect(link).toHaveClass('link', 'link-hover', 'custom-link');
  });

  it('handles invalid URL', () => {
    vi.mocked(utils.escapeUrl).mockReturnValueOnce('#');

    render(
      <ExternalLink href="javascript:alert(1)">
        Bad Link
      </ExternalLink>
    );

    expect(screen.getByText('Invalid URL')).toBeInTheDocument();
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('uses escaped URL', () => {
    vi.mocked(utils.escapeUrl).mockReturnValueOnce('https://safe-url.com');

    render(
      <ExternalLink href="https://example.com">
        Link
      </ExternalLink>
    );

    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', 'https://safe-url.com');
  });
});