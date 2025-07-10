import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import apiService from './api';
import {
  ConversationListResponse,
  Conversation,
  ToolResult
} from '../types';

// Mock fetch globally
const mockFetch = vi.fn();
global.fetch = mockFetch;

describe('ApiService', () => {
  beforeEach(() => {
    mockFetch.mockClear();
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  describe('request method', () => {
    it('adds default headers', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => ({ data: 'test' }),
      });

      await apiService.getConversations();

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations',
        expect.objectContaining({
          headers: expect.objectContaining({
            'Content-Type': 'application/json',
          }),
        })
      );
    });

    it('throws error for non-ok responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: async () => ({ error: 'Not found' }),
      });

      await expect(apiService.getConversation('123')).rejects.toThrow('Not found');
    });

    it('handles non-JSON error responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: async () => { throw new Error('Invalid JSON'); },
      });

      await expect(apiService.getConversation('123')).rejects.toThrow('HTTP 500');
    });
  });

  describe('getConversations', () => {
    it('fetches conversations without filters', async () => {
      const mockResponse: ConversationListResponse = {
        conversations: [],
        hasMore: false,
        total: 0,
        limit: 25,
        offset: 0,
      };

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => mockResponse,
      });

      const result = await apiService.getConversations();

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations', expect.any(Object));
      expect(result).toEqual(mockResponse);
    });

    it('applies search filters', async () => {
      const mockResponse: ConversationListResponse = {
        conversations: [],
        hasMore: false,
        total: 0,
        limit: 25,
        offset: 0,
      };

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => mockResponse,
      });

      await apiService.getConversations({
        searchTerm: 'test',
        sortBy: 'created',
        sortOrder: 'desc',
        limit: 10,
        offset: 20,
      });

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations?search=test&sortBy=created&sortOrder=desc&limit=10&offset=20',
        expect.any(Object)
      );
    });

    it('omits undefined filter values', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => ({ conversations: [], total: 0 }),
      });

      await apiService.getConversations({
        searchTerm: 'test',
        sortBy: undefined,
        limit: undefined,
      });

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations?search=test',
        expect.any(Object)
      );
    });
  });

  describe('getConversation', () => {
    it('fetches a single conversation', async () => {
      const mockConversation: Conversation = {
        id: '123',
        messages: [],
        toolResults: {},
        usage: {},
        createdAt: '2023-01-01T00:00:00Z',
        updatedAt: '2023-01-01T00:00:00Z',
        messageCount: 0,
      };

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => mockConversation,
      });

      const result = await apiService.getConversation('123');

      expect(mockFetch).toHaveBeenCalledWith('/api/conversations/123', expect.any(Object));
      expect(result).toEqual(mockConversation);
    });
  });

  describe('deleteConversation', () => {
    it('sends DELETE request', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => ({}),
      });

      await apiService.deleteConversation('123');

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations/123',
        expect.objectContaining({
          method: 'DELETE',
        })
      );
    });
  });

  describe('getToolResult', () => {
    it('fetches tool result', async () => {
      const mockToolResult: ToolResult = {
        toolName: 'test-tool',
        success: true,
        timestamp: '2023-01-01T00:00:00Z',
      };

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => mockToolResult,
      });

      const result = await apiService.getToolResult('conv-123', 'tool-123');

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/conversations/conv-123/tools/tool-123',
        expect.any(Object)
      );
      expect(result).toEqual(mockToolResult);
    });
  });
});
