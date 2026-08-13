import { useEffect, useState } from 'react';
import apiService from '../services/api';
import type { ApprovalStatus, AuthPrincipal, UserLoginAuthorization } from '../types';
import {
  ApprovalCodeForm,
  AuthDetailList,
  AuthNotice,
  AuthPageShell,
  formatAuthTimestamp,
} from '../components/auth/AuthPageShell';

const statusCopy = (status: ApprovalStatus): string => {
  switch (status) {
    case 'approved':
      return 'This sign-in request has already been approved.';
    case 'denied':
      return 'This sign-in request has been denied.';
    case 'expired':
      return 'This sign-in request has expired.';
    default:
      return '';
  }
};

const terminalDecisionError = (error: unknown): boolean => {
  if (!error || typeof error !== 'object' || !('status' in error)) {
    return false;
  }
  return [401, 403, 404, 409].includes(Number(error.status));
};

const accessError = (error: unknown): boolean => {
  if (!error || typeof error !== 'object' || !('status' in error)) {
    return false;
  }
  return [401, 403].includes(Number(error.status));
};

export default function UserLoginPage() {
  const [principal, setPrincipal] = useState<AuthPrincipal | null>(null);
  const [principalLoading, setPrincipalLoading] = useState(true);
  const [userCode, setUserCode] = useState('');
  const [authorization, setAuthorization] = useState<UserLoginAuthorization | null>(null);
  const [completionStatus, setCompletionStatus] = useState<ApprovalStatus | null>(null);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [activeAction, setActiveAction] = useState<'lookup' | 'approve' | 'deny' | null>(null);
  const busy = activeAction !== null;

  useEffect(() => {
    const previousTitle = document.title;
    document.title = 'Approve Kodelet sign-in';
    let active = true;
    apiService
      .getUserLoginPrincipal()
      .then((value) => {
        if (active) {
          setPrincipal(value);
        }
      })
      .catch((loadError: Error) => {
        if (active) {
          setError(loadError.message || 'Unable to verify the current session.');
        }
      })
      .finally(() => {
        if (active) {
          setPrincipalLoading(false);
        }
      });
    return () => {
      active = false;
      document.title = previousTitle;
    };
  }, []);

  const lookup = async () => {
    setActiveAction('lookup');
    setError('');
    setMessage('');
    try {
      const response = await apiService.submitUserLoginDecision(userCode, 'lookup');
      if (!response.authorization) {
        throw new Error('The sign-in request details were unavailable.');
      }
      setAuthorization(response.authorization);
    } catch (lookupError) {
      if (accessError(lookupError)) {
        setPrincipal(null);
      }
      setError(lookupError instanceof Error ? lookupError.message : 'Unable to find that sign-in request.');
    } finally {
      setActiveAction(null);
    }
  };

  const decide = async (decision: 'approve' | 'deny') => {
    if (!authorization) {
      return;
    }
    setActiveAction(decision);
    setError('');
    try {
      const response = await apiService.submitUserLoginDecision(
        authorization.userCode,
        decision,
      );
      setAuthorization(null);
      setCompletionStatus(response.status);
      setMessage(response.message || statusCopy(response.status));
    } catch (decisionError) {
      if (terminalDecisionError(decisionError)) {
        setAuthorization(null);
        setUserCode('');
      }
      if (accessError(decisionError)) {
        setPrincipal(null);
      }
      setError(
        decisionError instanceof Error
          ? decisionError.message
          : 'Unable to process the sign-in request.',
      );
    } finally {
      setActiveAction(null);
    }
  };

  const reset = () => {
    setAuthorization(null);
    setCompletionStatus(null);
    setMessage('');
    setError('');
    setUserCode('');
  };

  const requestStatusCopy = authorization ? statusCopy(authorization.status) : '';

  return (
    <AuthPageShell
      description="Authorize a Kodelet client to use your current identity and role set on this server."
      eyebrow="Secure client authorization"
      principal={principal}
      principalLoading={principalLoading}
      title="Approve Kodelet sign-in"
    >
      {error ? <AuthNotice tone="error">{error}</AuthNotice> : null}
      {message ? (
        <AuthNotice tone={completionStatus === 'approved' ? 'success' : 'info'}>
          {message}
        </AuthNotice>
      ) : null}

      {principal && !authorization && !completionStatus ? (
        <>
          <AuthNotice tone="warning">
            Only continue if you started this sign-in from your own Kodelet client. Never use a
            code someone else sent you.
          </AuthNotice>
          <ApprovalCodeForm
            busy={busy}
            helpText="Enter the eight-character code displayed in the Kodelet client."
            id="user-login-code"
            label="Sign-in code"
            onChange={setUserCode}
            onSubmit={lookup}
            value={userCode}
          />
        </>
      ) : null}

      {principal && authorization ? (
        <div className="auth-request-review">
          <AuthNotice tone="warning">
            Check that this code and client information match exactly. Approve only if you
            initiated the sign-in.
          </AuthNotice>
          <AuthDetailList
            items={[
              { label: 'Code', value: authorization.userCode, mono: true },
              { label: 'Client', value: authorization.clientName },
              {
                label: 'Platform',
                value: `${authorization.clientOS}/${authorization.clientArch}`,
                mono: true,
              },
              { label: 'Kodelet version', value: authorization.kodeletVersion, mono: true },
              { label: 'Expires', value: formatAuthTimestamp(authorization.expiresAt) },
            ]}
          />
          {requestStatusCopy ? <AuthNotice tone="info">{requestStatusCopy}</AuthNotice> : null}
          <div className="auth-decision-actions">
            <button className="auth-secondary-button" disabled={busy} onClick={reset} type="button">
              Use a different code
            </button>
            {authorization.status === 'pending' ? (
              <div className="auth-decision-primary-actions">
                <button
                  className="auth-danger-button"
                  disabled={busy}
                  onClick={() => decide('deny')}
                  type="button"
                >
                  {activeAction === 'deny' ? 'Denying…' : 'Deny'}
                </button>
                <button
                  className="auth-primary-button"
                  disabled={busy}
                  onClick={() => decide('approve')}
                  type="button"
                >
                  {activeAction === 'approve' ? 'Approving…' : 'Approve sign-in'}
                </button>
              </div>
            ) : null}
          </div>
        </div>
      ) : null}

      {completionStatus ? (
        <div className="auth-completion-actions">
          <a className="auth-secondary-button" href="/">
            Return to Kodelet
          </a>
        </div>
      ) : null}
    </AuthPageShell>
  );
}
