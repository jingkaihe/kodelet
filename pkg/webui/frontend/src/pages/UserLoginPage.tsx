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

  const lookup = async (code: string) => {
    setUserCode(code);
    setActiveAction('lookup');
    setError('');
    setMessage('');
    try {
      const response = await apiService.submitUserLoginDecision(code, 'lookup');
      if (!response.authorization) {
        throw new Error('The sign-in request details were unavailable.');
      }
      setAuthorization(response.authorization);
    } catch (lookupError) {
      if (accessError(lookupError)) {
        setPrincipal(null);
      }
      setError(
        lookupError && typeof lookupError === 'object' && 'status' in lookupError && Number(lookupError.status) === 404
          ? 'Code not found.'
          : lookupError instanceof Error
            ? lookupError.message
            : 'Unable to find that sign-in request.',
      );
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
  const pageTitle = authorization
    ? 'Approve sign-in'
    : completionStatus === 'approved'
      ? 'Sign-in approved'
      : completionStatus === 'denied'
        ? 'Sign-in denied'
        : 'Client sign-in';
  const pageDescription = authorization
    ? 'Confirm these details match your client.'
    : completionStatus
      ? undefined
      : 'Enter the code shown in your Kodelet client.';

  return (
    <AuthPageShell
      description={pageDescription}
      principal={principal}
      principalLoading={principalLoading}
      title={pageTitle}
    >
      {error ? <AuthNotice tone="error">{error}</AuthNotice> : null}
      {message ? (
        <AuthNotice tone={completionStatus === 'approved' ? 'success' : 'info'}>
          {message}
        </AuthNotice>
      ) : null}

      {principal && !authorization && !completionStatus ? (
        <>
          {!error ? (
            <AuthNotice tone="warning">
              Only use a code from your own Kodelet client.
            </AuthNotice>
          ) : null}
          <ApprovalCodeForm
            busy={busy}
            id="user-login-code"
            label="Sign-in code"
            onChange={(value) => {
              setUserCode(value);
              setError('');
            }}
            onSubmit={lookup}
            value={userCode}
          />
        </>
      ) : null}

      {principal && authorization ? (
        <div className="auth-request-review">
          <AuthNotice tone="warning">
            Only approve a sign-in you started.
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
