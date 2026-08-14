import type { Meta, StoryObj } from '@storybook/react-vite';
import { AuthNotice, AuthPageShell } from './AuthPageShell';

const principal = {
  id: 'https://issuer.example.com|subject-user',
  email: 'user@example.com',
  roles: ['user', 'runner-admin'],
};

const meta = {
  title: 'Authentication/AuthPageShell',
  component: AuthPageShell,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    title: 'Authentication',
    description: 'Complete this step to continue.',
    children: <AuthNotice tone="info">Authentication content appears here.</AuthNotice>,
  },
} satisfies Meta<typeof AuthPageShell>;

export default meta;

type Story = StoryObj<typeof meta>;

export const Default: Story = {};

export const CheckingSession: Story = {
  args: {
    principalLoading: true,
  },
};

export const SignedIn: Story = {
  args: {
    principal,
  },
};
