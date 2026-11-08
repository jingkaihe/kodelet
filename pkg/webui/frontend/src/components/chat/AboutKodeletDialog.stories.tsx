import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, fn, spyOn, userEvent, within } from 'storybook/test';
import apiService from '../../services/api';
import {
  builtinOnlyToolInventory,
  sampleToolInventory,
  toolInventoryWithIssues,
} from '../../stories/toolInventoryFixtures';
import type { Runner, ToolInventory } from '../../types';
import AboutKodeletDialog from './AboutKodeletDialog';

const runner: Runner = {
  id: 'runner-1',
  displayName: 'kodelet',
  host: { instanceId: 'host-1', hostname: 'framework', os: 'linux', arch: 'amd64' },
  workspace: { path: '/home/jingkaihe/workspace/kodelet', name: 'kodelet' },
  kodeletVersion: '0.8.24-beta',
  manifestChanged: false,
  status: 'idle',
  connected: true,
  workspaceGitDiff: true,
  workspaceTerminal: true,
  workspaceBrowser: true,
  workspaceDiscovery: true,
  workspaceCwd: true,
  workspaceInventory: true,
  generation: 1,
};

type ToolInventoryResponse = ToolInventory | 'pending' | 'error';

const meta = {
  title: 'Chat/AboutKodeletDialog',
  component: AboutKodeletDialog,
  parameters: { layout: 'fullscreen', toolInventory: sampleToolInventory },
  args: {
    onClose: fn(),
    runner,
    terminalAuthorized: true,
    toolInventoryTarget: { runnerId: 'runner-1', cwd: '/home/jingkaihe/workspace/kodelet' },
    initialTab: 'tools',
  },
  beforeEach: ({ parameters }) => {
    const response = parameters.toolInventory as ToolInventoryResponse;
    const mocks = [
      spyOn(apiService, 'getServerStatus').mockResolvedValue({
        version: '0.8.24-beta',
        gitCommit: 'cca7ebc1a2b3c4d5e6f7',
        buildTime: '2026-11-07T18:00:00Z',
        apiReady: true,
        embeddedRunner: { enabled: true, ready: true, runnerId: 'runner-1' },
      }),
      spyOn(apiService, 'getToolInventory').mockImplementation(() =>
        response === 'pending'
          ? new Promise(() => {})
          : response === 'error'
            ? Promise.reject(new Error('the runner is offline'))
            : Promise.resolve(response)
      ),
    ];
    return () => {
      for (const mock of mocks) mock.mockRestore();
    };
  },
} satisfies Meta<typeof AboutKodeletDialog>;

export default meta;

type Story = StoryObj<typeof meta>;

/** Sections start expanded; plugins and extensions start folded with their counts. */
export const ToolsTab: Story = {};

/** Plugin → extension → tool; clicking a row reveals its description and path. */
export const ToolsPluginExpanded: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole('treeitem', { name: /^jingkaihe@skills/ }));
    await userEvent.click(canvas.getByRole('treeitem', { name: /^last-word/ }));
    await userEvent.click(canvas.getByRole('treeitem', { name: /^code-search/ }));
    await userEvent.click(canvas.getByRole('treeitem', { name: /^code_search/ }));
    await expect(canvas.getByText(/extensions\/code-search$/)).toBeVisible();
  },
};

/** Filtering keeps matches with their ancestors expanded and highlights the matched text. */
export const ToolsFiltered: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.type(
      await canvas.findByPlaceholderText('Filter tools, plugins, and skills'),
      'search'
    );
    await expect(canvas.getByRole('treeitem', { name: /^web_search/ })).toBeVisible();
    await expect(canvas.queryByRole('treeitem', { name: /^bash/ })).not.toBeInTheDocument();
  },
};

/** Failed extensions and disabled items stay visible so configuration problems are discoverable. */
export const ToolsWithIssues: Story = {
  parameters: { toolInventory: toolInventoryWithIssues },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(await canvas.findByRole('treeitem', { name: /^drawing-board/ }));
    await expect(canvas.getByText(/Cannot find module/)).toBeVisible();
  },
};

export const ToolsBuiltinOnly: Story = {
  parameters: { toolInventory: builtinOnlyToolInventory },
};

export const ToolsLoading: Story = {
  parameters: { toolInventory: 'pending' },
};

export const ToolsLoadError: Story = {
  parameters: { toolInventory: 'error' },
};

export const ToolsUnsupportedRunner: Story = {
  args: { runner: { ...runner, workspaceInventory: false } },
};

export const AboutTab: Story = {
  args: { initialTab: 'about' },
};

export const Narrow: Story = {
  decorators: [
    (Story) => (
      <div style={{ width: 380, height: '100dvh', position: 'relative' }}>
        <Story />
      </div>
    ),
  ],
};
