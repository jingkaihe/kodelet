import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
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

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(apiService.getConversations).mockResolvedValue(mockConversations);
  });

  it('provides initial state', async () => {
    const { result } = renderHook(() => useConversations());
    
    // Check initial state
    expect(result.current.conversations).toEqual([]);
    expect(result.current.stats).toBe(null);
    expect(result.current.error).toBe(null);
    expect(result.current.filters.searchTerm).toBe('');
    expect(result.current.filters.sortBy).toBe('updated');
    
    // Wait for async effects to complete
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalled();
    });
  });

  it('loads data eventually', async () => {
    const { result } = renderHook(() => useConversations());
    
    // Wait for data to load
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalled();
    }, { timeout: 5000 });
    
    // Eventually conversations should be loaded
    await waitFor(() => {
      expect(result.current.conversations.length).toBeGreaterThan(0);
    }, { timeout: 5000 });
  });


  it('allows setting filters', async () => {
    const { result } = renderHook(() => useConversations());
    
    act(() => {
      result.current.setFilters({ searchTerm: 'test' });
    });
    
    expect(result.current.filters.searchTerm).toBe('test');
    
    // Wait for the effect triggered by filter change
    await waitFor(() => {
      expect(apiService.getConversations).toHaveBeenCalledWith(
        expect.objectContaining({ searchTerm: 'test' })
      );
    });
  });

});