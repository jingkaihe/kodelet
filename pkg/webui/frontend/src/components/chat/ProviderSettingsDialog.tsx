import React from 'react';
import { Check, CircleAlert, Copy, ExternalLink, X } from 'lucide-react';
import apiService from '../../services/api';
import type { CodexDeviceLogin, CodexProviderStatus } from '../../types';
import { copyToClipboard } from '../../utils';
import Spinner from '../Spinner';

const PROVIDER_DIALOG_FOCUSABLE_SELECTOR = [
  'button:not([disabled])',
  '[href]',
  "[tabindex]:not([tabindex='-1'])",
].join(',');
const CODEX_DEVICE_LOGIN_POLL_INTERVAL = 1200;

const formatVerificationURL = (value?: string): string => {
  if (!value) {
    return 'ChatGPT sign-in';
  }
  try {
    const url = new URL(value);
    return `${url.host}${url.pathname === '/' ? '' : url.pathname}`;
  } catch {
    return value;
  }
};

const OpenAIIcon: React.FC<{ className?: string }> = ({ className }) => (
  <svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 24 24">
    <path d="M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.0729zm-9.022 12.6081a4.4755 4.4755 0 0 1-2.8764-1.0408l.1419-.0804 4.7783-2.7582a.7948.7948 0 0 0 .3927-.6813v-6.7369l2.02 1.1686a.071.071 0 0 1 .038.052v5.5826a4.504 4.504 0 0 1-4.4945 4.4944zm-9.6607-4.1254a4.4708 4.4708 0 0 1-.5346-3.0137l.142.0852 4.783 2.7582a.7712.7712 0 0 0 .7806 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4992 4.4992 0 0 1-6.1408-1.6464zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7664.7664 0 0 0 .3879.6765l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z" />
  </svg>
);

interface ProviderSettingsDialogProps {
  onClose: () => void;
}

