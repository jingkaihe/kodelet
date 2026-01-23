import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import EmptyState from './EmptyState';

describe('EmptyState', () => {
  it('displays empty state message to user', () => {
    render(
      <EmptyState
        iconType="conversation"
        title="No conversations yet"
        description="Start a new conversation to get started"
      />
    );
    
    expect(screen.getByText('No conversations yet')).toBeInTheDocument();
    expect(screen.getByText('Start a new conversation to get started')).toBeInTheDocument();
  });

  it('renders call-to-action when provided', () => {
    render(
      <EmptyState
        iconType="search"
        title="No results found"
        description="Try adjusting your search"
        action={<button>Clear filters</button>}
      />
    );
    
    expect(screen.getByRole('button', { name: 'Clear filters' })).toBeInTheDocument();
  });
});