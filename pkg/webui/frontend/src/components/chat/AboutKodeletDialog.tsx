import { Check, X } from 'lucide-react';
import React from 'react';
import apiService from '../../services/api';
import type { Runner, ServerStatus } from '../../types';
import KodeletBrand from '../KodeletBrand';
import Spinner from '../Spinner';

interface AboutKodeletDialogProps {
  onClose: () => void;
  runner?: Runner;
  terminalAuthorized: boolean;
}

const AboutKodeletDialog: React.FC<AboutKodeletDialogProps> = ({
  onClose,
  runner,
  terminalAuthorized,
}) => {
  const [status, setStatus] = React.useState<ServerStatus | null>(null);
  const [error, setError] = React.useState(false);
  const [attempt, setAttempt] = React.useState(0);
  const dialogRef = React.useRef<HTMLDivElement | null>(null);
  const closeButtonRef = React.useRef<HTMLButtonElement | null>(null);
  const onCloseRef = React.useRef(onClose);
  onCloseRef.current = onClose;

  // biome-ignore lint/correctness/useExhaustiveDependencies(attempt): Retry reloads the instance version.
  React.useEffect(() => {
    let disposed = false;
    void apiService.getServerStatus().then(
      (result) => {
        if (!disposed) setStatus(result);
      },
      () => {
        if (!disposed) setError(true);
      }
    );
    return () => {
      disposed = true;
    };
  }, [attempt]);

  React.useEffect(() => {
    const previousFocus =
      document.activeElement instanceof HTMLElement && document.activeElement !== document.body
        ? document.activeElement
        : null;
    const focusTimer = window.setTimeout(() => closeButtonRef.current?.focus(), 0);
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        onCloseRef.current();
        return;
      }
      if (event.key !== 'Tab') return;

      const dialog = dialogRef.current;
      const buttons = dialog?.querySelectorAll<HTMLButtonElement>('button:not([disabled])');
      if (!dialog || !buttons?.length) return;
      const first = buttons[0];
      const last = buttons[buttons.length - 1];
      if (!dialog.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      } else if (event.shiftKey && document.activeElement === first) {
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
      // The menu item unmounts on opening; return to its persistent account trigger.
      window.setTimeout(() => {
        const target = previousFocus?.isConnected
          ? previousFocus
          : document.querySelector<HTMLElement>(
              '.sidebar-account-trigger, [data-testid="sidebar-about-kodelet"]'
            );
        if (!target?.closest('[inert]')) target?.focus();
      }, 0);
    };
  }, []);

  const runnerAvailable = Boolean(
    runner?.connected && (runner.status === 'idle' || runner.status === 'busy')
  );
  const capabilities = [
    { label: 'Git diffs', enabled: runner?.workspaceGitDiff },
    { label: 'Terminal', enabled: runner?.workspaceTerminal && terminalAuthorized },
    { label: 'Browser', enabled: runner?.workspaceBrowser && terminalAuthorized },
    { label: 'Slash commands', enabled: runner?.workspaceDiscovery },
    { label: 'Working directory', enabled: runner?.workspaceCwd },
    { label: 'Parallel runs', enabled: runner?.concurrentRuns },
  ];
  const gitCommit = status?.gitCommit === 'unknown' ? undefined : status?.gitCommit;
  const buildDate = status?.buildTime ? new Date(status.buildTime) : null;

  return (
    <div className="new-chat-dialog-backdrop about-kodelet-backdrop">
      <div
        aria-label="About Kodelet"
        aria-modal="true"
        className="about-kodelet-dialog surface-panel"
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="new-chat-context-header about-kodelet-header">
          <KodeletBrand />
          <button
            aria-label="Close about Kodelet"
            className="new-chat-context-close"
            onClick={onClose}
            ref={closeButtonRef}
            type="button"
          >
            <X aria-hidden="true" className="h-4 w-4" strokeWidth={1.8} />
          </button>
        </header>

        <div className="about-kodelet-content">
          <section aria-label="Instance details">
            <dl className="about-kodelet-details">
              <div>
                <dt>Server version</dt>
                <dd>
                  {status ? (
                    status.version
                  ) : error ? (
                    'Unavailable'
                  ) : (
                    <output className="about-kodelet-loading">
                      <Spinner /> Loading version…
                    </output>
                  )}
                </dd>
              </div>
              {status ? (
                <>
                  <div>
                    <dt>Build commit</dt>
                    <dd title={gitCommit}>{gitCommit?.slice(0, 7) || 'Unknown'}</dd>
                  </div>
                  <div>
                    <dt>Build date</dt>
                    <dd>
                      {buildDate && !Number.isNaN(buildDate.getTime()) ? (
                        <time dateTime={buildDate.toISOString()} title={status.buildTime}>
                          {buildDate.toLocaleDateString(undefined, {
                            year: 'numeric',
                            month: 'short',
                            day: 'numeric',
                            timeZone: 'UTC',
                          })}
                        </time>
                      ) : (
                        'Unknown'
                      )}
                    </dd>
                  </div>
                </>
              ) : null}
              {runner ? (
                <>
                  <div>
                    <dt>Runner</dt>
                    <dd>{runner.displayName || runner.workspace.name}</dd>
                  </div>
                  <div>
                    <dt>Runner version</dt>
                    <dd>{runner.kodeletVersion || 'Unknown'}</dd>
                  </div>
                </>
              ) : null}
            </dl>
            {error ? (
              <div className="about-kodelet-error">
                <p role="alert">Could not load the server version.</p>
                <button
                  className="panel-action-button"
                  onClick={() => {
                    setError(false);
                    setAttempt((current) => current + 1);
                    closeButtonRef.current?.focus();
                  }}
                  type="button"
                >
                  Try again
                </button>
              </div>
            ) : null}
          </section>

          <section aria-labelledby="about-kodelet-capabilities">
            <h3 id="about-kodelet-capabilities">Capabilities</h3>
            {!runner || !runnerAvailable ? (
              <p className="about-kodelet-description">
                {!runner
                  ? 'Select a workspace to see its capabilities.'
                  : 'This runner is not ready. Capabilities are unavailable until it reconnects.'}
              </p>
            ) : null}
            <dl className="about-kodelet-capabilities">
              {capabilities.map(({ label, enabled }) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd className={runnerAvailable && enabled ? 'is-enabled' : undefined}>
                    {runnerAvailable && enabled ? (
                      <Check aria-hidden="true" className="h-3.5 w-3.5" strokeWidth={2} />
                    ) : null}
                    {!runnerAvailable ? 'Unavailable' : enabled ? 'Enabled' : 'Not enabled'}
                  </dd>
                </div>
              ))}
            </dl>
          </section>
        </div>
      </div>
    </div>
  );
};

export default AboutKodeletDialog;