const ProviderSettingsDialog: React.FC<ProviderSettingsDialogProps> = ({ onClose }) => {
  const [providerStatus, setProviderStatus] = React.useState<CodexProviderStatus | null>(null);
  const [providerLoading, setProviderLoading] = React.useState(true);
  const [providerError, setProviderError] = React.useState<string | null>(null);
  const [login, setLogin] = React.useState<CodexDeviceLogin | null>(null);
  const [loginStarting, setLoginStarting] = React.useState(false);
  const [pollError, setPollError] = React.useState<string | null>(null);
  const dialogRef = React.useRef<HTMLDivElement | null>(null);
  const closeButtonRef = React.useRef<HTMLButtonElement | null>(null);
  const onCloseRef = React.useRef(onClose);

  const closeDialog = React.useCallback(() => {
    if (login?.status === 'pending') {
      void apiService.cancelCodexDeviceLogin(login.id).catch((error) => {
        console.error('Failed to cancel Codex device login', error);
      });
    }
    onClose();
  }, [login, onClose]);
  onCloseRef.current = closeDialog;

  React.useEffect(() => {
    let disposed = false;
    void apiService
      .getCodexProviderStatus()
      .then((status) => {
        if (!disposed) {
          setProviderStatus(status);
          setProviderError(null);
        }
      })
      .catch((error) => {
        if (!disposed) {
          console.error('Failed to load Codex provider status', error);
          setProviderError(
            error instanceof Error ? error.message : 'Could not load provider status.'
          );
        }
      })
      .finally(() => {
        if (!disposed) {
          setProviderLoading(false);
        }
      });

    return () => {
      disposed = true;
    };
  }, []);

  React.useEffect(() => {
    const previousFocus =
      document.activeElement instanceof HTMLElement && document.activeElement !== document.body
        ? document.activeElement
        : null;
    const focusTimer = window.setTimeout(() => closeButtonRef.current?.focus(), 0);
    const handleKeyDown = (event: KeyboardEvent) => {
      const dialog = dialogRef.current;
      if (!dialog) {
        return;
      }

      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        onCloseRef.current();
        return;
      }

      if (event.key !== 'Tab') {
        return;
      }

      const focusable = Array.from(
        dialog.querySelectorAll<HTMLElement>(PROVIDER_DIALOG_FOCUSABLE_SELECTOR)
      ).filter((element) => element.getClientRects().length > 0);
      if (focusable.length === 0) {
        event.preventDefault();
        dialog.focus();
        return;
      }

      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', handleKeyDown, true);
    return () => {
      window.clearTimeout(focusTimer);
      document.removeEventListener('keydown', handleKeyDown, true);
      previousFocus?.focus();
    };
  }, []);

  React.useEffect(() => {
    if (!login || login.status !== 'pending') {
      return undefined;
    }

    let disposed = false;
    let pollTimer = 0;
    const poll = async () => {
      try {
        const nextLogin = await apiService.getCodexDeviceLogin(login.id);
        if (disposed) {
          return;
        }
        setLogin(nextLogin);
        setPollError(null);
        if (nextLogin.status === 'connected') {
          setProviderStatus({ provider: 'codex', connected: true });
          setLogin(null);
          return;
        }
        if (nextLogin.status === 'pending') {
          pollTimer = window.setTimeout(poll, CODEX_DEVICE_LOGIN_POLL_INTERVAL);
        }
      } catch (error) {
        if (disposed) {
          return;
        }
        const status =
          typeof error === 'object' && error !== null && 'status' in error
            ? Number((error as { status?: unknown }).status)
            : 0;
        if (status >= 400 && status < 500 && status !== 429) {
          const failedLogin: CodexDeviceLogin = {
            ...login,
            status: 'failed',
            message:
              error instanceof Error
                ? error.message
                : 'The device sign-in session is no longer available.',
          };
          setLogin(failedLogin);
          return;
        }
        console.error('Failed to poll Codex device login', error);
        setPollError('Connection status is temporarily unavailable. Still waiting…');
        pollTimer = window.setTimeout(poll, CODEX_DEVICE_LOGIN_POLL_INTERVAL);
      }
    };

    pollTimer = window.setTimeout(poll, CODEX_DEVICE_LOGIN_POLL_INTERVAL);
    return () => {
      disposed = true;
      window.clearTimeout(pollTimer);
    };
  }, [login?.id, login?.status]);

  const startLogin = async () => {
    setLoginStarting(true);
    setProviderError(null);
    setPollError(null);
    try {
      const nextLogin = await apiService.startCodexDeviceLogin();
      if (nextLogin.status === 'connected') {
        setProviderStatus({ provider: 'codex', connected: true });
        setLogin(null);
      } else {
        setLogin(nextLogin);
      }
    } catch (error) {
      console.error('Failed to start Codex device login', error);
      setProviderError(error instanceof Error ? error.message : 'Could not start ChatGPT sign-in.');
    } finally {
      setLoginStarting(false);
    }
  };

  const connected = providerStatus?.connected === true;
  const loginFailed = login?.status === 'failed' || login?.status === 'canceled';
  const showDeviceFlow = login?.status === 'pending' || loginFailed;

  return (
    <div className="new-chat-dialog-backdrop provider-settings-backdrop">
      <div
        aria-labelledby="provider-settings-title"
        aria-modal="true"
        className="provider-settings-dialog surface-panel"
        data-testid="provider-settings-dialog"
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="new-chat-context-header provider-settings-header">
          <h2 className="new-chat-context-title" id="provider-settings-title">
            Provider settings
          </h2>
          <button
            aria-label="Close provider settings"
            className="new-chat-context-close"
            onClick={closeDialog}
            ref={closeButtonRef}
            type="button"
          >
            <X className="h-4 w-4" strokeWidth={1.8} />
          </button>
        </header>

        <div className="provider-settings-content">
          {!showDeviceFlow ? (
            <section className="provider-settings-list" aria-label="Providers">
              <article className="provider-settings-provider">
                <div className="provider-settings-provider-icon" aria-hidden="true">
                  <OpenAIIcon className="h-4 w-4" />
                </div>
                <div className="provider-settings-provider-body">
                  <div className="provider-settings-provider-copy">
                    <h3>ChatGPT</h3>
                    <p>Use your subscription for Codex.</p>
                  </div>
                  <div className="provider-settings-provider-controls">
                    <span
                      className={`provider-settings-status${connected ? ' is-connected' : ''}`}
                      role="status"
                    >
                      {providerLoading ? (
                        <>
                          <Spinner className="provider-settings-status-spinner" />
                          Checking
                        </>
                      ) : connected ? (
                        <>
                          <Check aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2.2} />
                          Connected
                        </>
                      ) : (
                        'Not connected'
                      )}
                    </span>
                    <button
                      className={`panel-action-button provider-settings-provider-action${connected ? ' is-reconnect' : ''}`}
                      disabled={providerLoading || loginStarting}
                      onClick={() => void startLogin()}
                      type="button"
                    >
                      {loginStarting ? (
                        <Spinner className="provider-settings-button-spinner" />
                      ) : null}
                      {loginStarting ? 'Connecting…' : connected ? 'Reconnect' : 'Connect'}
                    </button>
                  </div>
                </div>
              </article>
            </section>
          ) : login?.status === 'pending' ? (
            <section className="provider-device-flow" aria-label="Connect ChatGPT">
              <h3 className="provider-device-title">Connect ChatGPT</h3>
              <ol className="provider-device-steps">
                <li className="provider-device-step">
                  <span className="provider-device-step-index" aria-hidden="true">
                    1
                  </span>
                  <div className="provider-device-step-content">
                    <span className="provider-device-step-label">Open this link</span>
                    <a
                      className="tool-action-link provider-device-link"
                      href={login.verificationUrl}
                      rel="noreferrer"
                      target="_blank"
                    >
                      <span>{formatVerificationURL(login.verificationUrl)}</span>
                      <ExternalLink aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.9} />
                    </a>
                  </div>
                </li>
                <li className="provider-device-step">
                  <span className="provider-device-step-index" aria-hidden="true">
                    2
                  </span>
                  <div className="provider-device-step-content">
                    <span className="provider-device-step-label">Enter this code</span>
                    <output aria-label="ChatGPT device code" className="provider-device-code">
                      {login.userCode}
                    </output>
                  </div>
                  <button
                    aria-label="Copy device code"
                    className="panel-action-button provider-device-step-action provider-device-code-copy"
                    onClick={() => void copyToClipboard(login.userCode || '')}
                    title="Copy device code"
                    type="button"
                  >
                    <Copy aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                  </button>
                </li>
              </ol>

              <div className="provider-device-progress" role="status">
                <Spinner className="provider-device-status-spinner" />
                <span>{pollError || 'Waiting for sign-in…'}</span>
              </div>
            </section>
          ) : (
            <section className="provider-device-flow" aria-label="ChatGPT sign-in failed">
              <h3 className="provider-device-title">Couldn’t connect ChatGPT</h3>
              <div className="provider-settings-error" role="alert">
                <CircleAlert aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                <span>{login?.message || 'Sign-in did not complete.'}</span>
              </div>
              <button
                className="panel-action-button provider-device-retry"
                disabled={loginStarting}
                onClick={() => void startLogin()}
                type="button"
              >
                {loginStarting ? <Spinner className="provider-settings-button-spinner" /> : null}
                {loginStarting ? 'Connecting…' : 'Try again'}
              </button>
            </section>
          )}

          {providerError ? (
            <div className="provider-settings-error" role="alert">
              <CircleAlert aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
              <span>{providerError}</span>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
};

export default ProviderSettingsDialog;
