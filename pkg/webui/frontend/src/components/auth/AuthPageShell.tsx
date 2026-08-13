import { useRef, useState, type ClipboardEvent, type FormEvent, type ReactNode } from 'react';
import { AlertTriangle, CheckCircle2, Info } from 'lucide-react';
import type { AuthPrincipal } from '../../types';

type AuthNoticeTone = 'info' | 'warning' | 'error' | 'success';

interface AuthPageShellProps {
  title: string;
  description?: string;
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
  value: string;
  busy: boolean;
  onChange: (value: string) => void;
  onSubmit: (value: string) => void;
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
            <h1 className="auth-title" id="auth-page-title">
              {title}
            </h1>
            {description ? <p className="auth-description">{description}</p> : null}
          </header>

          <div className="auth-card-content">{children}</div>
        </section>
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
  value,
  busy,
  onChange,
  onSubmit,
}: ApprovalCodeFormProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [focused, setFocused] = useState(false);
  const compactValue = value.replace('-', '');
  const complete = compactValue.length === 8;

  const commitCode = (rawValue: string) => {
    const formatted = formatApprovalCode(rawValue);
    onChange(formatted);
    if (!busy && formatted !== value && formatted.replace('-', '').length === 8) {
      onSubmit(formatted);
    }
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (complete && !busy) {
      onSubmit(value);
    }
  };

  const handlePaste = (event: ClipboardEvent<HTMLInputElement>) => {
    event.preventDefault();
    commitCode(event.clipboardData.getData('text'));
  };

  const moveCaretToEnd = () => {
    const input = inputRef.current;
    if (!input) {
      return;
    }
    input.setSelectionRange(input.value.length, input.value.length);
  };

  return (
    <form className="auth-code-form" onSubmit={handleSubmit}>
      <div className="auth-code-control" aria-busy={busy}>
        <input
          aria-label={label}
          autoCapitalize="characters"
          autoComplete="one-time-code"
          autoFocus
          className="auth-code-input"
          data-1p-ignore="true"
          disabled={busy}
          id={id}
          inputMode="text"
          maxLength={32}
          onBlur={() => setFocused(false)}
          onChange={(event) => commitCode(event.target.value)}
          onClick={moveCaretToEnd}
          onFocus={() => {
            setFocused(true);
            moveCaretToEnd();
          }}
          onPaste={handlePaste}
          ref={inputRef}
          required
          spellCheck={false}
          type="text"
          value={value}
        />
        <div className="auth-code-cells" aria-hidden="true">
          {Array.from({ length: 8 }, (_, index) => {
            const character = compactValue[index] || '';
            const active = focused && !busy && !complete && index === compactValue.length;
            return (
              <span
                className={`auth-code-cell${character ? ' is-filled' : ''}${active ? ' is-active' : ''}`}
                key={index}
              >
                {character}
              </span>
            );
          })}
        </div>
      </div>
      {busy ? (
        <p className="auth-code-status" role="status">
          Checking…
        </p>
      ) : null}
      <button className="sr-only" disabled={!complete || busy} type="submit">
        Check code
      </button>
    </form>
  );
}
