import type { Meta, StoryObj } from '@storybook/react-vite';
import {
  ApprovalCodeForm,
  AuthDetailList,
  AuthNotice,
  AuthPageShell,
} from './AuthPageShell';

const principal = {
  id: 'https://issuer.example.com|subject-user',
  email: 'user@example.com',
  roles: ['user', 'runner-admin'],
};

const meta = {
  title: 'Authentication/ApprovalPages',
  component: AuthPageShell,
  parameters: {
    layout: 'fullscreen',
  },
} satisfies Meta<typeof AuthPageShell>;

export default meta;

type Story = StoryObj<typeof meta>;

export const SignInEntry: Story = {
  args: {
    description: 'Authorize a Kodelet client to use your current identity and role set on this server.',
    eyebrow: 'Secure client authorization',
    principal,
    title: 'Approve Kodelet sign-in',
    children: (
      <>
        <AuthNotice tone="warning">
          Only continue if you started this sign-in from your own Kodelet client. Never use a code
          someone else sent you.
        </AuthNotice>
        <ApprovalCodeForm
          busy={false}
          helpText="Enter the eight-character code displayed in the Kodelet client."
          id="storybook-sign-in-code"
          label="Sign-in code"
          onChange={() => {}}
          onSubmit={() => {}}
          value=""
        />
      </>
    ),
  },
};

export const SignInReview: Story = {
  args: {
    description: 'Authorize a Kodelet client to use your current identity and role set on this server.',
    eyebrow: 'Secure client authorization',
    principal,
    title: 'Approve Kodelet sign-in',
    children: (
      <div className="auth-request-review">
        <AuthNotice tone="warning">
          Check that this code and client information match exactly. Approve only if you initiated
          the sign-in.
        </AuthNotice>
        <AuthDetailList
          items={[
            { label: 'Code', value: 'ABCD-EFGH', mono: true },
            { label: 'Client', value: 'kodelet' },
            { label: 'Platform', value: 'linux/amd64', mono: true },
            { label: 'Kodelet version', value: 'v1.8.0', mono: true },
            { label: 'Expires', value: 'Aug 13, 2026, 9:45 PM' },
          ]}
        />
        <div className="auth-decision-actions">
          <button className="auth-secondary-button" type="button">
            Use a different code
          </button>
          <div className="auth-decision-primary-actions">
            <button className="auth-danger-button" type="button">
              Deny
            </button>
            <button className="auth-primary-button" type="button">
              Approve sign-in
            </button>
          </div>
        </div>
      </div>
    ),
  },
};

export const RunnerReplacementReview: Story = {
  args: {
    description: 'Authorize a runner and bind its key credential to the displayed host and workspace.',
    eyebrow: 'Runner administration',
    principal,
    title: 'Approve runner enrollment',
    children: (
      <div className="auth-request-review">
        <AuthNotice tone="warning">
          Check that the code, host, workspace, and key fingerprint match the runner you intend to
          enroll.
        </AuthNotice>
        <AuthDetailList
          items={[
            { label: 'Code', value: 'WXYZ-2345', mono: true },
            { label: 'Runner', value: 'Build runner' },
            { label: 'Host', value: 'runner.example.test (linux/arm64)' },
            { label: 'Workspace', value: '/home/kodelet/project', mono: true },
            { label: 'Kodelet version', value: 'v1.8.0', mono: true },
            {
              label: 'Public-key fingerprint',
              value: 'SHA256:6dYP9PZL2UQ0tbCDppR9xq6jXwsk4gF4cJ4JX3aYeGc',
              mono: true,
            },
            { label: 'Expires', value: 'Aug 13, 2026, 9:45 PM' },
          ]}
        />
        <label className="auth-replacement-confirmation">
          <input defaultChecked type="checkbox" />
          <span>
            <strong>Replace the existing credential.</strong> The currently active runner
            credential will be revoked immediately.
          </span>
        </label>
        <div className="auth-decision-actions">
          <button className="auth-secondary-button" type="button">
            Use a different code
          </button>
          <div className="auth-decision-primary-actions">
            <button className="auth-danger-button" type="button">
              Deny
            </button>
            <button className="auth-primary-button" type="button">
              Approve runner
            </button>
          </div>
        </div>
      </div>
    ),
  },
};
