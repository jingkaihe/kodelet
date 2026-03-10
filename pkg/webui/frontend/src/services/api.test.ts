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

    it('hydrates platform and api_mode from metadata', async () => {
      const mockResponse: ConversationListResponse = {
        conversations: [
          {
            id: 'conv-1',
            createdAt: '2023-01-01T00:00:00Z',
            updatedAt: '2023-01-02T00:00:00Z',
            messageCount: 3,
            provider: 'OpenAI',
            metadata: {
              platform: 'fireworks',
              api_mode: 'chat_completions',
            },
          },
        ],
        hasMore: false,
        total: 1,
        limit: 25,
        offset: 0,
      };

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => mockResponse,
      });

      const result = await apiService.getConversations();

      expect(result.conversations[0].platform).toBe('fireworks');
      expect(result.conversations[0].api_mode).toBe('chat_completions');
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

  describe('getChatSettings', () => {
    it('fetches chat settings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          currentProfile: 'work',
          profiles: [{ name: 'default', scope: 'built-in' }],
        }),
      });

      const result = await apiService.getChatSettings();

      expect(mockFetch).toHaveBeenCalledWith('/api/chat/settings', expect.any(Object));
      expect(result.currentProfile).toBe('work');
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

  describe('streamChat', () => {
    it('streams newline-delimited chat events', async () => {
      const onEvent = vi.fn();
      const encoder = new TextEncoder();

      mockFetch.mockResolvedValueOnce({
        ok: true,
        body: new ReadableStream({
          start(controller) {
            controller.enqueue(
              encoder.encode(
                '{"kind":"conversation","conversation_id":"conv-123"}\n{"kind":"done","conversation_id":"conv-123"}\n'
              )
            );
            controller.close();
          },
        }),
      });

      await apiService.streamChat(
        {
          message: 'hello',
        },
        { onEvent }
      );

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/chat',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({ message: 'hello' }),
        })
      );
      expect(onEvent).toHaveBeenNthCalledWith(1, {
        kind: 'conversation',
        conversation_id: 'conv-123',
      });
      expect(onEvent).toHaveBeenNthCalledWith(2, {
        kind: 'done',
        conversation_id: 'conv-123',
      });
    });

    it('sends multimodal content blocks when provided', async () => {
      const encoder = new TextEncoder();

      mockFetch.mockResolvedValueOnce({
        ok: true,
        body: new ReadableStream({
          start(controller) {
            controller.enqueue(encoder.encode('{"kind":"done"}\n'));
            controller.close();
          },
        }),
      });

      await apiService.streamChat(
        {
          message: 'describe this image',
          content: [
            { type: 'text', text: 'describe this image' },
            {
              type: 'image',
              source: {
                data: 'aGVsbG8=',
                media_type: 'image/png',
              },
            },
          ],
        },
        { onEvent: vi.fn() }
      );

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/chat',
        expect.objectContaining({
          method: 'POST',
          body: JSON.stringify({
            message: 'describe this image',
            content: [
              { type: 'text', text: 'describe this image' },
              {
                type: 'image',
                source: {
                  data: 'aGVsbG8=',
                  media_type: 'image/png',
                },
              },
            ],
          }),
        })
      );
    });

    it('sends profile when provided', async () => {
      const encoder = new TextEncoder();
      const stream = new ReadableStream({
        start(controller) {
          controller.enqueue(
            encoder.encode(
              '{"kind":"conversation","conversation_id":"conv-123"}\n{"kind":"done","conversation_id":"conv-123"}\n'
            )
          );
          controller.close();
        },
      });

      mockFetch.mockResolvedValueOnce({
        ok: true,
        body: stream,
      });

      await apiService.streamChat(
        {
          message: 'hello',
          profile: 'premium',
        },
        {
          onEvent: vi.fn(),
        }
      );

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/chat',
        expect.objectContaining({
          body: JSON.stringify({ message: 'hello', profile: 'premium' }),
        })
      );
    });
  });
});
