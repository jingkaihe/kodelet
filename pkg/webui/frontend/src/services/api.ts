// API service layer for Kodelet Web UI

import {
  Conversation,
  ConversationListResponse,
  SearchFilters,
  ApiError,
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

    return response.json();
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

    return response;
  }

  async getConversation(id: string): Promise<Conversation> {
    return this.request<Conversation>(`/api/conversations/${id}`);
  }

  async deleteConversation(id: string): Promise<void> {
    await this.request(`/api/conversations/${id}`, {
      method: 'DELETE',
    });
  }

  async getToolResult(conversationId: string, toolCallId: string): Promise<ToolResult> {
    return this.request(`/api/conversations/${conversationId}/tools/${toolCallId}`);
  }
}

export const apiService = new ApiService();
export default apiService;
