import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import SkillRenderer from './SkillRenderer';
import { ToolResult, SkillMetadata } from '../../types';

describe('SkillRenderer', () => {
  const createToolResult = (metadata: SkillMetadata | null | undefined): ToolResult => ({
    toolName: 'skill',
    success: true,
    error: undefined,
    timestamp: '2023-01-01T00:00:00Z',
    metadata: metadata as SkillMetadata | undefined,
  });

  it('renders skill information correctly', () => {
    const metadata: SkillMetadata = {
      skillName: 'pdf',
      directory: '/home/user/.kodelet/skills/pdf',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    expect(screen.getByText('⚡ Skill Loaded')).toBeInTheDocument();
    expect(screen.getByText('pdf')).toBeInTheDocument();
    expect(screen.getByText('Directory:')).toBeInTheDocument();
    expect(screen.getByText('/home/user/.kodelet/skills/pdf')).toBeInTheDocument();
  });

  it('renders badge with skill name', () => {
    const metadata: SkillMetadata = {
      skillName: 'kubernetes',
      directory: '~/.kodelet/skills/kubernetes',
    };
    
    render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    const badge = screen.getByText('kubernetes');
    expect(badge).toBeInTheDocument();
    expect(badge.className).toContain('font-heading');
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

  it('renders directory in code block', () => {
    const metadata: SkillMetadata = {
      skillName: 'xlsx',
      directory: '/skills/xlsx',
    };
    
    const { container } = render(<SkillRenderer toolResult={createToolResult(metadata)} />);
    
    const codeElement = container.querySelector('code');
    expect(codeElement).toBeInTheDocument();
    expect(codeElement?.textContent).toBe('/skills/xlsx');
  });
});
