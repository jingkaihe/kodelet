// API service layer for Kodelet Web UI

import type {
  AnthropicOAuthLogin,
  AnthropicProviderStatus,
  ApiError,
  AuthPrincipal,
  BrowserSession,
  BrowserTarget,
  ChatRequest,
  ChatSettings,
  ChatStreamEvent,
  CodexDeviceLogin,
  CodexProviderStatus,
  ContentBlock,
  Conversation,
  ConversationListResponse,
  CopilotDeviceLogin,
  CopilotProviderStatus,
  CWDHintsResponse,
  ForkConversationResponse,
  GitDiffResponse,
  RunnerDiscoveryTarget,
  RunnerEnrollmentDecisionResponse,
  RunnerListResponse,
  SearchFilters,
  ServerStatus,
  SlashCommandsResponse,
  SteerConversationResponse,
  StopConversationResponse,
  UIInputResponseResult,
  UserLoginDecisionResponse,
  WorkspaceTarget,
} from '../types';

const STREAM_TEXT_UPDATE_INTERVAL_MS = 75;

class ApiService {
  private baseUrl = '';
  private csrfCookieName = 'kodelet_csrf';
  private csrfHeaderName = 'X-CSRF-Token';
  private clientId =
    typeof globalThis.crypto?.randomUUID === 'function'
      ? globalThis.crypto.randomUUID()
      : `client-${Date.now()}-${Math.random().toString(36).slice(2)}`;

  private async request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
    const { headers, ...requestOptions } = options;
    const response = await fetch(`${this.baseUrl}${endpoint}`, {
      ...requestOptions,
      headers: {
        'Content-Type': 'application/json',
        ...headers,
        ...this.getCSRFHeaders(requestOptions.method),
      },
    });

    if (!response.ok) {
      let error: ApiError;
      try {
        error = await response.json();
      } catch {
        error = { error: `HTTP ${response.status}` };
      }
      const requestError = new Error(error.error || error.message || `HTTP ${response.status}`);
      Object.assign(requestError, { status: response.status });
      throw requestError;
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

  private getCSRFCookie(): string {
    if (typeof document === 'undefined') {
      return '';
    }
    const prefix = `${this.csrfCookieName}=`;
    const cookie = document.cookie
      .split(';')
      .map((value) => value.trim())
      .find((value) => value.startsWith(prefix));
    if (!cookie) {
      return '';
    }
    const value = cookie.slice(prefix.length);
    try {
      return decodeURIComponent(value);
    } catch {
      return value;
    }
  }

  private getCSRFHeaders(method = 'GET'): Record<string, string> {
    switch (method.toUpperCase()) {
      case 'GET':
      case 'HEAD':
      case 'OPTIONS':
      case 'TRACE':
        return {};
    }
    const token = this.getCSRFCookie();
    return token ? { [this.csrfHeaderName]: token } : {};
  }

  async getAuthPrincipal(): Promise<AuthPrincipal> {
    return this.request<AuthPrincipal>('/api/auth/me');
  }

  async getServerStatus(): Promise<ServerStatus> {
    return this.request<ServerStatus>('/api/status');
  }

  async getCodexProviderStatus(): Promise<CodexProviderStatus> {
    return this.request<CodexProviderStatus>('/api/providers/codex');
  }

  async startCodexDeviceLogin(): Promise<CodexDeviceLogin> {
    return this.request<CodexDeviceLogin>('/api/providers/codex/device-login', {
      method: 'POST',
    });
  }

  async getCodexDeviceLogin(id: string): Promise<CodexDeviceLogin> {
    return this.request<CodexDeviceLogin>(
      `/api/providers/codex/device-login/${encodeURIComponent(id)}`
    );
  }

  async cancelCodexDeviceLogin(id: string): Promise<void> {
    await this.request(`/api/providers/codex/device-login/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    });
  }

  async getCopilotProviderStatus(): Promise<CopilotProviderStatus> {
    return this.request<CopilotProviderStatus>('/api/providers/copilot');
  }

  async startCopilotDeviceLogin(): Promise<CopilotDeviceLogin> {
    return this.request<CopilotDeviceLogin>('/api/providers/copilot/device-login', {
      method: 'POST',
    });
  }

  async getCopilotDeviceLogin(id: string): Promise<CopilotDeviceLogin> {
    return this.request<CopilotDeviceLogin>(
      `/api/providers/copilot/device-login/${encodeURIComponent(id)}`
    );
  }

  async cancelCopilotDeviceLogin(id: string): Promise<void> {
    await this.request(`/api/providers/copilot/device-login/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    });
  }

