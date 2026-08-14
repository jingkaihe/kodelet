import React from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import { UserLoginPageView } from './UserLoginPage';

type UserLoginPageViewProps = React.ComponentProps<typeof UserLoginPageView>;

const principal = {
  id: 'https://issuer.example.com|subject-user',
  issuer: 'https://issuer.example.com',
  subject: 'subject-user',
  name: 'Test User',
  email: 'user@example.com',
  roles: ['user'],
};

const authorization = {
  status: 'pending' as const,
  userCode: 'ABCD-EFGH',
  clientName: 'kodelet',
  clientOS: 'linux',
  clientArch: 'amd64',
  kodeletVersion: 'v-test',
  expiresAt: '2030-01-02T03:14:05Z',
};

const InteractiveUserLoginPageView = (args: UserLoginPageViewProps) => {
  const [userCode, setUserCode] = React.useState(args.userCode);

  React.useEffect(() => {
    setUserCode(args.userCode);
  }, [args.userCode]);

  return (
    <UserLoginPageView
      {...args}
      userCode={userCode}
      onUserCodeChange={(value) => {
        setUserCode(value);
        args.onUserCodeChange(value);
      }}
    />
  );
};

const meta = {
  title: 'Authentication/UserLoginPage',
  component: UserLoginPageView,
  render: (args) => <InteractiveUserLoginPageView {...args} />,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    activeAction: null,
    authorization: null,
    completionStatus: null,
    error: '',
    message: '',
    principal,
    principalLoading: false,
    userCode: '',
    onDecision: fn(),
    onLookup: fn(),
    onReset: fn(),
    onUserCodeChange: fn(),
  },
} satisfies Meta<typeof UserLoginPageView>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Entry: Story = {};

export const LookupError: Story = {
  args: {
    error: 'Code not found.',
    userCode: 'ABCD-EFGH',
  },
};

export const Review: Story = {
  args: {
    authorization,
    userCode: authorization.userCode,
  },
};

export const Approved: Story = {
  args: {
    completionStatus: 'approved',
    message: 'Sign-in approved. You can return to the Kodelet client.',
  },
};
