import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor, act } from '@testing-library/react';
import { useConversation } from './useConversation';
import { apiService } from '../services/api';
import { Conversation, ToolResult } from '../types';

// Mock the API service
vi.mock('../services/api', () => ({
  apiService: {
    getConversation: vi.fn(),
    deleteConversation: vi.fn(),
  },
}));

// Mock window.location
delete (window as { location?: Location }).location;
window.location = { href: '' } as string & Location;

// Mock URL methods
global.URL.createObjectURL = vi.fn(() => 'mock-url');
global.URL.revokeObjectURL = vi.fn();

describe('useConversation', () => {
  const mockConversation: Conversation = {
    id: 'conv-123',
    messages: [
      {
        role: 'user',
        content: 'Hello',
        toolCalls: [],
      },
      {
        role: 'assistant',
        content: 'Hi there!',
        toolCalls: [],
        thinkingText: 'Thinking about greeting...',
      },
    ],
    toolResults: {},
    usage: {
      inputTokens: 100,
      outputTokens: 200,
      inputCost: 0.001,
      outputCost: 0.002,
    },
    createdAt: '2023-01-01T00:00:00Z',
    updatedAt: '2023-01-01T00:00:00Z',
    messageCount: 2,
  };

  beforeEach(() => {
    vi.clearAllMocks();
    window.location.href = '';
  });

  afterEach(() => {
    vi.resetAllMocks();
  });

  it('loads conversation on mount', async () => {
    vi.mocked(apiService.getConversation).mockResolvedValue(mockConversation);

    const { result } = renderHook(() => useConversation('conv-123'));

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.conversation).toEqual(mockConversation);
    expect(result.current.error).toBe(null);
    expect(apiService.getConversation).toHaveBeenCalledWith('conv-123');
  });

  it('handles empty conversation ID', async () => {
    const { result } = renderHook(() => useConversation(''));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.conversation).toBe(null);
    expect(apiService.getConversation).not.toHaveBeenCalled();
  });

  it('handles loading errors', async () => {
    const errorMessage = 'Failed to load';
    vi.mocked(apiService.getConversation).mockRejectedValueOnce(new Error(errorMessage));

    const { result } = renderHook(() => useConversation('conv-123'));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.error).toBe(errorMessage);
    expect(result.current.conversation).toBe(null);
  });

  it('normalizes message structure', async () => {
    const conversationWithBadData = {
      ...mockConversation,
      messages: [
        { role: 'user' as const, content: 'Test' }, // Fixed role
        { role: 'assistant' as const, content: '', tool_calls: [{ id: '1', function: { name: 'test', arguments: '{}' } }] }, // Old format
      ],
      toolResults: {} as Record<string, ToolResult>, // Should be normalized to {}
    };

    vi.mocked(apiService.getConversation).mockResolvedValue(conversationWithBadData);

    const { result } = renderHook(() => useConversation('conv-123'));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    const conversation = result.current.conversation!;
    expect(conversation.messages?.[0]?.role).toBe('user');
    expect(conversation.messages?.[0]?.content).toBe('Test');
    expect(conversation.messages?.[0]?.toolCalls).toEqual([]);
    expect(conversation.messages?.[1]?.toolCalls).toEqual([{ id: '1', function: { name: 'test', arguments: '{}' } }]);
    expect(conversation.toolResults).toEqual({});
  });

  it('deletes conversation and navigates', async () => {
    vi.mocked(apiService.getConversation).mockResolvedValue(mockConversation);
    vi.mocked(apiService.deleteConversation).mockResolvedValueOnce(undefined);

    const { result } = renderHook(() => useConversation('conv-123'));

    await waitFor(() => {
      expect(result.current.conversation).toBeTruthy();
    });

    await act(async () => {
      await result.current.deleteConversation();
    });

    expect(apiService.deleteConversation).toHaveBeenCalledWith('conv-123');
    expect(window.location.href).toBe('/');
  });

  it('handles delete errors', async () => {
    vi.mocked(apiService.getConversation).mockResolvedValue(mockConversation);
    const errorMessage = 'Failed to delete';
    vi.mocked(apiService.deleteConversation).mockRejectedValueOnce(new Error(errorMessage));

    const { result } = renderHook(() => useConversation('conv-123'));

    await waitFor(() => {
      expect(result.current.conversation).toBeTruthy();
    });

    await expect(
      act(async () => {
        await result.current.deleteConversation();
      })
    ).rejects.toThrow(errorMessage);

    expect(window.location.href).toBe(''); // Should not navigate
  });

  it('exports conversation as JSON', async () => {
    vi.mocked(apiService.getConversation).mockResolvedValue(mockConversation);

    // Mock document.createElement to return a proper anchor element
    const mockAnchor = document.createElement('a');
    const clickSpy = vi.fn();
    mockAnchor.click = clickSpy;
    const originalCreateElement = document.createElement.bind(document);

    vi.spyOn(document, 'createElement').mockImplementation((tagName: string) => {
      if (tagName === 'a') {
        return mockAnchor;
      }
      return originalCreateElement(tagName);
    });

    const { result } = renderHook(() => useConversation('conv-123'));

    await waitFor(() => {
      expect(result.current.conversation).toBeTruthy();
    });

    act(() => {
      result.current.exportConversation();
    });

    expect(document.createElement).toHaveBeenCalledWith('a');
    expect(clickSpy).toHaveBeenCalled();
    expect(URL.createObjectURL).toHaveBeenCalled();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('mock-url');

    // Check that the blob was created with correct data
    const blobCall = vi.mocked(URL.createObjectURL).mock.calls[0][0] as Blob;
    const reader = new FileReader();
    const blobContent = await new Promise<string>((resolve) => {
      reader.onload = () => resolve(reader.result as string);
      reader.readAsText(blobCall);
    });

    const exportedData = JSON.parse(blobContent);
    expect(exportedData.id).toBe('conv-123');
    expect(exportedData.messages).toEqual(mockConversation.messages);
  });

  it('refreshes conversation', async () => {
    vi.mocked(apiService.getConversation).mockResolvedValue(mockConversation);

    const { result } = renderHook(() => useConversation('conv-123'));

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    vi.clearAllMocks();

    await act(async () => {
      await result.current.refresh();
    });

    expect(apiService.getConversation).toHaveBeenCalledWith('conv-123');
  });

  it('reloads when conversation ID changes', async () => {
    vi.mocked(apiService.getConversation).mockResolvedValue(mockConversation);

    const { result, rerender } = renderHook(
      ({ id }) => useConversation(id),
      { initialProps: { id: 'conv-123' } }
    );

    await waitFor(() => {
      expect(result.current.conversation?.id).toBe('conv-123');
    });

    const newConversation = { ...mockConversation, id: 'conv-456' };
    vi.mocked(apiService.getConversation).mockResolvedValue(newConversation);

    rerender({ id: 'conv-456' });

    await waitFor(() => {
      expect(result.current.conversation?.id).toBe('conv-456');
    });

    expect(apiService.getConversation).toHaveBeenCalledWith('conv-456');
  });
});