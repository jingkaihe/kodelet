import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import SubagentRenderer from './SubagentRenderer';
import { ToolResult, SubagentMetadata } from '../../types';

interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
  ),
}));

vi.mock('marked', () => ({
  marked: {
    setOptions: vi.fn(),
    parse: (text: string) => `<p>${text}</p>`,
  },
}));

describe('SubagentRenderer', () => {
  const createToolResult = (metadata: Partial<SubagentMetadata>): ToolResult => ({
    toolName: 'subagent',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as SubagentMetadata,
  });

  it('returns null when metadata is missing', () => {
    const toolResult = createToolResult({});
    const { container } = render(<SubagentRenderer toolResult={{ ...toolResult, metadata: undefined }} />);

    expect(container.firstChild).toBeNull();
  });

  it('renders delegated badge', () => {
    const toolResult = createToolResult({
      question: 'What is the answer?',
      response: '42',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    expect(screen.getByTestId('status-badge')).toHaveTextContent('Delegated');
  });

  it('renders workflow badge when workflow is present', () => {
    const toolResult = createToolResult({
      question: 'Create a PR',
      response: 'PR created',
      workflow: 'github/pr',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    expect(screen.getByText('github/pr')).toBeInTheDocument();
  });

  it('renders cwd badge when cwd is present', () => {
    const toolResult = createToolResult({
      question: 'Run tests',
      response: 'Tests passed',
      cwd: '/home/user/project',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    expect(screen.getByText('/home/user/project')).toBeInTheDocument();
  });

  it('renders both workflow and cwd badges', () => {
    const toolResult = createToolResult({
      question: 'Create commit',
      response: 'feat: add feature',
      workflow: 'commit',
      cwd: '/tmp/repo',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    expect(screen.getByText('commit')).toBeInTheDocument();
    expect(screen.getByText('/tmp/repo')).toBeInTheDocument();
  });

  it('shows details when "Show details" is clicked', () => {
    const toolResult = createToolResult({
      question: 'What is the meaning of life?',
      response: '42',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    expect(screen.queryByText('Question:')).not.toBeInTheDocument();
    expect(screen.queryByText('Response:')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('Show details'));

    expect(screen.getByText('Question:')).toBeInTheDocument();
    expect(screen.getByText('Response:')).toBeInTheDocument();
  });

  it('renders question and response in details view', () => {
    const toolResult = createToolResult({
      question: 'Find the bug',
      response: 'Bug found in line 42',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show details'));

    expect(screen.getByText('Question:')).toBeInTheDocument();
    expect(screen.getByText('Response:')).toBeInTheDocument();
  });

  it('renders workflow in details view when present', () => {
    const toolResult = createToolResult({
      question: 'Create PR',
      response: 'PR created',
      workflow: 'github/pr',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show details'));

    expect(screen.getByText('Workflow:')).toBeInTheDocument();
  });

  it('renders directory in details view when cwd is present', () => {
    const toolResult = createToolResult({
      question: 'Run tests',
      response: 'Tests passed',
      cwd: '/home/user/project',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show details'));

    expect(screen.getByText('Directory:')).toBeInTheDocument();
  });

  it('handles empty question gracefully', () => {
    const toolResult = createToolResult({
      response: 'Workflow result',
      workflow: 'commit',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show details'));

    expect(screen.queryByText('Question:')).not.toBeInTheDocument();
    expect(screen.getByText('Response:')).toBeInTheDocument();
  });

  it('handles empty response gracefully', () => {
    const toolResult = createToolResult({
      question: 'Do something',
    });

    render(<SubagentRenderer toolResult={toolResult} />);

    fireEvent.click(screen.getByText('Show details'));

    expect(screen.getByText('Question:')).toBeInTheDocument();
    expect(screen.queryByText('Response:')).not.toBeInTheDocument();
  });
});
