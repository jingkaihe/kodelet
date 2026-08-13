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
    description: 'Enter the code shown in your Kodelet client.',
    principal,
    title: 'Client sign-in',
    children: (
      <>
        <AuthNotice tone="warning">
          Only use a code from your own Kodelet client.
        </AuthNotice>
        <ApprovalCodeForm
          busy={false}
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

export const RunnerEntry: Story = {
  args: {
    description: 'Enter the code shown in the runner terminal.',
    principal,
    title: 'Runner enrollment',
    children: (
      <>
        <AuthNotice tone="warning">
          Only use a code from a runner you control.
        </AuthNotice>
        <ApprovalCodeForm
          busy={false}
          id="storybook-runner-code"
          label="Enrollment code"
          onChange={() => {}}
          onSubmit={() => {}}
          value=""
        />
      </>
    ),
  },
};

export const SignInLookupError: Story = {
  args: {
    description: 'Enter the code shown in your Kodelet client.',
    principal,
    title: 'Client sign-in',
    children: (
      <>
        <AuthNotice tone="error">Code not found.</AuthNotice>
        <ApprovalCodeForm
          busy={false}
          id="storybook-invalid-sign-in-code"
          label="Sign-in code"
          onChange={() => {}}
          onSubmit={() => {}}
          value="ABCD-EFGH"
        />
      </>
    ),
  },
};

export const SignInReview: Story = {
  args: {
    description: 'Confirm these details match your client.',
    principal,
    title: 'Approve sign-in',
    children: (
      <div className="auth-request-review">
        <AuthNotice tone="warning">
          Only approve a sign-in you started.
        </AuthNotice>
        <AuthDetailList
          items={[
            { label: 'Code', value: 'ABCD-EFGH', mono: true },
            { label: 'Client', value: 'kodelet' },
            { label: 'Platform', value: 'linux/amd64', mono: true },
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
    description: 'Confirm the runner, host, and workspace.',
    principal,
    title: 'Approve runner',
    children: (
      <div className="auth-request-review">
        <AuthNotice tone="warning">
          Only approve a runner you control.
        </AuthNotice>
        <AuthDetailList
          items={[
            { label: 'Code', value: 'WXYZ-2345', mono: true },
            { label: 'Runner', value: 'Build runner' },
            { label: 'Host', value: 'runner.example.test (linux/arm64)' },
            { label: 'Workspace', value: '/home/kodelet/project', mono: true },
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
            <strong>Replace the existing credential.</strong> The current credential will be
            revoked.
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
