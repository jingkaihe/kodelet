import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ProviderSettingsDialog from './ProviderSettingsDialog';

const mockGetCodexProviderStatus = vi.fn();
const mockStartCodexDeviceLogin = vi.fn();
const mockGetCodexDeviceLogin = vi.fn();
const mockCancelCodexDeviceLogin = vi.fn();

vi.mock('../../services/api', () => ({
  default: {
    getCodexProviderStatus: (...args: unknown[]) => mockGetCodexProviderStatus(...args),
    startCodexDeviceLogin: (...args: unknown[]) => mockStartCodexDeviceLogin(...args),
    getCodexDeviceLogin: (...args: unknown[]) => mockGetCodexDeviceLogin(...args),
    cancelCodexDeviceLogin: (...args: unknown[]) => mockCancelCodexDeviceLogin(...args),
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
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('shows ChatGPT status and the connection action', async () => {
    render(<ProviderSettingsDialog onClose={vi.fn()} />);
    await flushPromises();

    expect(screen.getByRole('dialog', { name: 'Provider settings' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'ChatGPT' })).toBeInTheDocument();
    expect(screen.getByText('Use your subscription for Codex.')).toBeInTheDocument();
    expect(screen.getByText('Not connected')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Connect' })).toBeEnabled();
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

    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
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
    expect(screen.getByRole('button', { name: 'Reconnect' })).toHaveClass('is-reconnect');
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

    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
    await flushPromises();
    fireEvent.click(screen.getByRole('button', { name: 'Close provider settings' }));

    expect(mockCancelCodexDeviceLogin).toHaveBeenCalledWith('codex_login_123');
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
    fireEvent.click(screen.getByRole('button', { name: 'Connect' }));
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
