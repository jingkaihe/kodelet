import { Check, X } from 'lucide-react';
import React from 'react';
import { useDialogFocus } from '../../features/chat/useDialogFocus';
import apiService from '../../services/api';
import type { Runner, ServerStatus, Usage } from '../../types';
import KodeletBrand from '../KodeletBrand';
import Spinner from '../Spinner';

interface AboutKodeletDialogProps {
  onClose: () => void;
  runner?: Runner;
  terminalAuthorized: boolean;
  usage?: Usage;
}

const AboutKodeletDialog: React.FC<AboutKodeletDialogProps> = ({
  onClose,
  runner,
  terminalAuthorized,
  usage,
}) => {
  const [tab, setTab] = React.useState<'about' | 'context'>('about');
  const [status, setStatus] = React.useState<ServerStatus | null>(null);
  const [error, setError] = React.useState(false);
  const [attempt, setAttempt] = React.useState(0);
  const { dialogRef, closeButtonRef } = useDialogFocus(
    onClose,
    '.sidebar-account-trigger, [data-testid="sidebar-about-kodelet"]'
  );

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

  const runnerAvailable = Boolean(
    runner?.connected && (runner.status === 'idle' || runner.status === 'busy')
  );
  const capabilities = [
    { label: 'Git diffs', enabled: runner?.workspaceGitDiff },
    { label: 'Terminal', enabled: runner?.workspaceTerminal && terminalAuthorized },
    { label: 'Browser', enabled: runner?.workspaceBrowser && terminalAuthorized },
    { label: 'Slash commands', enabled: runner?.workspaceDiscovery },
    { label: 'Working directory', enabled: runner?.workspaceCwd },
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

        <div aria-label="About Kodelet tabs" className="about-kodelet-tabs" role="tablist">
          {(['about', 'context'] as const).map((view) => (
            <button
              aria-controls="about-kodelet-panel"
              aria-selected={tab === view}
              id={`about-kodelet-tab-${view}`}
              key={view}
              onClick={() => setTab(view)}
              onKeyDown={(event) => {
                let next: typeof tab;
                switch (event.key) {
                  case 'ArrowLeft':
                  case 'ArrowRight':
                    next = tab === 'about' ? 'context' : 'about';
                    break;
                  case 'Home':
                    next = 'about';
                    break;
                  case 'End':
                    next = 'context';
                    break;
                  default:
                    return;
                }
                event.preventDefault();
                setTab(next);
                dialogRef.current
                  ?.querySelector<HTMLButtonElement>(`#about-kodelet-tab-${next}`)
                  ?.focus();
              }}
              role="tab"
              tabIndex={tab === view ? 0 : -1}
              type="button"
            >
              {view === 'about' ? 'About' : 'Context'}
            </button>
          ))}
        </div>
        <div
          aria-labelledby={`about-kodelet-tab-${tab}`}
          className="about-kodelet-content"
          id="about-kodelet-panel"
          key={tab}
          role="tabpanel"
          // biome-ignore lint/a11y/noNoninteractiveTabindex: Tab panels need keyboard focus for scrolling and reading their content.
          tabIndex={0}
        >
          {tab === 'context' ? (
            <ContextUsage usage={usage} />
          ) : (
            <>
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
            </>
          )}
        </div>
      </div>
    </div>
  );
};

