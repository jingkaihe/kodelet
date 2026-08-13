import type { FormEvent, ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, Info, ShieldCheck } from 'lucide-react';
import type { AuthPrincipal } from '../../types';

type AuthNoticeTone = 'info' | 'warning' | 'error' | 'success';

interface AuthPageShellProps {
  eyebrow: string;
  title: string;
  description: string;
  principal?: AuthPrincipal | null;
  principalLoading?: boolean;
  children: ReactNode;
}

interface AuthNoticeProps {
  tone: AuthNoticeTone;
  children: ReactNode;
}

interface AuthDetailItem {
  label: string;
  value: ReactNode;
  mono?: boolean;
}

interface AuthDetailListProps {
  items: AuthDetailItem[];
}

interface ApprovalCodeFormProps {
  id: string;
  label: string;
  helpText: string;
  value: string;
  busy: boolean;
  onChange: (value: string) => void;
  onSubmit: () => void;
}

const approvalCodeAlphabet = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789';

export const formatApprovalCode = (value: string): string => {
  const compact = Array.from(value.toUpperCase())
    .filter((character) => approvalCodeAlphabet.includes(character))
    .join('')
    .slice(0, 8);
  return compact.length > 4 ? `${compact.slice(0, 4)}-${compact.slice(4)}` : compact;
};

export const formatAuthTimestamp = (value: string): string => {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date);
};

const principalLabel = (principal?: AuthPrincipal | null): string => {
  return principal?.email || principal?.name || principal?.id || '';
};

export function AuthPageShell({
  eyebrow,
  title,
  description,
  principal,
  principalLoading = false,
  children,
}: AuthPageShellProps) {
  const identity = principalLabel(principal);

  return (
    <div className="auth-page">
      <main className="auth-page-shell">
        <div className="auth-app-bar">
          <div className="auth-brand" aria-label="Kodelet">
            Kodelet
          </div>
          {principalLoading ? (
            <span className="auth-identity-chip">Checking session…</span>
          ) : identity ? (
            <span className="auth-identity-chip" title={identity}>
              Signed in as {identity}
            </span>
          ) : null}
        </div>

        <section className="auth-card surface-panel" aria-labelledby="auth-page-title">
          <header className="auth-header">
            <div className="auth-context-label">
              <ShieldCheck aria-hidden="true" size={15} strokeWidth={1.8} />
              <p className="auth-eyebrow">{eyebrow}</p>
            </div>
            <h1 className="auth-title" id="auth-page-title">
              {title}
            </h1>
            <p className="auth-description">{description}</p>
          </header>

          <div className="auth-card-content">{children}</div>
        </section>

        <p className="auth-page-footnote">
          Approval changes access to this Kodelet server. Check every detail before continuing.
        </p>
      </main>
    </div>
  );
}

export function AuthNotice({ tone, children }: AuthNoticeProps) {
  const Icon =
    tone === 'success'
      ? CheckCircle2
      : tone === 'info'
        ? Info
        : AlertTriangle;

  return (
    <div
      className={`auth-notice auth-notice-${tone}`}
      role={tone === 'error' ? 'alert' : 'status'}
    >
      <Icon className="auth-notice-icon" size={18} strokeWidth={1.8} aria-hidden="true" />
      <div>{children}</div>
    </div>
  );
}

export function AuthDetailList({ items }: AuthDetailListProps) {
  return (
    <dl className="auth-detail-list">
      {items.map((item) => (
        <div className="auth-detail-row" key={item.label}>
          <dt>{item.label}</dt>
          <dd className={item.mono ? 'auth-detail-mono' : undefined}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function ApprovalCodeForm({
  id,
  label,
  helpText,
  value,
  busy,
  onChange,
  onSubmit,
}: ApprovalCodeFormProps) {
  const complete = value.replace('-', '').length === 8;

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (complete && !busy) {
      onSubmit();
    }
  };

  return (
    <form className="auth-code-form" onSubmit={handleSubmit}>
      <label className="auth-field" htmlFor={id}>
        <span className="auth-field-label">{label}</span>
        <input
          aria-describedby={`${id}-help`}
          autoCapitalize="characters"
          autoComplete="off"
          autoFocus
          className="auth-code-input"
          disabled={busy}
          id={id}
          inputMode="text"
          maxLength={9}
          onChange={(event) => onChange(formatApprovalCode(event.target.value))}
          placeholder="ABCD-EFGH"
          required
          spellCheck={false}
          type="text"
          value={value}
        />
      </label>
      <p className="auth-field-help" id={`${id}-help`}>
        {helpText}
      </p>
      <div className="auth-form-actions">
        <button className="auth-primary-button" disabled={!complete || busy} type="submit">
          {busy ? 'Checking…' : 'Continue'}
        </button>
      </div>
    </form>
  );
}
