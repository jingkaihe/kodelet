import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, spyOn, userEvent, waitFor, within } from 'storybook/test';
import apiService from '../../services/api';
import ProviderSettingsDialog from './ProviderSettingsDialog';

const meta = {
  title: 'Chat/ProviderSettingsDialog',
  component: ProviderSettingsDialog,
  parameters: { layout: 'fullscreen' },
  args: { onClose: fn() },
  beforeEach: () => {
    const codexLogin = {
      id: 'storybook-codex', status: 'pending' as const,
      verificationUrl: 'https://auth.openai.com/codex/device', userCode: 'ABCD-EFGH',
    };
    const copilotLogin = {
      id: 'storybook-copilot', status: 'pending' as const,
      verificationUrl: 'https://github.com/login/device', userCode: 'WXYZ-2345',
    };
    const mocks = [
      spyOn(apiService, 'getCodexProviderStatus').mockResolvedValue({ provider: 'codex', connected: true }),
      spyOn(apiService, 'getCopilotProviderStatus').mockResolvedValue({ provider: 'copilot', connected: true }),
      spyOn(apiService, 'getAnthropicProviderStatus').mockResolvedValue({ provider: 'anthropic', connected: true }),
      spyOn(apiService, 'startCodexDeviceLogin').mockResolvedValue(codexLogin),
      spyOn(apiService, 'getCodexDeviceLogin').mockResolvedValue(codexLogin),
      spyOn(apiService, 'cancelCodexDeviceLogin').mockResolvedValue(undefined),
      spyOn(apiService, 'startCopilotDeviceLogin').mockResolvedValue(copilotLogin),
      spyOn(apiService, 'getCopilotDeviceLogin').mockResolvedValue(copilotLogin),
      spyOn(apiService, 'cancelCopilotDeviceLogin').mockResolvedValue(undefined),
      spyOn(apiService, 'startAnthropicOAuthLogin').mockResolvedValue({
        id: 'storybook-anthropic', status: 'pending',
        authorizationUrl: 'https://claude.ai/oauth/authorize?state=storybook',
      }),
      spyOn(apiService, 'completeAnthropicOAuthLogin').mockResolvedValue({
        id: 'storybook-anthropic', status: 'connected',
      }),
      spyOn(apiService, 'cancelAnthropicOAuthLogin').mockResolvedValue(undefined),
    ];
    return () => mocks.forEach((mock) => { mock.mockRestore(); });
  },
} satisfies Meta<typeof ProviderSettingsDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Connected: Story = {};

export const DeviceSignIn: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole('button', { name: 'Reconnect ChatGPT' }));
    await expect(canvas.findByLabelText('ChatGPT device code')).resolves.toHaveTextContent('ABCD-EFGH');
  },
};

export const AuthorizationCode: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole('button', { name: 'Reconnect Anthropic' }));
    await waitFor(() => expect(canvas.getByRole('textbox', { name: 'Anthropic authorization code' })).toBeVisible());
  },
};

export const FailedSignIn: Story = {
  beforeEach: () => {
    const mock = spyOn(apiService, 'startCodexDeviceLogin').mockResolvedValue({
      id: 'storybook-codex', status: 'failed', message: 'The device code expired. Start a new sign-in.',
    });
    return () => mock.mockRestore();
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole('button', { name: 'Reconnect ChatGPT' }));
    await expect(canvas.findByRole('alert')).resolves.toHaveTextContent('The device code expired.');
  },
};