  async getAnthropicProviderStatus(): Promise<AnthropicProviderStatus> {
    return this.request<AnthropicProviderStatus>('/api/providers/anthropic');
  }

  async startAnthropicOAuthLogin(): Promise<AnthropicOAuthLogin> {
    return this.request<AnthropicOAuthLogin>('/api/providers/anthropic/oauth-login', {
      method: 'POST',
    });
  }

  async completeAnthropicOAuthLogin(id: string, code: string): Promise<AnthropicOAuthLogin> {
    return this.request<AnthropicOAuthLogin>(
      `/api/providers/anthropic/oauth-login/${encodeURIComponent(id)}/complete`,
      {
        method: 'POST',
        body: JSON.stringify({ code }),
      }
    );
  }

  async cancelAnthropicOAuthLogin(id: string): Promise<void> {
    await this.request(`/api/providers/anthropic/oauth-login/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    });
  }

  async getUserLoginPrincipal(): Promise<AuthPrincipal> {
    return this.request<AuthPrincipal>('/api/auth/v1/device/context');
  }

  async getRunnerEnrollmentPrincipal(): Promise<AuthPrincipal> {
    return this.request<AuthPrincipal>('/api/runner/v1/enrollment/context');
  }

  async submitUserLoginDecision(
    userCode: string,
    decision: 'lookup' | 'approve' | 'deny'
  ): Promise<UserLoginDecisionResponse> {
    return this.request<UserLoginDecisionResponse>('/api/auth/v1/device/decision', {
      method: 'POST',
      body: JSON.stringify({
        userCode,
        decision,
        csrfToken: this.getCSRFCookie(),
      }),
    });
  }

  async submitRunnerEnrollmentDecision(
    userCode: string,
    decision: 'lookup' | 'approve' | 'deny',
    replace = false
  ): Promise<RunnerEnrollmentDecisionResponse> {
    return this.request<RunnerEnrollmentDecisionResponse>('/api/runner/v1/enrollment/decision', {
      method: 'POST',
      body: JSON.stringify({
        userCode,
        decision,
        csrfToken: this.getCSRFCookie(),
        replace,
      }),
    });
  }

  async getConversations(
    filters: Partial<SearchFilters> = {},
    signal?: AbortSignal
  ): Promise<ConversationListResponse> {
    const params = new URLSearchParams();

    if (filters.searchTerm) params.append('search', filters.searchTerm);
    if (filters.cwd) params.append('cwd', filters.cwd);
    if (filters.sortBy) params.append('sortBy', filters.sortBy);
    if (filters.sortOrder) params.append('sortOrder', filters.sortOrder);
    if (filters.limit) params.append('limit', filters.limit.toString());
    if (filters.offset) params.append('offset', filters.offset.toString());

    const queryString = params.toString();
    const endpoint = queryString ? `/api/conversations?${queryString}` : '/api/conversations';

    const response = await this.request<ConversationListResponse>(endpoint, { signal });

    // Ensure conversations is always an array
    if (!response.conversations || !Array.isArray(response.conversations)) {
      response.conversations = [];
    }

    response.conversations = response.conversations.map((conversation) => ({
      ...conversation,
      platform:
        conversation.platform ?? this.extractStringMetadataValue(conversation.metadata, 'platform'),
      api_mode:
        conversation.api_mode ?? this.extractStringMetadataValue(conversation.metadata, 'api_mode'),
    }));

    return response;
  }

  async getConversation(id: string): Promise<Conversation> {
    return this.request<Conversation>(`/api/conversations/${id}`);
  }

  async getRunners(): Promise<RunnerListResponse> {
    return this.request<RunnerListResponse>('/api/runners');
  }

  async discardDraftWorkspace(target: { runnerId: string; conversationId: string }): Promise<void> {
    const params = new URLSearchParams(target);
    return this.request<void>(`/api/workspace/draft?${params}`, {
      method: 'DELETE',
      keepalive: true,
    });
  }

  async heartbeatDraftWorkspace(target: {
    runnerId: string;
    conversationId: string;
  }): Promise<void> {
    const params = new URLSearchParams(target);
    return this.request<void>(`/api/workspace/draft/heartbeat?${params}`, { method: 'POST' });
  }

  async getChatSettings(profile?: string, runnerId?: string): Promise<ChatSettings> {
    const params = new URLSearchParams();
    if (profile) {
      params.append('profile', profile);
    }
    if (runnerId) {
      params.append('runnerId', runnerId);
    }
    const suffix = params.toString();
    return this.request<ChatSettings>(`/api/chat/settings${suffix ? `?${suffix}` : ''}`);
  }

  async getSlashCommands(
    cwd?: string,
    target?: RunnerDiscoveryTarget
  ): Promise<SlashCommandsResponse> {
    const params = new URLSearchParams();
    if (cwd) {
      params.append('cwd', cwd);
    }
    for (const [key, value] of Object.entries(target || {})) {
      if (key === 'profile' && (!value?.trim() || target?.conversationId)) continue;
      if (value !== undefined) params.append(key, value);
    }
    const suffix = params.toString();
    return this.request<SlashCommandsResponse>(
      `/api/chat/slash-commands${suffix ? `?${suffix}` : ''}`
    );
  }

  async getCWDHints(query: string, target?: RunnerDiscoveryTarget): Promise<CWDHintsResponse> {
    const params = new URLSearchParams();
    if (query) {
      params.append('q', query);
    }
    for (const [key, value] of Object.entries(target || {})) {
      if (key === 'profile' && (!value?.trim() || target?.conversationId)) continue;
      if (value !== undefined) params.append(key, value);
    }
    const suffix = params.toString();
    return this.request<CWDHintsResponse>(`/api/chat/cwd-suggestions${suffix ? `?${suffix}` : ''}`);
  }

  async getGitDiff(target: WorkspaceTarget = { kind: 'local' }): Promise<GitDiffResponse> {
    const params = new URLSearchParams();
    if (target.kind === 'local') {
      if (target.cwd) {
        params.append('cwd', target.cwd);
      }
    } else {
      params.append('runnerId', target.runnerId);
      if (target.conversationId) {
        params.append('conversationId', target.conversationId);
      } else if (target.cwd) {
        params.append('cwd', target.cwd);
      }
    }

    const suffix = params.toString();
    return this.request<GitDiffResponse>(`/api/git/diff${suffix ? `?${suffix}` : ''}`);
  }

  createTerminalWebSocket(options: {
    target: WorkspaceTarget;
    rows?: number;
    cols?: number;
  }): WebSocket {
    const params = new URLSearchParams();
    if (options.target.kind === 'local') {
      if (options.target.cwd) {
        params.append('cwd', options.target.cwd);
      }
    } else {
      if (!options.target.runnerId.trim() || !options.target.conversationId?.trim()) {
        throw new Error('A runner and conversation are required to open a terminal');
      }
      params.append('runnerId', options.target.runnerId);
      params.append('conversationId', options.target.conversationId);
      if (options.target.cwd) {
        params.append('cwd', options.target.cwd);
      }
    }
    if (options.rows) {
      params.append('rows', String(options.rows));
    }
    if (options.cols) {
      params.append('cols', String(options.cols));
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const suffix = params.toString();
    return new WebSocket(
      `${protocol}//${window.location.host}/api/terminal/ws${suffix ? `?${suffix}` : ''}`
    );
  }

