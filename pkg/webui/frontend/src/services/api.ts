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
  RealtimeCapabilities,
  RealtimeEvent,
  RealtimeSession,
  RunnerDiscoveryTarget,
  RunnerEnrollmentDecisionResponse,
  RunnerListResponse,
  SearchFilters,
  ServerStatus,
  SlashCommandsResponse,
  SteerConversationResponse,
  StopConversationResponse,
  ToolInventory,
  ToolInventoryTarget,
  TranscriptionCapabilities,
  TranscriptionResult,
  TurnReceipt,
  UIInputResponseResult,
  UserLoginDecisionResponse,
  WorkspaceTarget,
} from '../types';
import {
  type ChatEventLevel,
  type ChatEventResubscription,
  ChatEventSocket,
  EVENT_SOCKET_UNAVAILABLE,
} from './chatEvents';

class ApiService {
  private baseUrl = '';
  private csrfCookieName = 'kodelet_csrf';
  private csrfHeaderName = 'X-CSRF-Token';
  private clientId =
    typeof globalThis.crypto?.randomUUID === 'function'
      ? globalThis.crypto.randomUUID()
      : `client-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  // One WebSocket for every conversation this page follows; see ChatEventSocket.
  private chatEvents: ChatEventSocket | null = this.createChatEventSocket();

  private async request<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
    const { headers, ...requestOptions } = options;
    const response = await fetch(`${this.baseUrl}${endpoint}`, {
      ...requestOptions,
      headers: {
        ...(requestOptions.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
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

  async getConversation(id: string, signal?: AbortSignal): Promise<Conversation> {
    return this.request<Conversation>(`/api/conversations/${id}`, { signal });
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

  async getTranscriptionCapabilities(signal?: AbortSignal): Promise<TranscriptionCapabilities> {
    return this.request('/api/transcription', { signal });
  }

  async transcribeAudio(
    file: File,
    signal?: AbortSignal,
    language?: string
  ): Promise<TranscriptionResult> {
    const body = new FormData();
    body.append('file', file, file.name);
    if (language) body.append('language', language);
    return this.request('/api/transcription', { method: 'POST', body, signal });
  }

  async getChatSettings(
    profile?: string,
    runnerId?: string,
    workspace?: { cwd?: string; environmentProfile?: string; bootstrap?: boolean }
  ): Promise<ChatSettings> {
    const params = new URLSearchParams();
    if (profile) {
      params.append('profile', profile);
    }
    if (runnerId) {
      params.append('runnerId', runnerId);
    }
    if (workspace?.cwd) params.append('cwd', workspace.cwd);
    if (workspace?.environmentProfile)
      params.append('environmentProfile', workspace.environmentProfile);
    if (workspace?.bootstrap) params.append('bootstrap', 'true');
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

  async getToolInventory(target: ToolInventoryTarget): Promise<ToolInventory> {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(target)) {
      if (key === 'profile' && (!value?.trim() || target.conversationId)) continue;
      if (key === 'cwd' && (!value || target.conversationId)) continue;
      if (value !== undefined) params.append(key, value);
    }
    const suffix = params.toString();
    return this.request<ToolInventory>(`/api/chat/tool-inventory${suffix ? `?${suffix}` : ''}`);
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
    terminalId?: string;
    rows?: number;
    cols?: number;
  }): WebSocket {
    const params = new URLSearchParams();
    if (options.target.kind === 'runner') {
      if (!options.target.runnerId.trim() || !options.target.conversationId?.trim()) {
        throw new Error('A runner and conversation are required to open a terminal');
      }
      params.append('runnerId', options.target.runnerId);
      params.append('conversationId', options.target.conversationId);
    }
    if (options.target.cwd) {
      params.append('cwd', options.target.cwd);
    }
    if (options.rows) {
      params.append('rows', String(options.rows));
    }
    if (options.cols) {
      params.append('cols', String(options.cols));
    }
    if (options.terminalId) {
      params.append('terminalId', options.terminalId);
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const suffix = params.toString();
    return new WebSocket(
      `${protocol}//${window.location.host}/api/terminal/ws${suffix ? `?${suffix}` : ''}`
    );
  }

  async closeTerminal(
    target: WorkspaceTarget,
    terminalId: string,
    sessionId?: string
  ): Promise<void> {
    if (target.kind !== 'runner' || !target.runnerId.trim() || !target.conversationId?.trim()) {
      throw new Error('A runner and conversation are required to close a terminal');
    }
    const params = new URLSearchParams({
      runnerId: target.runnerId,
      conversationId: target.conversationId,
      terminalId,
    });
    if (target.cwd) params.set('cwd', target.cwd);
    if (sessionId) params.set('sessionId', sessionId);
    await this.request<void>(`/api/terminal?${params}`, { method: 'DELETE' });
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

  async getTurnReceipt(conversationId: string, turnId: string): Promise<TurnReceipt> {
    return this.request<TurnReceipt>(
      `/api/conversations/${encodeURIComponent(conversationId)}/turns/${encodeURIComponent(turnId)}`
    );
  }

  /** Replaces the page's event socket, for example with a test double. */
  setChatEventSocket(socket: ChatEventSocket | null): void {
    if (this.chatEvents !== socket) this.chatEvents?.close();
    this.chatEvents = socket;
  }

  private createChatEventSocket(): ChatEventSocket | null {
    if (typeof window === 'undefined' || typeof WebSocket !== 'function') return null;
    return new ChatEventSocket({
      url: () => {
        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const clientId = encodeURIComponent(this.clientId);
        return `${protocol}//${window.location.host}/api/chat/events?clientId=${clientId}`;
      },
      getTurnReceipt: (conversationId, turnId) => this.getTurnReceipt(conversationId, turnId),
    });
  }

  /** The page's connected event socket, which carries every chat event it follows. */
  private async eventSocket(): Promise<ChatEventSocket> {
    const socket = this.chatEvents;
    if (!socket || !(await socket.connect())) throw new Error(EVENT_SOCKET_UNAVAILABLE);
    return socket;
  }

  /**
   * Submits a turn and delivers its events until it ends. The request returns
   * once the turn starts; its events arrive on the page's event socket.
   */
  async streamChat(
    request: ChatRequest & { conversationId: string },
    options: {
      signal?: AbortSignal;
      onEvent: (event: ChatStreamEvent) => void;
    }
  ): Promise<void> {
    const socket = await this.eventSocket();
    return socket.submit(request, {
      ...options,
      post: (turnRequest, headers, signal) =>
        fetch('/api/chat', {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'X-Kodelet-Client-ID': this.clientId,
            ...this.getCSRFHeaders('POST'),
            ...headers,
          },
          body: JSON.stringify(turnRequest),
          signal,
        }),
    });
  }

  async getRealtimeCapabilities(signal?: AbortSignal): Promise<RealtimeCapabilities> {
    return this.request('/api/realtime', { signal });
  }

  async startRealtime(
    offer: string,
    chat: ChatRequest,
    options: { signal?: AbortSignal; voice?: string } = {}
  ): Promise<RealtimeSession> {
    return this.request('/api/realtime', {
      method: 'POST',
      headers: { 'X-Kodelet-Client-ID': this.clientId },
      body: JSON.stringify({ offer, chat, voice: options.voice }),
      signal: options.signal,
    });
  }

  async endRealtime(id: string): Promise<void> {
    await this.request(`/api/realtime/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      headers: { 'X-Kodelet-Client-ID': this.clientId },
      keepalive: true,
    });
  }

  /** Delivers a live voice call's events until the call ends. */
  async streamRealtime(
    id: string,
    options: { signal: AbortSignal; onEvent: (event: RealtimeEvent) => void }
  ): Promise<void> {
    const socket = await this.eventSocket();
    return socket.attachRealtime(id, options);
  }

  /**
   * Follows a conversation until aborted or ended by the server. The lifecycle
   * level, for conversations the page is not showing, omits transcript detail.
   * onResubscribed reports that events may have been missed; see
   * ChatEventSocket.watch.
   */
  async streamConversation(
    conversationId: string,
    options: {
      signal?: AbortSignal;
      level?: ChatEventLevel;
      onEvent: (event: ChatStreamEvent) => void;
      onResubscribed?: (state: ChatEventResubscription) => void;
    }
  ): Promise<void> {
    const socket = await this.eventSocket();
    return socket.watch(conversationId, { ...options, capabilities: 'interactive' });
  }
}

export const apiService = new ApiService();
export default apiService;
