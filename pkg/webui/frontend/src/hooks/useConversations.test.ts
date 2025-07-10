import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { useConversations } from './useConversations';
import { apiService } from '../services/api';
import { ConversationListResponse } from '../types';

// Mock the API service
vi.mock('../services/api', () => ({
  apiService: {
    getConversations: vi.fn(),
    deleteConversation: vi.fn(),
  },
}));

describe('useConversations (simplified)', () => {
  const mockConversations: ConversationListResponse = {
    conversations: [
      {
        id: '1',
        messages: [],
        toolResults: {},
        usage: {
          inputTokens: 100,
          outputTokens: 200,
        },
        createdAt: '2023-01-01T00:00:00Z',
        updatedAt: '2023-01-01T00:00:00Z',
        messageCount: 0,
      },
    ],
    hasMore: false,
    total: 1,
    limit: 25,
    offset: 0,
    stats: {
      totalConversations: 10,
      totalMessages: 100,
      totalTokens: 1000,
      totalCost: 0.05,
      inputTokens: 600,
      outputTokens: 400,
      cacheReadTokens: 0,
      cacheWriteTokens: 0,
      inputCost: 0.03,
      outputCost: 0.02,
      cacheReadCost: 0,
      cacheWriteCost: 0,
    },
  };

  const mockFilters = {
    searchTerm: '',
    sortBy: 'updated' as const,
    sortOrder: 'desc' as const,
    limit: 25,
    offset: 0,
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiService.getConversations).mockResolvedValue(mockConversations);
  });

  it('provides initial state', async () => {
    const { result } = renderHook(() => useConversations({ filters: mockFilters }));
    
    // Check initial state
    expect(result.current.conversations).toEqual([]);
    expect(result.current.stats).toBe(null);
    expect(result.current.error).toBe(null);
    
    // Wait for async effects to complete
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalled();
    });
  });

  it('loads data eventually', async () => {
    const { result } = renderHook(() => useConversations({ filters: mockFilters }));
    
    // Wait for data to load
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalled();
    }, { timeout: 5000 });
    
    // Eventually conversations should be loaded
    await waitFor(() => {
      expect(result.current.conversations.length).toBeGreaterThan(0);
    }, { timeout: 5000 });
  });


  it('allows updating filters through props', async () => {
    const initialFilters = { ...mockFilters };
    const { rerender } = renderHook(
      (props) => useConversations({ filters: props.filters }),
      { initialProps: { filters: initialFilters } }
    );
    
    // Wait for the initial load to complete
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalled();
    });
    
    // Clear the mock to check the next call
    vi.clearAllMocks();
    vi.mocked(apiService.getConversations).mockResolvedValue(mockConversations);
    
    const updatedFilters = { ...mockFilters, searchTerm: 'test' };
    rerender({ filters: updatedFilters });
    
    // Wait for the effect triggered by filter change
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalledWith(
        expect.objectContaining({ searchTerm: 'test' })
      );
    });
  });

});