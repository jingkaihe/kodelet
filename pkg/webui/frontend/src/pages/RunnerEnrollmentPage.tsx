import { useEffect, useState } from 'react';
import apiService from '../services/api';
import type {
  ApprovalStatus,
  AuthPrincipal,
  RunnerEnrollmentAuthorization,
} from '../types';
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
      return 'This runner enrollment has already been approved.';
    case 'denied':
      return 'This runner enrollment has been denied.';
    case 'expired':
      return 'This runner enrollment has expired.';
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

export default function RunnerEnrollmentPage() {
  const [principal, setPrincipal] = useState<AuthPrincipal | null>(null);
  const [principalLoading, setPrincipalLoading] = useState(true);
  const [userCode, setUserCode] = useState('');
  const [enrollment, setEnrollment] = useState<RunnerEnrollmentAuthorization | null>(null);
  const [replaceConfirmed, setReplaceConfirmed] = useState(false);
  const [completionStatus, setCompletionStatus] = useState<ApprovalStatus | null>(null);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const [activeAction, setActiveAction] = useState<'lookup' | 'approve' | 'deny' | null>(null);
  const busy = activeAction !== null;

  useEffect(() => {
    const previousTitle = document.title;
    document.title = 'Approve runner enrollment';
    let active = true;
    apiService
      .getRunnerEnrollmentPrincipal()
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
      const response = await apiService.submitRunnerEnrollmentDecision(userCode, 'lookup');
      if (!response.enrollment) {
        throw new Error('The runner enrollment details were unavailable.');
      }
      setEnrollment(response.enrollment);
      setReplaceConfirmed(false);
    } catch (lookupError) {
      if (accessError(lookupError)) {
        setPrincipal(null);
      }
      setError(
        lookupError instanceof Error
          ? lookupError.message
          : 'Unable to find that runner enrollment.',
      );
    } finally {
      setActiveAction(null);
    }
  };

  const decide = async (decision: 'approve' | 'deny') => {
    if (!enrollment) {
      return;
    }
    setActiveAction(decision);
    setError('');
    try {
      const response = await apiService.submitRunnerEnrollmentDecision(
        enrollment.userCode,
        decision,
        decision === 'approve' && replaceConfirmed,
      );
      setEnrollment(null);
      setCompletionStatus(response.status);
      setMessage(response.message || statusCopy(response.status));
    } catch (decisionError) {
      if (terminalDecisionError(decisionError)) {
        setEnrollment(null);
        setReplaceConfirmed(false);
        setUserCode('');
      }
      if (accessError(decisionError)) {
        setPrincipal(null);
      }
      setError(
        decisionError instanceof Error
          ? decisionError.message
          : 'Unable to process the runner enrollment.',
      );
    } finally {
      setActiveAction(null);
    }
  };

  const reset = () => {
    setEnrollment(null);
    setReplaceConfirmed(false);
    setCompletionStatus(null);
    setMessage('');
    setError('');
    setUserCode('');
  };

  const requestStatusCopy = enrollment ? statusCopy(enrollment.status) : '';
  const runnerName = enrollment?.displayName || enrollment?.host.hostname || 'Unnamed runner';

  return (
    <AuthPageShell
      description="Authorize a runner and bind its key credential to the displayed host and workspace."
      eyebrow="Runner enrollment"
      principal={principal}
      principalLoading={principalLoading}
      title="Approve runner enrollment"
    >
      {error ? <AuthNotice tone="error">{error}</AuthNotice> : null}
      {message ? (
        <AuthNotice tone={completionStatus === 'approved' ? 'success' : 'info'}>
          {message}
        </AuthNotice>
      ) : null}

      {principal && !enrollment && !completionStatus ? (
        <>
          <AuthNotice tone="warning">
            Only continue if you started this enrollment from a runner you control. Never use a
            code someone else sent you.
          </AuthNotice>
          <ApprovalCodeForm
            busy={busy}
            helpText="Enter the eight-character code displayed in the runner terminal."
            id="runner-enrollment-code"
            label="Enrollment code"
            onChange={setUserCode}
            onSubmit={lookup}
            value={userCode}
          />
        </>
      ) : null}

      {principal && enrollment ? (
        <div className="auth-request-review">
          <AuthNotice tone="warning">
            Check that the code, host, workspace, and key fingerprint match the runner you intend
            to enroll.
          </AuthNotice>
          <AuthDetailList
            items={[
              { label: 'Code', value: enrollment.userCode, mono: true },
              { label: 'Runner', value: runnerName },
              {
                label: 'Host',
                value: `${enrollment.host.hostname || 'Unknown host'} (${enrollment.host.os}/${enrollment.host.arch})`,
              },
              { label: 'Workspace', value: enrollment.workspace.path, mono: true },
              {
                label: 'Kodelet version',
                value: enrollment.kodeletVersion || 'Not reported',
                mono: true,
              },
              { label: 'Public-key fingerprint', value: enrollment.fingerprint, mono: true },
              { label: 'Expires', value: formatAuthTimestamp(enrollment.expiresAt) },
            ]}
          />
          {enrollment.replaceNeeded ? (
            <label className="auth-replacement-confirmation">
              <input
                checked={replaceConfirmed}
                disabled={busy}
                onChange={(event) => setReplaceConfirmed(event.target.checked)}
                type="checkbox"
              />
              <span>
                <strong>Replace the existing credential.</strong> The currently active runner
                credential will be revoked immediately.
              </span>
            </label>
          ) : null}
          {requestStatusCopy ? <AuthNotice tone="info">{requestStatusCopy}</AuthNotice> : null}
          <div className="auth-decision-actions">
            <button className="auth-secondary-button" disabled={busy} onClick={reset} type="button">
              Use a different code
            </button>
            {enrollment.status === 'pending' ? (
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
                  disabled={busy || (enrollment.replaceNeeded && !replaceConfirmed)}
                  onClick={() => decide('approve')}
                  type="button"
                >
                  {activeAction === 'approve' ? 'Approving…' : 'Approve runner'}
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
