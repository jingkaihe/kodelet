import React from 'react';
import { Check, CircleAlert, Copy, ExternalLink, X } from 'lucide-react';
import apiService from '../../services/api';
import type {
  AnthropicOAuthLogin,
  AnthropicProviderStatus,
  CodexDeviceLogin,
  CodexProviderStatus,
  CopilotDeviceLogin,
  CopilotProviderStatus,
} from '../../types';
import { copyToClipboard } from '../../utils';
import Spinner from '../Spinner';

const PROVIDER_DIALOG_FOCUSABLE_SELECTOR = [
  'button:not([disabled])',
  '[href]',
  "[tabindex]:not([tabindex='-1'])",
].join(',');
const DEVICE_LOGIN_POLL_INTERVAL = 1200;

const formatProviderURL = (value?: string): string => {
  if (!value) {
    return 'Sign-in page';
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

const AnthropicIcon: React.FC<{ className?: string }> = ({ className }) => (
  <svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 24 24">
    <path d="M17.3041 3.541h-3.6718l6.696 16.918H24Zm-10.6082 0L0 20.459h3.7442l1.3693-3.5527h7.0052l1.3693 3.5528h3.7442L10.5363 3.5409Zm-.3712 10.2232 2.2914-5.9456 2.2914 5.9456Z" />
  </svg>
);

const GitHubIcon: React.FC<{ className?: string }> = ({ className }) => (
  <svg aria-hidden="true" className={className} fill="currentColor" viewBox="0 0 24 24">
    <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.084-.729.084-.729 1.205.084 1.838 1.237 1.838 1.237 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297 24 5.67 18.627.297 12 .297Z" />
  </svg>
);

type ProviderLogin = 'codex' | 'copilot' | 'anthropic';

interface ProviderSettingsDialogProps {
  onClose: () => void;
}

const ProviderSettingsDialog: React.FC<ProviderSettingsDialogProps> = ({ onClose }) => {
  const [codexStatus, setCodexStatus] = React.useState<CodexProviderStatus | null>(null);
  const [copilotStatus, setCopilotStatus] = React.useState<CopilotProviderStatus | null>(null);
  const [anthropicStatus, setAnthropicStatus] = React.useState<AnthropicProviderStatus | null>(null);
  const [codexLoading, setCodexLoading] = React.useState(true);
  const [copilotLoading, setCopilotLoading] = React.useState(true);
  const [anthropicLoading, setAnthropicLoading] = React.useState(true);
  const [providerError, setProviderError] = React.useState<string | null>(null);
  const [activeProvider, setActiveProvider] = React.useState<ProviderLogin | null>(null);
  const [codexLogin, setCodexLogin] = React.useState<CodexDeviceLogin | null>(null);
  const [copilotLogin, setCopilotLogin] = React.useState<CopilotDeviceLogin | null>(null);
  const [anthropicLogin, setAnthropicLogin] = React.useState<AnthropicOAuthLogin | null>(null);
  const [anthropicCode, setAnthropicCode] = React.useState('');
  const [loginStarting, setLoginStarting] = React.useState<ProviderLogin | null>(null);
  const [anthropicSubmitting, setAnthropicSubmitting] = React.useState(false);
  const [pollError, setPollError] = React.useState<string | null>(null);
  const dialogRef = React.useRef<HTMLDivElement | null>(null);
  const closeButtonRef = React.useRef<HTMLButtonElement | null>(null);
  const onCloseRef = React.useRef(onClose);

  const closeDialog = React.useCallback(() => {
    if (activeProvider === 'codex' && codexLogin?.status === 'pending') {
      void apiService.cancelCodexDeviceLogin(codexLogin.id).catch((error) => {
        console.error('Failed to cancel Codex device login', error);
      });
    }
    if (activeProvider === 'copilot' && copilotLogin?.status === 'pending') {
      void apiService.cancelCopilotDeviceLogin(copilotLogin.id).catch((error) => {
        console.error('Failed to cancel GitHub Copilot device login', error);
      });
    }
    if (
      activeProvider === 'anthropic' &&
      anthropicLogin?.status === 'pending' &&
      !anthropicSubmitting
    ) {
      void apiService.cancelAnthropicOAuthLogin(anthropicLogin.id).catch((error) => {
        console.error('Failed to cancel Anthropic OAuth login', error);
      });
    }
    onClose();
  }, [activeProvider, anthropicLogin, anthropicSubmitting, codexLogin, copilotLogin, onClose]);
  onCloseRef.current = closeDialog;

  React.useEffect(() => {
    let disposed = false;
    void apiService
      .getCodexProviderStatus()
      .then((status) => {
        if (!disposed) {
          setCodexStatus(status);
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
          setCodexLoading(false);
        }
      });

    void apiService
      .getAnthropicProviderStatus()
      .then((status) => {
        if (!disposed) {
          setAnthropicStatus(status);
        }
      })
      .catch((error) => {
        if (!disposed) {
          console.error('Failed to load Anthropic provider status', error);
          setProviderError(
            error instanceof Error ? error.message : 'Could not load provider status.'
          );
        }
      })
      .finally(() => {
        if (!disposed) {
          setAnthropicLoading(false);
        }
      });

    void apiService
      .getCopilotProviderStatus()
      .then((status) => {
        if (!disposed) {
          setCopilotStatus(status);
        }
      })
      .catch((error) => {
        if (!disposed) {
          console.error('Failed to load GitHub Copilot provider status', error);
          setProviderError(
            error instanceof Error ? error.message : 'Could not load provider status.'
          );
        }
      })
      .finally(() => {
        if (!disposed) {
          setCopilotLoading(false);
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
    if (activeProvider !== 'codex' || !codexLogin || codexLogin.status !== 'pending') {
      return undefined;
    }

    let disposed = false;
    let pollTimer = 0;
    const poll = async () => {
      try {
        const nextLogin = await apiService.getCodexDeviceLogin(codexLogin.id);
        if (disposed) {
          return;
        }
        setCodexLogin(nextLogin);
        setPollError(null);
        if (nextLogin.status === 'connected') {
          setCodexStatus({ provider: 'codex', connected: true });
          setCodexLogin(null);
          setActiveProvider(null);
          return;
        }
        if (nextLogin.status === 'pending') {
          pollTimer = window.setTimeout(poll, DEVICE_LOGIN_POLL_INTERVAL);
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
            ...codexLogin,
            status: 'failed',
            message:
              error instanceof Error
                ? error.message
                : 'The device sign-in session is no longer available.',
          };
          setCodexLogin(failedLogin);
          return;
        }
        console.error('Failed to poll Codex device login', error);
        setPollError('Connection status is temporarily unavailable. Still waiting…');
        pollTimer = window.setTimeout(poll, DEVICE_LOGIN_POLL_INTERVAL);
      }
    };

    pollTimer = window.setTimeout(poll, DEVICE_LOGIN_POLL_INTERVAL);
    return () => {
      disposed = true;
      window.clearTimeout(pollTimer);
    };
  }, [activeProvider, codexLogin?.id, codexLogin?.status]);

  React.useEffect(() => {
    if (activeProvider !== 'copilot' || !copilotLogin || copilotLogin.status !== 'pending') {
      return undefined;
    }

    let disposed = false;
    let pollTimer = 0;
    const poll = async () => {
      try {
        const nextLogin = await apiService.getCopilotDeviceLogin(copilotLogin.id);
        if (disposed) {
          return;
        }
        setCopilotLogin(nextLogin);
        setPollError(null);
        if (nextLogin.status === 'connected') {
          setCopilotStatus({ provider: 'copilot', connected: true });
          setCopilotLogin(null);
          setActiveProvider(null);
          return;
        }
        if (nextLogin.status === 'pending') {
          pollTimer = window.setTimeout(poll, DEVICE_LOGIN_POLL_INTERVAL);
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
          const failedLogin: CopilotDeviceLogin = {
            ...copilotLogin,
            status: 'failed',
            message:
              error instanceof Error
                ? error.message
                : 'The device sign-in session is no longer available.',
          };
          setCopilotLogin(failedLogin);
          return;
        }
        console.error('Failed to poll GitHub Copilot device login', error);
        setPollError('Connection status is temporarily unavailable. Still waiting…');
        pollTimer = window.setTimeout(poll, DEVICE_LOGIN_POLL_INTERVAL);
      }
    };

    pollTimer = window.setTimeout(poll, DEVICE_LOGIN_POLL_INTERVAL);
    return () => {
      disposed = true;
      window.clearTimeout(pollTimer);
    };
  }, [activeProvider, copilotLogin?.id, copilotLogin?.status]);

  const startCodexLogin = async () => {
    setLoginStarting('codex');
    setProviderError(null);
    setPollError(null);
    try {
      const nextLogin = await apiService.startCodexDeviceLogin();
      if (nextLogin.status === 'connected') {
        setCodexStatus({ provider: 'codex', connected: true });
        setCodexLogin(null);
        setActiveProvider(null);
      } else {
        setCodexLogin(nextLogin);
        setActiveProvider('codex');
      }
    } catch (error) {
      console.error('Failed to start Codex device login', error);
      setProviderError(error instanceof Error ? error.message : 'Could not start ChatGPT sign-in.');
    } finally {
      setLoginStarting(null);
    }
  };

  const startCopilotLogin = async () => {
    setLoginStarting('copilot');
    setProviderError(null);
    setPollError(null);
    try {
      const nextLogin = await apiService.startCopilotDeviceLogin();
      if (nextLogin.status === 'connected') {
        setCopilotStatus({ provider: 'copilot', connected: true });
        setCopilotLogin(null);
        setActiveProvider(null);
      } else {
        setCopilotLogin(nextLogin);
        setActiveProvider('copilot');
      }
    } catch (error) {
      console.error('Failed to start GitHub Copilot device login', error);
      setProviderError(
        error instanceof Error ? error.message : 'Could not start GitHub Copilot sign-in.'
      );
    } finally {
      setLoginStarting(null);
    }
  };

  const startAnthropicLogin = async () => {
    setLoginStarting('anthropic');
    setProviderError(null);
    setAnthropicCode('');
    try {
      const nextLogin = await apiService.startAnthropicOAuthLogin();
      if (nextLogin.status === 'connected') {
        setAnthropicStatus({ provider: 'anthropic', connected: true });
        setAnthropicLogin(null);
        setActiveProvider(null);
      } else {
        setAnthropicLogin(nextLogin);
        setActiveProvider('anthropic');
      }
    } catch (error) {
      console.error('Failed to start Anthropic OAuth login', error);
      setProviderError(error instanceof Error ? error.message : 'Could not start Anthropic sign-in.');
    } finally {
      setLoginStarting(null);
    }
  };

  const completeAnthropicLogin = async () => {
    if (!anthropicLogin || !anthropicCode.trim()) {
      return;
    }
    setAnthropicSubmitting(true);
    setProviderError(null);
    try {
      const nextLogin = await apiService.completeAnthropicOAuthLogin(
        anthropicLogin.id,
        anthropicCode.trim()
      );
      if (nextLogin.status === 'connected') {
        setAnthropicStatus({ provider: 'anthropic', connected: true });
        setAnthropicLogin(null);
        setAnthropicCode('');
        setActiveProvider(null);
      } else {
        setAnthropicLogin(nextLogin);
      }
    } catch (error) {
      console.error('Failed to complete Anthropic OAuth login', error);
      setAnthropicLogin({
        ...anthropicLogin,
        status: 'failed',
        message: error instanceof Error ? error.message : 'Could not complete Anthropic sign-in.',
      });
    } finally {
      setAnthropicSubmitting(false);
    }
  };

  const codexConnected = codexStatus?.connected === true;
  const copilotConnected = copilotStatus?.connected === true;
  const anthropicConnected = anthropicStatus?.connected === true;
  const activeProviderName =
    activeProvider === 'anthropic'
      ? 'Anthropic'
      : activeProvider === 'copilot'
        ? 'GitHub Copilot'
        : 'ChatGPT';
  const activeDeviceLogin =
    activeProvider === 'copilot'
      ? copilotLogin
      : activeProvider === 'codex'
        ? codexLogin
        : null;
  const activeLoginMessage =
    activeProvider === 'anthropic'
      ? anthropicLogin?.message
      : activeProvider === 'copilot'
        ? copilotLogin?.message
        : codexLogin?.message;

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
          {!activeProvider ? (
            <section className="provider-settings-list" aria-label="Providers">
              <article aria-label="ChatGPT" className="provider-settings-provider">
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
                      className={`provider-settings-status${codexConnected ? ' is-connected' : ''}`}
                      role="status"
                    >
                      {codexLoading ? (
                        <>
                          <Spinner className="provider-settings-status-spinner" />
                          Checking
                        </>
                      ) : codexConnected ? (
                        <>
                          <Check aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2.2} />
                          Connected
                        </>
                      ) : (
                        'Not connected'
                      )}
                    </span>
                    <button
                      aria-label={`${codexConnected ? 'Reconnect' : 'Connect'} ChatGPT`}
                      className={`panel-action-button provider-settings-provider-action${codexConnected ? ' is-reconnect' : ''}`}
                      disabled={codexLoading || loginStarting !== null}
                      onClick={() => void startCodexLogin()}
                      type="button"
                    >
                      {loginStarting === 'codex' ? (
                        <Spinner className="provider-settings-button-spinner" />
                      ) : null}
                      {loginStarting === 'codex'
                        ? 'Connecting…'
                        : codexConnected
                          ? 'Reconnect'
                          : 'Connect'}
                    </button>
                  </div>
                </div>
              </article>
              <article aria-label="Anthropic" className="provider-settings-provider">
                <div className="provider-settings-provider-icon" aria-hidden="true">
                  <AnthropicIcon className="h-4 w-4" />
                </div>
                <div className="provider-settings-provider-body">
                  <div className="provider-settings-provider-copy">
                    <h3>Anthropic</h3>
                    <p>Use your Claude subscription.</p>
                  </div>
                  <div className="provider-settings-provider-controls">
                    <span
                      className={`provider-settings-status${anthropicConnected ? ' is-connected' : ''}`}
                      role="status"
                    >
                      {anthropicLoading ? (
                        <>
                          <Spinner className="provider-settings-status-spinner" />
                          Checking
                        </>
                      ) : anthropicConnected ? (
                        <>
                          <Check aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2.2} />
                          Connected
                        </>
                      ) : (
                        'Not connected'
                      )}
                    </span>
                    <button
                      aria-label={`${anthropicConnected ? 'Reconnect' : 'Connect'} Anthropic`}
                      className={`panel-action-button provider-settings-provider-action${anthropicConnected ? ' is-reconnect' : ''}`}
                      disabled={anthropicLoading || loginStarting !== null}
                      onClick={() => void startAnthropicLogin()}
                      type="button"
                    >
                      {loginStarting === 'anthropic' ? (
                        <Spinner className="provider-settings-button-spinner" />
                      ) : null}
                      {loginStarting === 'anthropic'
                        ? 'Connecting…'
                        : anthropicConnected
                          ? 'Reconnect'
                          : 'Connect'}
                    </button>
                  </div>
                </div>
              </article>
              <article aria-label="GitHub Copilot" className="provider-settings-provider">
                <div className="provider-settings-provider-icon" aria-hidden="true">
                  <GitHubIcon className="h-4 w-4" />
                </div>
                <div className="provider-settings-provider-body">
                  <div className="provider-settings-provider-copy">
                    <h3>GitHub Copilot</h3>
                    <p>Use your Copilot subscription.</p>
                  </div>
                  <div className="provider-settings-provider-controls">
                    <span
                      className={`provider-settings-status${copilotConnected ? ' is-connected' : ''}`}
                      role="status"
                    >
                      {copilotLoading ? (
                        <>
                          <Spinner className="provider-settings-status-spinner" />
                          Checking
                        </>
                      ) : copilotConnected ? (
                        <>
                          <Check aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2.2} />
                          Connected
                        </>
                      ) : (
                        'Not connected'
                      )}
                    </span>
                    <button
                      aria-label={`${copilotConnected ? 'Reconnect' : 'Connect'} GitHub Copilot`}
                      className={`panel-action-button provider-settings-provider-action${copilotConnected ? ' is-reconnect' : ''}`}
                      disabled={copilotLoading || loginStarting !== null}
                      onClick={() => void startCopilotLogin()}
                      type="button"
                    >
                      {loginStarting === 'copilot' ? (
                        <Spinner className="provider-settings-button-spinner" />
                      ) : null}
                      {loginStarting === 'copilot'
                        ? 'Connecting…'
                        : copilotConnected
                          ? 'Reconnect'
                          : 'Connect'}
                    </button>
                  </div>
                </div>
              </article>
            </section>
          ) : activeDeviceLogin?.status === 'pending' ? (
            <section className="provider-device-flow" aria-label={`Connect ${activeProviderName}`}>
              <h3 className="provider-device-title">Connect {activeProviderName}</h3>
              <ol className="provider-device-steps">
                <li className="provider-device-step">
                  <span className="provider-device-step-index" aria-hidden="true">
                    1
                  </span>
                  <div className="provider-device-step-content">
                    <span className="provider-device-step-label">Open this link</span>
                    <a
                      className="tool-action-link provider-device-link"
                      href={activeDeviceLogin.verificationUrl}
                      rel="noreferrer"
                      target="_blank"
                    >
                      <span>{formatProviderURL(activeDeviceLogin.verificationUrl)}</span>
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
                    <output
                      aria-label={`${activeProviderName} device code`}
                      className="provider-device-code"
                    >
                      {activeDeviceLogin.userCode}
                    </output>
                  </div>
                  <button
                    aria-label="Copy device code"
                    className="panel-action-button provider-device-step-action provider-device-code-copy"
                    onClick={() => void copyToClipboard(activeDeviceLogin.userCode || '')}
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
          ) : activeProvider === 'anthropic' && anthropicLogin?.status === 'pending' ? (
            <form
              aria-label="Connect Anthropic"
              className="provider-device-flow"
              onSubmit={(event) => {
                event.preventDefault();
                void completeAnthropicLogin();
              }}
            >
              <h3 className="provider-device-title">Connect Anthropic</h3>
              <ol className="provider-device-steps">
                <li className="provider-device-step">
                  <span className="provider-device-step-index" aria-hidden="true">
                    1
                  </span>
                  <div className="provider-device-step-content">
                    <span className="provider-device-step-label">Open this link</span>
                    <a
                      className="tool-action-link provider-device-link"
                      href={anthropicLogin.authorizationUrl}
                      rel="noreferrer"
                      target="_blank"
                    >
                      <span>{formatProviderURL(anthropicLogin.authorizationUrl)}</span>
                      <ExternalLink aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={1.9} />
                    </a>
                  </div>
                </li>
                <li className="provider-device-step">
                  <span className="provider-device-step-index" aria-hidden="true">
                    2
                  </span>
                  <div className="provider-device-step-content">
                    <label className="provider-device-step-label" htmlFor="anthropic-authorization-code">
                      Paste the code
                    </label>
                    <input
                      aria-label="Anthropic authorization code"
                      autoComplete="off"
                      className="provider-auth-code-input"
                      disabled={anthropicSubmitting}
                      id="anthropic-authorization-code"
                      onChange={(event) => setAnthropicCode(event.target.value)}
                      placeholder="code#state"
                      spellCheck={false}
                      value={anthropicCode}
                    />
                  </div>
                  <button
                    aria-label="Complete Anthropic sign-in"
                    className="panel-action-button provider-device-step-action provider-auth-code-submit"
                    disabled={!anthropicCode.trim() || anthropicSubmitting}
                    type="submit"
                  >
                    {anthropicSubmitting ? (
                      <Spinner className="provider-settings-button-spinner" />
                    ) : (
                      'Connect'
                    )}
                  </button>
                </li>
              </ol>
            </form>
          ) : (
            <section
              className="provider-device-flow"
              aria-label={`${activeProviderName} sign-in failed`}
            >
              <h3 className="provider-device-title">Couldn’t connect {activeProviderName}</h3>
              <div className="provider-settings-error" role="alert">
                <CircleAlert aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
                <span>{activeLoginMessage || 'Sign-in did not complete.'}</span>
              </div>
              <button
                className="panel-action-button provider-device-retry"
                disabled={loginStarting !== null}
                onClick={() =>
                  void (activeProvider === 'anthropic'
                    ? startAnthropicLogin()
                    : activeProvider === 'copilot'
                      ? startCopilotLogin()
                      : startCodexLogin())
                }
                type="button"
              >
                {loginStarting !== null ? (
                  <Spinner className="provider-settings-button-spinner" />
                ) : null}
                {loginStarting !== null ? 'Connecting…' : 'Try again'}
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