const formatContextTokens = (tokens: number): string => {
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(1)}M`;
  if (tokens >= 1_000) return `${(tokens / 1_000).toFixed(1)}K`;
  return String(tokens);
};

const ContextUsage: React.FC<{ usage?: Usage }> = ({ usage }) => {
  const current = usage?.currentContextWindow;
  const capacity = usage?.maxContextWindow;
  const utilization =
    current !== undefined && capacity && capacity > 0 ? (current / capacity) * 100 : undefined;
  const breakdown = usage?.contextBreakdown;
  const formatPercent = (value: number) => `${value.toFixed(1)}%`;
  const share = (tokens: number) =>
    breakdown && breakdown.totalTokens > 0 ? tokens / breakdown.totalTokens : 0;
  const usedFraction =
    utilization !== undefined
      ? Math.min(1, Math.max(0, utilization / 100))
      : breakdown && breakdown.totalTokens > 0
        ? 1
        : 0;
  const toolTones = ['tool', 'system', 'definitions', 'input', 'output', 'other'];
  const toolRows =
    breakdown?.tools.map((tool, index) => ({
      ...tool,
      tokens: tool.inputTokens + tool.outputTokens,
      tone: toolTones[index % toolTones.length],
    })) ?? [];
  const toolTokens = toolRows.reduce((sum, tool) => sum + tool.tokens, 0);
  const toolShare = (tokens: number) => (toolTokens > 0 ? tokens / toolTokens : 0);
  const rows = breakdown
    ? [
        { label: 'System prompt', tokens: breakdown.systemPromptTokens, tone: 'system' },
        { label: 'Tool definitions', tokens: breakdown.toolDefinitionTokens, tone: 'definitions' },
        { label: 'Input text', tokens: breakdown.inputTextTokens, tone: 'input' },
        { label: 'Output text', tokens: breakdown.outputTextTokens, tone: 'output' },
        { label: 'Other / reasoning', tokens: breakdown.otherTokens, tone: 'other' },
        {
          label: 'Tool usage',
          tokens: toolTokens,
          tone: 'tool',
        },
      ]
    : [];
  const barCaption = breakdown
    ? utilization !== undefined
      ? 'Used split estimated · remainder available'
      : 'Estimated composition · capacity unavailable'
    : undefined;
  const contextSegments = (
    <>
      {breakdown && breakdown.totalTokens > 0 ? (
        rows.map(({ label, tokens, tone }) => (
          <span
            aria-hidden="true"
            className={`about-context-segment about-context-${tone}`}
            key={tone}
            style={{ width: `${usedFraction * share(tokens) * 100}%` }}
            title={`${label}: ${formatContextTokens(tokens)} tokens · ${formatPercent(share(tokens) * 100)} of used`}
          />
        ))
      ) : (
        <span
          aria-hidden="true"
          className="about-context-segment about-context-unclassified"
          style={{ width: `${usedFraction * 100}%` }}
          title="Used · breakdown unavailable"
        />
      )}
      {utilization !== undefined ? (
        <span
          aria-hidden="true"
          className="about-context-segment about-context-available"
          style={{ width: `${(1 - usedFraction) * 100}%` }}
          title="Available"
        />
      ) : null}
    </>
  );

  return (
    <>
      <section aria-labelledby="about-context-window">
        <h3 id="about-context-window">Context window</h3>
        <p className="about-context-total">
          {current !== undefined && utilization !== undefined && capacity
            ? `${formatContextTokens(current)} / ${formatContextTokens(capacity)} tokens (${formatPercent(utilization)})`
            : current !== undefined && current > 0
              ? `${formatContextTokens(current)} tokens · Capacity unavailable.`
              : 'Context usage unavailable until a model turn.'}
        </p>
        {utilization !== undefined && current !== undefined && capacity ? (
          // biome-ignore lint/a11y/useSemanticElements: Native meter rendering cannot show independently coloured segments.
          <div
            aria-describedby={barCaption ? 'about-context-caption' : undefined}
            aria-label="Context window usage"
            aria-valuemax={capacity}
            aria-valuemin={0}
            aria-valuenow={Math.min(capacity, Math.max(0, current))}
            aria-valuetext={`${current.toLocaleString()} of ${capacity.toLocaleString()} tokens, ${formatPercent(utilization)} used`}
            className="about-context-meter"
            role="meter"
          >
            {contextSegments}
          </div>
        ) : breakdown ? (
          <div
            aria-describedby="about-context-caption"
            aria-label="Estimated composition"
            className="about-context-meter"
            role="img"
          >
            {contextSegments}
          </div>
        ) : null}
        {barCaption ? (
          <p className="about-kodelet-description" id="about-context-caption">
            {barCaption}
          </p>
        ) : null}
      </section>
      <section aria-labelledby="about-context-composition">
        <h3 id="about-context-composition">Composition</h3>
        {breakdown ? (
          <>
            <p className="about-context-estimate">
              ~{formatContextTokens(breakdown.totalTokens)} tokens
            </p>
            <p className="about-kodelet-description">Code mode: {breakdown.codeMode || 'off'}</p>
            <dl className="about-context-breakdown">
              {rows.map(({ label, tokens, tone }) => (
                <div className={`about-context-row about-context-${tone}`} key={tone}>
                  <dt>
                    <span aria-hidden="true" className="about-context-swatch" />
                    {label}
                  </dt>
                  <dd className="about-context-values">
                    <span>{formatContextTokens(tokens)} tokens</span>
                    <span className="about-context-share">
                      {formatPercent(share(tokens) * 100)}
                    </span>
                  </dd>
                </div>
              ))}
            </dl>
          </>
        ) : (
          <p className="about-kodelet-description">Breakdown unavailable until a model turn.</p>
        )}
      </section>
      {breakdown ? (
        <section aria-labelledby="about-context-tools">
          <h3 id="about-context-tools">Tool usage</h3>
          {toolRows.length === 0 ? (
            <p className="about-kodelet-description">No tool usage in this context.</p>
          ) : (
            <>
              <p className="about-context-estimate">~{formatContextTokens(toolTokens)} tokens</p>
              {toolTokens > 0 ? (
                <div aria-label="Tool usage breakdown" className="about-context-meter" role="img">
                  {toolRows.map((tool) => (
                    <span
                      aria-hidden="true"
                      className={`about-context-segment about-context-${tool.tone}`}
                      key={tool.name}
                      style={{ width: `${toolShare(tool.tokens) * 100}%` }}
                      title={`${tool.name}: ${formatContextTokens(tool.tokens)} tokens · ${formatPercent(toolShare(tool.tokens) * 100)} of tools`}
                    />
                  ))}
                </div>
              ) : null}
              <dl className="about-context-breakdown">
                {toolRows.map((tool) => (
                  <div className={`about-context-row about-context-${tool.tone}`} key={tool.name}>
                    <dt>
                      <span aria-hidden="true" className="about-context-swatch" />
                      {tool.name}
                    </dt>
                    <dd className="about-context-values">
                      <span>{formatContextTokens(tool.tokens)} tokens</span>
                      <span className="about-context-share">
                        {formatPercent(toolShare(tool.tokens) * 100)}
                      </span>
                    </dd>
                    <dd className="about-context-detail">
                      {tool.calls} {tool.calls === 1 ? 'call' : 'calls'} ·{' '}
                      {formatContextTokens(tool.inputTokens)} input ·{' '}
                      {formatContextTokens(tool.outputTokens)} output tokens
                    </dd>
                  </div>
                ))}
              </dl>
            </>
          )}
        </section>
      ) : null}
    </>
  );
};

export default AboutKodeletDialog;
