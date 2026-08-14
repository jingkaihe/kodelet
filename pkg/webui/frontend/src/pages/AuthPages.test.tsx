import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import UserLoginPage from './UserLoginPage';
import RunnerEnrollmentPage from './RunnerEnrollmentPage';
import SignedOutPage from './SignedOutPage';
import { formatApprovalCode } from '../components/auth/AuthPageShell';

const apiMocks = vi.hoisted(() => ({
  getUserLoginPrincipal: vi.fn(),
  getRunnerEnrollmentPrincipal: vi.fn(),
  submitUserLoginDecision: vi.fn(),
  submitRunnerEnrollmentDecision: vi.fn(),
}));

vi.mock('../services/api', () => ({
  default: apiMocks,
  apiService: apiMocks,
}));

const principal = {
  id: 'https://issuer.example.com|subject-user',
  email: 'user@example.com',
  roles: ['user', 'runner-admin'],
};

describe('authentication approval pages', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    apiMocks.getUserLoginPrincipal.mockResolvedValue(principal);
    apiMocks.getRunnerEnrollmentPrincipal.mockResolvedValue(principal);
  });

  it('normalizes manually entered approval codes', () => {
    expect(formatApprovalCode('ab cd-efgh')).toBe('ABCD-EFGH');
    expect(formatApprovalCode('IO01-ABCD')).toBe('ABCD');
  });

  it('keeps signed-out users on a public confirmation page until they continue', () => {
    render(<SignedOutPage />);

    expect(screen.getByRole('heading', { name: 'Signed out' })).toBeInTheDocument();
    expect(screen.getByText('Your Kodelet session has ended.')).toBeInTheDocument();
    expect(screen.getByText('Your identity provider may still be signed in.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', '/');
    expect(screen.getByRole('link', { name: 'Sign in' }).querySelector('svg')).toHaveClass(
      'lucide-log-in',
    );
  });

  it('reviews and approves a Kodelet client sign-in', async () => {
    const user = userEvent.setup();
    apiMocks.submitUserLoginDecision
      .mockResolvedValueOnce({
        status: 'pending',
        authorization: {
          status: 'pending',
          userCode: 'ABCD-EFGH',
          clientName: 'kodelet',
          clientOS: 'linux',
          clientArch: 'amd64',
          kodeletVersion: 'v-test',
          expiresAt: '2030-01-02T03:14:05Z',
        },
      })
      .mockResolvedValueOnce({
        status: 'approved',
        message: 'Sign-in approved. You can return to the Kodelet client.',
      });

    render(<UserLoginPage />);

    expect(await screen.findByText('Signed in as user@example.com')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Client sign-in' })).toBeInTheDocument();
    expect(
      screen.getByText('Only use a code from your own Kodelet client.'),
    ).toBeInTheDocument();

    const signInCode = screen.getByLabelText('Sign-in code');
    await user.click(signInCode);
    await user.paste('abcd-efgh');
    expect(signInCode).toHaveValue('ABCD-EFGH');
    expect(screen.queryByRole('button', { name: 'Continue' })).not.toBeInTheDocument();

    expect(await screen.findByText('linux/amd64')).toBeInTheDocument();
    expect(apiMocks.submitUserLoginDecision).toHaveBeenNthCalledWith(
      1,
      'ABCD-EFGH',
      'lookup',
    );

    await user.click(screen.getByRole('button', { name: 'Approve sign-in' }));

    expect(
      await screen.findByText('Sign-in approved. You can return to the Kodelet client.'),
    ).toBeInTheDocument();
    expect(apiMocks.submitUserLoginDecision).toHaveBeenNthCalledWith(
      2,
      'ABCD-EFGH',
      'approve',
    );
  });

  it('hides sign-in controls when the approval context is unavailable', async () => {
    apiMocks.getUserLoginPrincipal.mockRejectedValueOnce(
      Object.assign(new Error('OIDC authentication required'), { status: 401 }),
    );

    render(<UserLoginPage />);

    expect(await screen.findByRole('alert')).toHaveTextContent('OIDC authentication required');
    expect(screen.queryByLabelText('Sign-in code')).not.toBeInTheDocument();
  });

  it('shows the active denial action without implying approval', async () => {
    const user = userEvent.setup();
    let completeDenial: ((value: { status: string; message: string }) => void) | undefined;
    apiMocks.submitUserLoginDecision
      .mockResolvedValueOnce({
        status: 'pending',
        authorization: {
          status: 'pending',
          userCode: 'ABCD-EFGH',
          clientName: 'kodelet',
          clientOS: 'linux',
          clientArch: 'amd64',
          kodeletVersion: 'v-test',
          expiresAt: '2030-01-02T03:14:05Z',
        },
      })
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            completeDenial = resolve;
          }),
      );

    render(<UserLoginPage />);
    await user.type(await screen.findByLabelText('Sign-in code'), 'abcdefgh');
    await user.click(await screen.findByRole('button', { name: 'Deny' }));

    expect(screen.getByRole('button', { name: 'Denying…' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Approve sign-in' })).toBeDisabled();

    completeDenial?.({ status: 'denied', message: 'Sign-in denied.' });
    expect(await screen.findByText('Sign-in denied.')).toBeInTheDocument();
  });

  it('requires explicit confirmation before replacing a runner credential', async () => {
    const user = userEvent.setup();
    apiMocks.submitRunnerEnrollmentDecision
      .mockResolvedValueOnce({
        status: 'pending',
        enrollment: {
          status: 'pending',
          userCode: 'WXYZ-2345',
          displayName: 'Build runner',
          host: {
            instanceId: 'host-one',
            hostname: 'runner.example.test',
            os: 'linux',
            arch: 'arm64',
          },
          workspace: {
            path: '/work/project',
            name: 'project',
          },
          kodeletVersion: 'v-runner',
          fingerprint: 'SHA256:runner-fingerprint',
          expiresAt: '2030-01-02T03:14:05Z',
          replaceNeeded: true,
        },
      })
      .mockResolvedValueOnce({
        status: 'approved',
        message: 'Runner enrollment approved. You can return to the runner terminal.',
      });

    render(<RunnerEnrollmentPage />);

    await screen.findByText('Signed in as user@example.com');
    expect(screen.getByRole('heading', { name: 'Runner enrollment' })).toBeInTheDocument();
    await user.type(screen.getByLabelText('Enrollment code'), 'wxyz2345');

    expect(await screen.findByText('SHA256:runner-fingerprint')).toBeInTheDocument();
    expect(screen.getByText('/work/project')).toBeInTheDocument();
    const approveButton = screen.getByRole('button', { name: 'Approve runner' });
    expect(approveButton).toBeDisabled();

    await user.click(screen.getByRole('checkbox'));
    expect(approveButton).toBeEnabled();
    await user.click(approveButton);

    await waitFor(() => {
      expect(apiMocks.submitRunnerEnrollmentDecision).toHaveBeenNthCalledWith(
        2,
        'WXYZ-2345',
        'approve',
        true,
      );
    });
    expect(
      await screen.findByText(
        'Runner enrollment approved. You can return to the runner terminal.',
      ),
    ).toBeInTheDocument();
  });

  it('hides runner controls when the enrollment context is forbidden', async () => {
    apiMocks.getRunnerEnrollmentPrincipal.mockRejectedValueOnce(
      Object.assign(new Error('insufficient permissions'), { status: 403 }),
    );

    render(<RunnerEnrollmentPage />);

    expect(await screen.findByRole('alert')).toHaveTextContent('insufficient permissions');
    expect(screen.queryByLabelText('Enrollment code')).not.toBeInTheDocument();
  });

  it('shows only the lookup error after an invalid code', async () => {
    const user = userEvent.setup();
    apiMocks.submitUserLoginDecision.mockRejectedValueOnce(
      Object.assign(new Error('User login request not found.'), { status: 404 }),
    );

    render(<UserLoginPage />);
    await user.type(await screen.findByLabelText('Sign-in code'), 'abcdefgh');

    expect(await screen.findByRole('alert')).toHaveTextContent('Code not found.');
    expect(
      screen.queryByText('Only use a code from your own Kodelet client.'),
    ).not.toBeInTheDocument();
  });

  it('clears stale sign-in approval controls after a conflict', async () => {
    const user = userEvent.setup();
    apiMocks.submitUserLoginDecision
      .mockResolvedValueOnce({
        status: 'pending',
        authorization: {
          status: 'pending',
          userCode: 'ABCD-EFGH',
          clientName: 'kodelet',
          clientOS: 'linux',
          clientArch: 'amd64',
          kodeletVersion: 'v-test',
          expiresAt: '2030-01-02T03:14:05Z',
        },
      })
      .mockRejectedValueOnce(
        Object.assign(new Error('User login request is no longer pending.'), { status: 409 }),
      );

    render(<UserLoginPage />);
    await user.type(await screen.findByLabelText('Sign-in code'), 'abcdefgh');
    await user.click(await screen.findByRole('button', { name: 'Approve sign-in' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('no longer pending');
    expect(screen.queryByRole('button', { name: 'Approve sign-in' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Sign-in code')).toHaveValue('');
  });

  it('clears stale runner approval controls after a conflict', async () => {
    const user = userEvent.setup();
    apiMocks.submitRunnerEnrollmentDecision
      .mockResolvedValueOnce({
        status: 'pending',
        enrollment: {
          status: 'pending',
          userCode: 'WXYZ-2345',
          host: { hostname: 'runner.example.test', os: 'linux', arch: 'arm64' },
          workspace: { path: '/work/project' },
          fingerprint: 'SHA256:runner-fingerprint',
          expiresAt: '2030-01-02T03:14:05Z',
          replaceNeeded: false,
        },
      })
      .mockRejectedValueOnce(
        Object.assign(new Error('Runner enrollment request has expired.'), { status: 409 }),
      );

    render(<RunnerEnrollmentPage />);
    await user.type(await screen.findByLabelText('Enrollment code'), 'wxyz2345');
    await user.click(await screen.findByRole('button', { name: 'Approve runner' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('has expired');
    expect(screen.queryByRole('button', { name: 'Approve runner' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('Enrollment code')).toHaveValue('');
  });
});
