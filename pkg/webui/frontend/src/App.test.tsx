import { describe, it, expect, vi } from 'vitest';
import { render, waitFor } from '@testing-library/react';
import App from './App';

// Mock the hooks to prevent async state updates
vi.mock('./hooks/useConversations', () => ({
  useConversations: () => ({
    conversations: [],
    stats: null,
    loading: false,
    error: null,
    hasMore: false,
    filters: {
      searchTerm: '',
      sortBy: 'updated',
      sortOrder: 'desc',
      limit: 25,
      offset: 0,
    },
    setFilters: vi.fn(),
    loadMore: vi.fn(),
    deleteConversation: vi.fn(),
    refresh: vi.fn(),
  }),
}));

vi.mock('./hooks/useConversation', () => ({
  useConversation: () => ({
    conversation: null,
    loading: true,
    error: null,
    refresh: vi.fn(),
  }),
}));

describe('App', () => {
  it('renders without crashing', async () => {
    const { container } = render(<App />);

    await waitFor(() => {
      expect(container.querySelector('.min-h-screen')).toBeInTheDocument();
    });
  });

  it('provides base application structure', async () => {
    const { container } = render(<App />);

    await waitFor(() => {
      const wrapper = container.firstElementChild;
      expect(wrapper).toHaveClass('min-h-screen', 'bg-base-100');
    });
  });
});