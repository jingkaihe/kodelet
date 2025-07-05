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
  it('renders with title and children', () => {
    render(
      <ToolCard title="Test Tool">
        <div>Test content</div>
      </ToolCard>
    );
    
    expect(screen.getByText('Test Tool')).toBeInTheDocument();
    expect(screen.getByText('Test content')).toBeInTheDocument();
  });

  it('renders with badge', () => {
    render(
      <ToolCard title="Test Tool" badge={{ text: 'Badge Text', className: 'badge-success' }}>
        <div>Content</div>
      </ToolCard>
    );
    
    const badge = screen.getByText('Badge Text');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveClass('badge', 'badge-sm', 'badge-success');
  });

  it('renders with actions', () => {
    render(
      <ToolCard title="Test Tool" actions={<button>Action</button>}>
        <div>Content</div>
      </ToolCard>
    );
    
    expect(screen.getByRole('button', { name: 'Action' })).toBeInTheDocument();
  });

  it('applies custom className', () => {
    render(
      <ToolCard title="Test Tool" className="custom-class">
        <div>Content</div>
      </ToolCard>
    );
    
    const card = screen.getByRole('article');
    expect(card).toHaveClass('card', 'custom-class', 'border');
  });

  it('uses default className when not provided', () => {
    render(
      <ToolCard title="Test Tool">
        <div>Content</div>
      </ToolCard>
    );
    
    const card = screen.getByRole('article');
    expect(card).toHaveClass('card', 'bg-base-200', 'border');
  });
});

describe('Collapsible', () => {
  it('renders with title and children', () => {
    render(
      <Collapsible title="Collapsible Section">
        <div>Collapsible content</div>
      </Collapsible>
    );
    
    expect(screen.getByText('Collapsible Section')).toBeInTheDocument();
    expect(screen.getByText('Collapsible content')).toBeInTheDocument();
  });

  it('is expanded by default when collapsed prop is false', () => {
    render(
      <Collapsible title="Section" collapsed={false}>
        <div>Content</div>
      </Collapsible>
    );
    
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).toBeChecked();
    expect(checkbox).toHaveAttribute('aria-expanded', 'true');
  });

  it('is collapsed when collapsed prop is true', () => {
    render(
      <Collapsible title="Section" collapsed={true}>
        <div>Content</div>
      </Collapsible>
    );
    
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).not.toBeChecked();
    expect(checkbox).toHaveAttribute('aria-expanded', 'false');
  });

  it('renders with badge', () => {
    render(
      <Collapsible title="Section" badge={{ text: 'New', className: 'badge-primary' }}>
        <div>Content</div>
      </Collapsible>
    );
    
    const badge = screen.getByLabelText('New');
    expect(badge).toHaveClass('badge', 'badge-sm', 'badge-primary');
  });

  it('toggles expansion state', () => {
    render(
      <Collapsible title="Section" collapsed={true}>
        <div>Content</div>
      </Collapsible>
    );
    
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).not.toBeChecked();
    
    fireEvent.click(checkbox);
    expect(checkbox).toBeChecked();
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
  it('renders code with line numbers by default', () => {
    const code = 'const x = 1;\nconst y = 2;';
    render(<CodeBlock code={code} />);
    
    const lineNumbers = screen.getAllByText(/^\s*\d+\s*$/);
    expect(lineNumbers).toHaveLength(2);
    expect(screen.getByText('const x = 1;')).toBeInTheDocument();
    expect(screen.getByText('const y = 2;')).toBeInTheDocument();
  });

  it('renders without line numbers when showLineNumbers is false', () => {
    const code = 'const x = 1;\nconst y = 2;';
    const { container } = render(<CodeBlock code={code} showLineNumbers={false} />);
    
    const lineNumbers = screen.queryAllByText(/^\s*\d+\s*$/);
    expect(lineNumbers).toHaveLength(0);
    
    // When showLineNumbers is false, the code is rendered as plain text
    const codeElement = container.querySelector('code');
    expect(codeElement?.textContent).toBe(code);
  });

  it('applies language class', () => {
    render(<CodeBlock code="const x = 1;" language="javascript" />);
    
    const codeElement = screen.getByRole('region', { name: 'Code block' }).querySelector('code');
    expect(codeElement).toHaveClass('language-javascript');
  });

  it('applies maxHeight style', () => {
    render(<CodeBlock code="const x = 1;" maxHeight={200} />);
    
    const codeBlock = screen.getByRole('region', { name: 'Code block' });
    expect(codeBlock).toHaveStyle({ maxHeight: '200px', overflowY: 'auto' });
  });

  it('handles empty lines correctly', () => {
    const code = 'line1\n\nline3';
    const { container } = render(<CodeBlock code={code} />);
    
    const lines = screen.getAllByText(/line\d/);
    expect(lines).toHaveLength(2);
    
    // Empty line should be rendered as a space in line-content span
    const lineContentSpans = container.querySelectorAll('.line-content');
    expect(lineContentSpans).toHaveLength(3);
    expect(lineContentSpans[1].textContent).toBe(' '); // Empty line rendered as space
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