  async openBrowserSession(target: BrowserTarget, signal?: AbortSignal): Promise<BrowserSession> {
    if (!target.runnerId || !target.conversationId) {
      throw new Error('A runner and conversation are required to open a browser');
    }
    const params = new URLSearchParams({
      runnerId: target.runnerId,
      conversationId: target.conversationId,
    });
    if (target.cwd) {
      params.append('cwd', target.cwd);
    }
    return this.request<BrowserSession>(`/api/browser/session?${params}`, {
      method: 'POST',
      signal,
    });
  }

  createBrowserWebSocket(id: string): WebSocket {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    return new WebSocket(
      `${protocol}//${window.location.host}/api/browser/${encodeURIComponent(id)}/ws`
    );
  }

  browserDevToolsURL(id: string): string {
    const path = `/api/browser/${encodeURIComponent(id)}`;
    const params = new URLSearchParams({
      [window.location.protocol === 'https:' ? 'wss' : 'ws']: `${window.location.host}${path}/ws`,
    });
    return `${path}/devtools/inspector.html?${params}`;
  }

  async stopBrowserSession(id: string): Promise<void> {
    await this.request(`/api/browser/${encodeURIComponent(id)}`, { method: 'DELETE' });
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

  async steerConversation(
    id: string,
    message: string,
    content?: ContentBlock[]
  ): Promise<SteerConversationResponse> {
    const body = content && content.length > 0 ? { message, content } : { message };
    return this.request<SteerConversationResponse>(`/api/conversations/${id}/steer`, {
      method: 'POST',
      body: JSON.stringify(body),
    });
  }

  async stopConversation(id: string): Promise<StopConversationResponse> {
    return this.request<StopConversationResponse>(`/api/conversations/${id}/stop`, {
      method: 'POST',
    });
  }

  async respondToUIInput(
    conversationId: string,
    requestId: string,
    response: { status: 'submitted' | 'dismissed'; value?: string }
  ): Promise<UIInputResponseResult> {
    return this.request<UIInputResponseResult>(
      `/api/conversations/${conversationId}/ui-input/${requestId}`,
      {
        method: 'POST',
        headers: { 'X-Kodelet-Client-ID': this.clientId },
        body: JSON.stringify(response),
      }
    );
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
        'X-Kodelet-Client-ID': this.clientId,
        ...this.getCSRFHeaders('POST'),
      },
      body: JSON.stringify(request),
      signal: options.signal,
    });
    return this.consumeChatStream(response, options.onEvent, request.conversationId);
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
      headers: {
        'X-Kodelet-Client-ID': this.clientId,
        'X-Kodelet-UI-Capabilities': 'interactive',
      },
      signal: options.signal,
    });
    return this.consumeChatStream(response, options.onEvent, conversationId);
  }

  private async consumeChatStream(
    response: Response,
    onEvent: (event: ChatStreamEvent) => void,
    conversationId?: string
  ): Promise<void> {
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
    const pendingPrompts = new Map<string, string | undefined>();
    let pendingDelta: ChatStreamEvent | undefined;
    let deltaTimer: ReturnType<typeof setTimeout> | undefined;
    let lastDeltaDelivery = Number.NEGATIVE_INFINITY;
    let deliveryFailure: { error: unknown } | undefined;
    const flushDelta = () => {
      clearTimeout(deltaTimer);
      deltaTimer = undefined;
      const event = pendingDelta;
      pendingDelta = undefined;
      if (event) {
        lastDeltaDelivery = performance.now();
        onEvent(event);
      }
    };
    const deliver = (line: string) => {
      const event = JSON.parse(line) as ChatStreamEvent;
      conversationId = event.conversation_id || conversationId;
      // Coalesce adjacent text updates, not the network stream. Event boundaries
      // flush immediately so tools, prompts and completion never overtake text.
      if (event.kind === 'text-delta' || event.kind === 'thinking-delta') {
        if (
          pendingDelta &&
          (pendingDelta.kind !== event.kind ||
            pendingDelta.conversation_id !== event.conversation_id)
        ) {
          flushDelta();
        }
        pendingDelta = pendingDelta
          ? { ...event, delta: (pendingDelta.delta || '') + (event.delta || '') }
          : event;
        if (deltaTimer !== undefined) return;
        // Show the first text of a burst at once, then at most one update per interval.
        const wait = lastDeltaDelivery + STREAM_TEXT_UPDATE_INTERVAL_MS - performance.now();
        if (wait <= 0) {
          flushDelta();
          return;
        }
        deltaTimer = setTimeout(() => {
          try {
            flushDelta();
          } catch (error) {
            // Wake the pending read so callback errors reject the stream just
            // like synchronous delivery, rather than escaping from a timer.
            deliveryFailure = { error };
            void reader.cancel().catch(() => {});
          }
        }, wait);
        return;
      }
      flushDelta();
      const id = event.ui_input?.id || event.ui_confirm?.id || event.ui_select?.id;
      if (id) pendingPrompts.set(id, conversationId);
      if (event.kind === 'ui-request-end' && event.ui_request_id) {
        pendingPrompts.delete(event.ui_request_id);
      }
      onEvent(event);
    };

    try {
      while (true) {
        const { done, value } = await reader.read();
        if (deliveryFailure) throw deliveryFailure.error;
        buffer += decoder.decode(value, { stream: !done });

        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) {
            continue;
          }
          deliver(trimmed);
        }

        if (done) {
          const trimmed = buffer.trim();
          if (trimmed) {
            deliver(trimmed);
          }
          return;
        }
      }
    } finally {
      // Also flush on EOF, cancellation or a failed read; do not lose the tail
      // of an interrupted answer or leave callbacks alive after the stream ends.
      try {
        flushDelta();
        // A disconnected stream loses response authority, not execution ownership.
        for (const [id, scope] of pendingPrompts) {
          onEvent({
            kind: 'ui-request-end',
            conversation_id: scope,
            ui_request_id: id,
          });
        }
      } finally {
        await reader.cancel().catch(() => {});
        reader.releaseLock();
      }
    }
  }
}

export const apiService = new ApiService();
export default apiService;
