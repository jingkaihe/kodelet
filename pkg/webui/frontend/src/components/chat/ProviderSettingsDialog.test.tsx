import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ProviderSettingsDialog from './ProviderSettingsDialog';

const mockGetCodexProviderStatus = vi.fn();
const mockStartCodexDeviceLogin = vi.fn();
const mockGetCodexDeviceLogin = vi.fn();
const mockCancelCodexDeviceLogin = vi.fn();
const mockGetCopilotProviderStatus = vi.fn();
const mockStartCopilotDeviceLogin = vi.fn();
const mockGetCopilotDeviceLogin = vi.fn();
const mockCancelCopilotDeviceLogin = vi.fn();
const mockGetAnthropicProviderStatus = vi.fn();
const mockStartAnthropicOAuthLogin = vi.fn();
const mockCompleteAnthropicOAuthLogin = vi.fn();
const mockCancelAnthropicOAuthLogin = vi.fn();

vi.mock('../../services/api', () => ({
  default: {
    getCodexProviderStatus: (...args: unknown[]) => mockGetCodexProviderStatus(...args),
    startCodexDeviceLogin: (...args: unknown[]) => mockStartCodexDeviceLogin(...args),
    getCodexDeviceLogin: (...args: unknown[]) => mockGetCodexDeviceLogin(...args),
    cancelCodexDeviceLogin: (...args: unknown[]) => mockCancelCodexDeviceLogin(...args),
    getCopilotProviderStatus: (...args: unknown[]) => mockGetCopilotProviderStatus(...args),
    startCopilotDeviceLogin: (...args: unknown[]) => mockStartCopilotDeviceLogin(...args),
    getCopilotDeviceLogin: (...args: unknown[]) => mockGetCopilotDeviceLogin(...args),
    cancelCopilotDeviceLogin: (...args: unknown[]) => mockCancelCopilotDeviceLogin(...args),
    getAnthropicProviderStatus: (...args: unknown[]) => mockGetAnthropicProviderStatus(...args),
    startAnthropicOAuthLogin: (...args: unknown[]) => mockStartAnthropicOAuthLogin(...args),
    completeAnthropicOAuthLogin: (...args: unknown[]) =>
      mockCompleteAnthropicOAuthLogin(...args),
    cancelAnthropicOAuthLogin: (...args: unknown[]) => mockCancelAnthropicOAuthLogin(...args),
  },
}));

const flushPromises = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
};

