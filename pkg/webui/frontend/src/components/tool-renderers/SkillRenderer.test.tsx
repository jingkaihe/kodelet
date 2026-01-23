import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import SkillRenderer from './SkillRenderer';
import { ToolResult, SkillMetadata } from '../../types';

interface MockStatusBadgeProps {
  text: string;
  variant?: string;
}

vi.mock('./shared', () => ({
  StatusBadge: ({ text, variant }: MockStatusBadgeProps) => (
    <span data-testid="status-badge" data-variant={variant}>{text}</span>
  ),
}));

describe('SkillRenderer', () => {
  const createToolResult = (metadata: SkillMetadata | null | undefined): ToolResult => ({
    toolName: 'skill',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as SkillMetadata | undefined,
  });

  it('renders skill name in badge', () => {
    const metadata: SkillMetadata = {
      skillName: 'pdf',
      directory: '/home/user/.kodelet/skills/pdf',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    expect(screen.getByText('pdf')).toBeInTheDocument();
    expect(screen.getByText('loaded')).toBeInTheDocument();
  });

  it('renders directory path', () => {
    const metadata: SkillMetadata = {
      skillName: 'kubernetes',
      directory: '~/.kodelet/skills/kubernetes',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    expect(screen.getByText('~/.kodelet/skills/kubernetes')).toBeInTheDocument();
  });

  it('shows success variant badge', () => {
    const metadata: SkillMetadata = {
      skillName: 'test',
      directory: '/test',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    const badge = screen.getByTestId('status-badge');
    expect(badge).toHaveAttribute('data-variant', 'success');
  });

  it('returns null when metadata is null', () => {
    const { container } = render(<SkillRenderer toolResult={createToolResult(null)} />);
    expect(container.firstChild).toBeNull();
  });

  it('returns null when metadata is undefined', () => {
    const { container } = render(<SkillRenderer toolResult={createToolResult(undefined)} />);
    expect(container.firstChild).toBeNull();
  });

  it('handles long skill names', () => {
    const metadata: SkillMetadata = {
      skillName: 'very-long-skill-name-for-testing',
      directory: '/some/directory/path',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    expect(screen.getByText('very-long-skill-name-for-testing')).toBeInTheDocument();
  });

  it('handles long directory paths', () => {
    const metadata: SkillMetadata = {
      skillName: 'test-skill',
      directory: '/home/user/very/long/path/to/kodelet/skills/test-skill',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    expect(screen.getByText('/home/user/very/long/path/to/kodelet/skills/test-skill')).toBeInTheDocument();
  });
});
