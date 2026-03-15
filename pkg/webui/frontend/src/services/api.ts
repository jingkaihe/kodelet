// API service layer for Kodelet Web UI

import {
  ChatSettings,
  ChatRequest,
  ChatStreamEvent,
  Conversation,
  ConversationListResponse,
  SearchFilters,
  ApiError,
  SteerConversationResponse,
  StopConversationResponse,
  ForkConversationResponse,
  ToolResult
} from '../types';

class ApiService {
  private baseUrl = '';

  private async request<T>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<T> {
    const response = await fetch(`${this.baseUrl}${endpoint}`, {
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
      ...options,
    });

    if (!response.ok) {
      let error: ApiError;
      try {
        error = await response.json();
      } catch {
        error = { error: `HTTP ${response.status}` };
      }
      throw new Error(error.error || error.message || `HTTP ${response.status}`);
    }

    if (response.status === 204) {
      return undefined as T;
    }

    return response.json();
  }

  private extractStringMetadataValue(metadata: unknown, key: string): string | undefined {
    if (!metadata || typeof metadata !== 'object' || Array.isArray(metadata)) {
      return undefined;
    }

    const rawValue = (metadata as Record<string, unknown>)[key];
    if (typeof rawValue !== 'string') {
      return undefined;
    }

    const normalized = rawValue.trim().toLowerCase();
    return normalized || undefined;
  }

  async getConversations(filters: Partial<SearchFilters> = {}): Promise<ConversationListResponse> {
    const params = new URLSearchParams();

    if (filters.searchTerm) params.append('search', filters.searchTerm);
    if (filters.sortBy) params.append('sortBy', filters.sortBy);
    if (filters.sortOrder) params.append('sortOrder', filters.sortOrder);
    if (filters.limit) params.append('limit', filters.limit.toString());
    if (filters.offset) params.append('offset', filters.offset.toString());

    const queryString = params.toString();
    const endpoint = queryString ? `/api/conversations?${queryString}` : '/api/conversations';

    const response = await this.request<ConversationListResponse>(endpoint);

    // Ensure conversations is always an array
    if (!response.conversations || !Array.isArray(response.conversations)) {
      response.conversations = [];
    }

    response.conversations = response.conversations.map((conversation) => ({
      ...conversation,
      platform: conversation.platform ?? this.extractStringMetadataValue(conversation.metadata, 'platform'),
      api_mode: conversation.api_mode ?? this.extractStringMetadataValue(conversation.metadata, 'api_mode'),
    }));

    return response;
  }

  async getConversation(id: string): Promise<Conversation> {
    return this.request<Conversation>(`/api/conversations/${id}`);
  }

  async getChatSettings(): Promise<ChatSettings> {
    return this.request<ChatSettings>('/api/chat/settings');
  }

  async deleteConversation(id: string): Promise<void> {
    await this.request(`/api/conversations/${id}`, {
      method: 'DELETE',
    });
  }

  async forkConversation(id: string): Promise<ForkConversationResponse> {
    return this.request<ForkConversationResponse>(`/api/conversations/${id}/fork`, {
      method: 'POST',
    });
  }

  async steerConversation(id: string, message: string): Promise<SteerConversationResponse> {
    return this.request<SteerConversationResponse>(`/api/conversations/${id}/steer`, {
      method: 'POST',
      body: JSON.stringify({ message }),
    });
  }

  async stopConversation(id: string): Promise<StopConversationResponse> {
    return this.request<StopConversationResponse>(`/api/conversations/${id}/stop`, {
      method: 'POST',
    });
  }

  async getToolResult(conversationId: string, toolCallId: string): Promise<ToolResult> {
    return this.request(`/api/conversations/${conversationId}/tools/${toolCallId}`);
  }

  async streamChat(
    request: ChatRequest,
    options: {
      signal?: AbortSignal;
      onEvent: (event: ChatStreamEvent) => void;
    }
  ): Promise<void> {
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(request),
      signal: options.signal,
    });

    if (!response.ok) {
      let error: ApiError;
      try {
        error = await response.json();
      } catch {
        error = { error: `HTTP ${response.status}` };
      }
      throw new Error(error.error || error.message || `HTTP ${response.status}`);
    }

    if (!response.body) {
      throw new Error('Streaming response body is unavailable');
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    while (true) {
      const { done, value } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });

      const lines = buffer.split('\n');
      buffer = lines.pop() || '';

      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed) {
          continue;
        }
        options.onEvent(JSON.parse(trimmed) as ChatStreamEvent);
      }

      if (done) {
        const trimmed = buffer.trim();
        if (trimmed) {
          options.onEvent(JSON.parse(trimmed) as ChatStreamEvent);
        }
        return;
      }
    }
  }

  async streamConversation(
    conversationId: string,
    options: {
      signal?: AbortSignal;
      onEvent: (event: ChatStreamEvent) => void;
    }
  ): Promise<void> {
    const response = await fetch(`/api/conversations/${conversationId}/stream`, {
      method: 'GET',
      signal: options.signal,
    });

    if (!response.ok) {
      let error: ApiError;
      try {
        error = await response.json();
      } catch {
        error = { error: `HTTP ${response.status}` };
      }
      throw new Error(error.error || error.message || `HTTP ${response.status}`);
    }

    if (!response.body) {
      throw new Error('Streaming response body is unavailable');
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    while (true) {
      const { done, value } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });

      const lines = buffer.split('\n');
      buffer = lines.pop() || '';

      for (const line of lines) {
        const trimmed = line.trim();
        if (!trimmed) {
          continue;
        }
        options.onEvent(JSON.parse(trimmed) as ChatStreamEvent);
      }

      if (done) {
        const trimmed = buffer.trim();
        if (trimmed) {
          options.onEvent(JSON.parse(trimmed) as ChatStreamEvent);
        }
        return;
      }
    }
  }
}

export const apiService = new ApiService();
export default apiService;