describe('ProviderSettingsDialog', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    mockGetCodexProviderStatus.mockResolvedValue({ provider: 'codex', connected: false });
    mockCancelCodexDeviceLogin.mockResolvedValue(undefined);
    mockGetCopilotProviderStatus.mockResolvedValue({ provider: 'copilot', connected: false });
    mockCancelCopilotDeviceLogin.mockResolvedValue(undefined);
    mockGetAnthropicProviderStatus.mockResolvedValue({
      provider: 'anthropic',
      connected: false,
    });
    mockCancelAnthropicOAuthLogin.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows each subscription provider and its connection action', async () => {
    render(<ProviderSettingsDialog onClose={vi.fn()} />);
    await flushPromises();

    expect(screen.getByRole('dialog', { name: 'Provider settings' })).toBeInTheDocument();
    const chatGPT = screen.getByRole('article', { name: 'ChatGPT' });
    const anthropic = screen.getByRole('article', { name: 'Anthropic' });
    const copilot = screen.getByRole('article', { name: 'GitHub Copilot' });
    expect(within(chatGPT).getByText('Use your subscription for Codex.')).toBeInTheDocument();
    expect(within(chatGPT).getByText('Not connected')).toBeInTheDocument();
    expect(within(chatGPT).getByRole('button', { name: 'Connect ChatGPT' })).toBeEnabled();
    expect(within(anthropic).getByText('Use your Claude subscription.')).toBeInTheDocument();
    expect(within(anthropic).getByText('Not connected')).toBeInTheDocument();
    expect(within(anthropic).getByRole('button', { name: 'Connect Anthropic' })).toBeEnabled();
    expect(within(copilot).getByText('Use your Copilot subscription.')).toBeInTheDocument();
    expect(within(copilot).getByText('Not connected')).toBeInTheDocument();
    expect(
      within(copilot).getByRole('button', { name: 'Connect GitHub Copilot' })
    ).toBeEnabled();
  });

  it('shows the GitHub device code and updates when Copilot sign-in completes', async () => {
    mockStartCopilotDeviceLogin.mockResolvedValue({
      id: 'copilot_login_123',
      status: 'pending',
      verificationUrl: 'https://github.com/login/device',
      userCode: 'USER-123',
    });
    mockGetCopilotDeviceLogin.mockResolvedValue({
      id: 'copilot_login_123',
      status: 'connected',
      message: 'GitHub Copilot subscription connected.',
    });
    render(<ProviderSettingsDialog onClose={vi.fn()} />);
    await flushPromises();

    fireEvent.click(screen.getByRole('button', { name: 'Connect GitHub Copilot' }));
    await flushPromises();

    expect(screen.getByRole('heading', { name: 'Connect GitHub Copilot' })).toBeInTheDocument();
    expect(screen.getByLabelText('GitHub Copilot device code')).toHaveTextContent('USER-123');
    expect(screen.getByRole('link', { name: 'github.com/login/device' })).toHaveAttribute(
      'href',
      'https://github.com/login/device'
    );
    expect(screen.getByRole('button', { name: 'Copy device code' })).not.toHaveTextContent('Copy');

    await act(async () => {
      vi.advanceTimersByTime(1200);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(mockGetCopilotDeviceLogin).toHaveBeenCalledWith('copilot_login_123');
    const copilot = screen.getByRole('article', { name: 'GitHub Copilot' });
    expect(within(copilot).getByText('Connected')).toBeInTheDocument();
    expect(
      within(copilot).getByRole('button', { name: 'Reconnect GitHub Copilot' })
    ).toHaveClass('is-reconnect');
  });

  it('shows the device code and updates when sign-in completes', async () => {
    mockStartCodexDeviceLogin.mockResolvedValue({
      id: 'codex_login_123',
      status: 'pending',
      verificationUrl: 'https://auth.openai.com/codex/device',
      userCode: 'ABCD-EFGH',
    });
    mockGetCodexDeviceLogin.mockResolvedValue({
      id: 'codex_login_123',
      status: 'connected',
      message: 'ChatGPT subscription connected.',
    });
    render(<ProviderSettingsDialog onClose={vi.fn()} />);
    await flushPromises();

    fireEvent.click(screen.getByRole('button', { name: 'Connect ChatGPT' }));
    await flushPromises();

    expect(screen.getByText('Open this link')).toBeInTheDocument();
    expect(screen.getByText('Enter this code')).toBeInTheDocument();
    expect(screen.getByLabelText('ChatGPT device code')).toHaveTextContent('ABCD-EFGH');
    expect(screen.getByRole('link', { name: 'auth.openai.com/codex/device' })).toHaveAttribute(
      'href',
      'https://auth.openai.com/codex/device'
    );
    expect(screen.getByRole('button', { name: 'Copy device code' })).not.toHaveTextContent('Copy');
    expect(screen.getByText('Waiting for sign-in…')).toBeInTheDocument();

    await act(async () => {
      vi.advanceTimersByTime(1200);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(mockGetCodexDeviceLogin).toHaveBeenCalledWith('codex_login_123');
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Reconnect ChatGPT' })).toHaveClass(
      'is-reconnect'
    );
  });

  it('opens the Anthropic authorization link and connects with the returned code', async () => {
    mockStartAnthropicOAuthLogin.mockResolvedValue({
      id: 'anthropic_login_123',
      status: 'pending',
      authorizationUrl: 'https://claude.ai/oauth/authorize?test=1',
    });
    mockCompleteAnthropicOAuthLogin.mockResolvedValue({
      id: 'anthropic_login_123',
      status: 'connected',
      message: 'Anthropic subscription connected.',
    });
    render(<ProviderSettingsDialog onClose={vi.fn()} />);
    await flushPromises();

    fireEvent.click(screen.getByRole('button', { name: 'Connect Anthropic' }));
    await flushPromises();

    expect(screen.getByRole('heading', { name: 'Connect Anthropic' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'claude.ai/oauth/authorize' })).toHaveAttribute(
      'href',
      'https://claude.ai/oauth/authorize?test=1'
    );
    fireEvent.change(screen.getByLabelText('Anthropic authorization code'), {
      target: { value: 'authorization-code#state' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Complete Anthropic sign-in' }));
    await flushPromises();

    expect(mockCompleteAnthropicOAuthLogin).toHaveBeenCalledWith(
      'anthropic_login_123',
      'authorization-code#state'
    );
    const anthropic = screen.getByRole('article', { name: 'Anthropic' });
    expect(within(anthropic).getByText('Connected')).toBeInTheDocument();
    expect(within(anthropic).getByRole('button', { name: 'Reconnect Anthropic' })).toHaveClass(
      'is-reconnect'
    );
  });

  it('cancels a pending device login when the dialog closes', async () => {
    const onClose = vi.fn();
    mockStartCodexDeviceLogin.mockResolvedValue({
      id: 'codex_login_123',
      status: 'pending',
      verificationUrl: 'https://auth.openai.com/codex/device',
      userCode: 'ABCD-EFGH',
    });
    render(<ProviderSettingsDialog onClose={onClose} />);
    await flushPromises();

    fireEvent.click(screen.getByRole('button', { name: 'Connect ChatGPT' }));
    await flushPromises();
    fireEvent.click(screen.getByRole('button', { name: 'Close provider settings' }));

    expect(mockCancelCodexDeviceLogin).toHaveBeenCalledWith('codex_login_123');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('cancels a pending Anthropic login when the dialog closes', async () => {
    const onClose = vi.fn();
    mockStartAnthropicOAuthLogin.mockResolvedValue({
      id: 'anthropic_login_123',
      status: 'pending',
      authorizationUrl: 'https://claude.ai/oauth/authorize?test=1',
    });
    render(<ProviderSettingsDialog onClose={onClose} />);
    await flushPromises();

    fireEvent.click(screen.getByRole('button', { name: 'Connect Anthropic' }));
    await flushPromises();
    fireEvent.click(screen.getByRole('button', { name: 'Close provider settings' }));

    expect(mockCancelAnthropicOAuthLogin).toHaveBeenCalledWith('anthropic_login_123');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('cancels a pending GitHub Copilot login when the dialog closes', async () => {
    const onClose = vi.fn();
    mockStartCopilotDeviceLogin.mockResolvedValue({
      id: 'copilot_login_123',
      status: 'pending',
      verificationUrl: 'https://github.com/login/device',
      userCode: 'USER-123',
    });
    render(<ProviderSettingsDialog onClose={onClose} />);
    await flushPromises();

    fireEvent.click(screen.getByRole('button', { name: 'Connect GitHub Copilot' }));
    await flushPromises();
    fireEvent.click(screen.getByRole('button', { name: 'Close provider settings' }));

    expect(mockCancelCopilotDeviceLogin).toHaveBeenCalledWith('copilot_login_123');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('stops polling when the server reports a terminal session error', async () => {
    mockStartCodexDeviceLogin.mockResolvedValue({
      id: 'codex_login_123',
      status: 'pending',
      verificationUrl: 'https://auth.openai.com/codex/device',
      userCode: 'ABCD-EFGH',
    });
    mockGetCodexDeviceLogin.mockRejectedValue(
      Object.assign(new Error('Codex device login not found'), { status: 404 })
    );
    render(<ProviderSettingsDialog onClose={vi.fn()} />);
    await flushPromises();
    fireEvent.click(screen.getByRole('button', { name: 'Connect ChatGPT' }));
    await flushPromises();

    await act(async () => {
      vi.advanceTimersByTime(1200);
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(screen.getByRole('alert')).toHaveTextContent('Codex device login not found');
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    await act(async () => {
      vi.advanceTimersByTime(2400);
      await Promise.resolve();
    });
    expect(mockGetCodexDeviceLogin).toHaveBeenCalledOnce();
  });
});
