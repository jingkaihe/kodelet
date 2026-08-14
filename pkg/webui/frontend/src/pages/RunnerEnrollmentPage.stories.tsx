import React from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import { RunnerEnrollmentPageView } from './RunnerEnrollmentPage';

type RunnerEnrollmentPageViewProps = React.ComponentProps<typeof RunnerEnrollmentPageView>;

const principal = {
  id: 'https://issuer.example.com|subject-admin',
  issuer: 'https://issuer.example.com',
  subject: 'subject-admin',
  name: 'Runner Admin',
  email: 'admin@example.com',
  roles: ['user', 'runner-admin'],
};

const enrollment = {
  status: 'pending' as const,
  userCode: 'WXYZ-2345',
  displayName: 'Build runner',
  host: {
    hostname: 'runner.example.test',
    os: 'linux',
    arch: 'arm64',
  },
  workspace: {
    path: '/home/kodelet/project',
  },
  kodeletVersion: 'v-test',
  fingerprint: 'SHA256:6dYP9PZL2UQ0tbCDppR9xq6jXwsk4gF4cJ4JX3aYeGc',
  expiresAt: '2030-01-02T03:14:05Z',
  replaceNeeded: true,
};

const InteractiveRunnerEnrollmentPageView = (args: RunnerEnrollmentPageViewProps) => {
  const [userCode, setUserCode] = React.useState(args.userCode);
  const [replaceConfirmed, setReplaceConfirmed] = React.useState(args.replaceConfirmed);

  React.useEffect(() => {
    setUserCode(args.userCode);
  }, [args.userCode]);

  React.useEffect(() => {
    setReplaceConfirmed(args.replaceConfirmed);
  }, [args.replaceConfirmed]);

  return (
    <RunnerEnrollmentPageView
      {...args}
      replaceConfirmed={replaceConfirmed}
      userCode={userCode}
      onReplaceConfirmedChange={(confirmed) => {
        setReplaceConfirmed(confirmed);
        args.onReplaceConfirmedChange(confirmed);
      }}
      onUserCodeChange={(value) => {
        setUserCode(value);
        args.onUserCodeChange(value);
      }}
    />
  );
};

const meta = {
  title: 'Authentication/RunnerEnrollmentPage',
  component: RunnerEnrollmentPageView,
  render: (args) => <InteractiveRunnerEnrollmentPageView {...args} />,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    activeAction: null,
    completionStatus: null,
    enrollment: null,
    error: '',
    message: '',
    principal,
    principalLoading: false,
    replaceConfirmed: false,
    userCode: '',
    onDecision: fn(),
    onLookup: fn(),
    onReplaceConfirmedChange: fn(),
    onReset: fn(),
    onUserCodeChange: fn(),
  },
} satisfies Meta<typeof RunnerEnrollmentPageView>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Entry: Story = {};

export const ReplacementReview: Story = {
  args: {
    enrollment,
    replaceConfirmed: true,
    userCode: enrollment.userCode,
  },
};

export const Approved: Story = {
  args: {
    completionStatus: 'approved',
    message: 'Runner enrollment approved. You can return to the runner terminal.',
  },
};
