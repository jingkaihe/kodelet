import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ContextBreakdown, Runner, ServerStatus, ToolInventory } from '../../types';
import AboutKodeletDialog from './AboutKodeletDialog';

const mockGetServerStatus = vi.fn();
const mockGetToolInventory = vi.fn();

vi.mock('../../services/api', () => ({
  default: {
    getServerStatus: (...args: unknown[]) => mockGetServerStatus(...args),
    getToolInventory: (...args: unknown[]) => mockGetToolInventory(...args),
  },
}));

const serverStatus = {
  version: '1.2.3',
  gitCommit: 'abcdef1234567890abcdef1234567890abcdef1234',
  buildTime: '2026-09-27T23:45:00Z',
  apiReady: true,
  embeddedRunner: { enabled: true, ready: true, runnerId: 'runner-1' },
} satisfies ServerStatus;

const makeRunner = (overrides: Partial<Runner> = {}): Runner => ({
  id: 'runner-1',
  displayName: 'Development runner',
  host: {
    instanceId: 'host-1',
    hostname: 'worker',
    os: 'linux',
    arch: 'amd64',
  },
  workspace: { path: '/workspace/kodelet', name: 'kodelet workspace' },
  kodeletVersion: '1.2.2',
  manifestChanged: false,
  status: 'idle',
  connected: true,
  workspaceGitDiff: true,
  workspaceTerminal: true,
  workspaceBrowser: true,
  workspaceDiscovery: true,
  workspaceCwd: true,
  generation: 1,
  ...overrides,
});

const capabilities = [
  'Git diffs',
  'Terminal',
  'Browser',
  'Slash commands',
  'Working directory',
] as const;

const contextBreakdown: ContextBreakdown = {
  systemPromptTokens: 100,
  toolDefinitionTokens: 200,
  inputTextTokens: 100,
  outputTextTokens: 100,
  thinkingTokens: 25,
  otherTokens: 25,
  tools: [
    { name: 'code_execute', inputTokens: 100, outputTokens: 250, calls: 3 },
    { name: 'skill', inputTokens: 20, outputTokens: 80, calls: 1 },
  ],
  totalTokens: 1000,
  codeMode: 'only',
  estimated: true,
};

const expectCapability = (label: string, state: string) => {
  const definition = screen.getByText(label, { selector: 'dt' }).nextElementSibling;
  expect(definition?.tagName).toBe('DD');
  expect(definition).toHaveTextContent(state);
};

const toolInventory: ToolInventory = {
  builtinTools: [{ name: 'bash', description: 'Run a bash command.', enabled: true }],
  plugins: [],
  extensions: [],
  skills: [],
};

const toolTarget = { runnerId: 'runner-1', cwd: '/workspace/kodelet' };

describe('AboutKodeletDialog', () => {
  beforeEach(() => {
    mockGetServerStatus.mockReset().mockResolvedValue(serverStatus);
    mockGetToolInventory.mockReset().mockResolvedValue(toolInventory);
  });

  describe('tools tab', () => {
    it('is hidden without a workspace target', async () => {
      render(<AboutKodeletDialog onClose={vi.fn()} runner={makeRunner()} terminalAuthorized />);
      expect(screen.queryByRole('tab', { name: 'Tools' })).not.toBeInTheDocument();
      await screen.findByText('1.2.3', { selector: 'dd' });
    });

    it('loads the tool list when first opened and keeps it across tab switches', async () => {
      const user = userEvent.setup();
      let resolveInventory: (value: ToolInventory) => void = () => {};
      mockGetToolInventory.mockImplementationOnce(
        () =>
          new Promise<ToolInventory>((resolve) => {
            resolveInventory = resolve;
          })
      );
      render(
        <AboutKodeletDialog
          onClose={vi.fn()}
          runner={makeRunner({ workspaceInventory: true })}
          terminalAuthorized
          toolInventoryTarget={toolTarget}
        />
      );
      expect(mockGetToolInventory).not.toHaveBeenCalled();

      await user.click(screen.getByRole('tab', { name: 'Tools' }));
      expect(screen.getByText('Loading tools…')).toBeInTheDocument();
      await act(async () => resolveInventory(toolInventory));
      expect(await screen.findByRole('treeitem', { name: /^bash/ })).toBeInTheDocument();
      expect(mockGetToolInventory).toHaveBeenCalledExactlyOnceWith(toolTarget);

      await user.click(screen.getByRole('tab', { name: 'About' }));
      await user.click(screen.getByRole('tab', { name: 'Tools' }));
      expect(screen.getByRole('treeitem', { name: /^bash/ })).toBeInTheDocument();
      expect(mockGetToolInventory).toHaveBeenCalledOnce();
    });

    it('explains runners that cannot list tools without calling them', async () => {
      const { rerender } = render(
        <AboutKodeletDialog
          initialTab="tools"
          onClose={vi.fn()}
          runner={makeRunner()}
          terminalAuthorized
          toolInventoryTarget={toolTarget}
        />
      );
      expect(screen.getByText(/does not list its tools/)).toBeInTheDocument();

      rerender(
        <AboutKodeletDialog
          initialTab="tools"
          onClose={vi.fn()}
          runner={makeRunner({ workspaceInventory: true, connected: false, status: 'offline' })}
          terminalAuthorized
          toolInventoryTarget={toolTarget}
        />
      );
      expect(screen.getByText(/This runner is not ready/)).toBeInTheDocument();
      expect(mockGetToolInventory).not.toHaveBeenCalled();
      // The about tab's status request still settles while the tools tab is shown.
      await act(async () => {});
    });

    it('reports load failures and retries', async () => {
      const user = userEvent.setup();
      mockGetToolInventory.mockRejectedValueOnce(new Error('the runner is offline'));
      render(
        <AboutKodeletDialog
          initialTab="tools"
          onClose={vi.fn()}
          runner={makeRunner({ workspaceInventory: true })}
          terminalAuthorized
          toolInventoryTarget={toolTarget}
        />
      );
      expect(await screen.findByRole('alert')).toHaveTextContent(
        'Could not load tools: the runner is offline'
      );

      await user.click(screen.getByRole('button', { name: 'Try again' }));
      expect(await screen.findByRole('treeitem', { name: /^bash/ })).toBeInTheDocument();
      expect(mockGetToolInventory).toHaveBeenCalledTimes(2);
    });
  });

  it('loads the server version separately from the runner version and shows live capabilities', async () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <AboutKodeletDialog onClose={onClose} runner={makeRunner()} terminalAuthorized />
    );

    expect(screen.getByRole('dialog', { name: 'About Kodelet' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'About Kodelet' })).not.toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Kodelet' }).closest('header')).toContainElement(
      screen.getByRole('button', { name: 'Close about Kodelet' })
    );
    expect(await screen.findByText('1.2.3', { selector: 'dd' })).toBeInTheDocument();
    expect(mockGetServerStatus).toHaveBeenCalledOnce();
    const buildCommit = screen.getByText('Build commit', { selector: 'dt' }).nextElementSibling;
    expect(buildCommit?.textContent).toBe(serverStatus.gitCommit.slice(0, 7));
    expect(buildCommit).toHaveAttribute('title', serverStatus.gitCommit);
    const buildDate = screen.getByText('Build date', { selector: 'dt' }).nextElementSibling;
    const date = new Date(serverStatus.buildTime);
    expect(buildDate).toHaveTextContent(
      date.toLocaleDateString(undefined, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        timeZone: 'UTC',
      })
    );
    expect(buildDate?.querySelector('time')).toHaveAttribute('datetime', date.toISOString());
    expect(buildDate?.querySelector('time')).toHaveAttribute('title', serverStatus.buildTime);
    expect(screen.getByText('1.2.2', { selector: 'dd' })).toBeInTheDocument();
    expect(screen.getByText('Development runner')).toBeInTheDocument();
    for (const capability of capabilities) {
      expectCapability(capability, 'Enabled');
    }

    rerender(
      <AboutKodeletDialog
        onClose={onClose}
        runner={makeRunner({ status: 'busy' })}
        terminalAuthorized
      />
    );

    for (const capability of capabilities) {
      expectCapability(capability, 'Enabled');
    }
    expect(mockGetServerStatus).toHaveBeenCalledOnce();
  });

  it('shows Unknown for missing or unknown build metadata and unparseable dates', async () => {
    for (const metadata of [
      { gitCommit: undefined, buildTime: undefined },
      { gitCommit: 'unknown', buildTime: 'unknown' },
      { gitCommit: undefined, buildTime: 'not a date' },
    ]) {
      mockGetServerStatus.mockResolvedValueOnce({ ...serverStatus, ...metadata });
      const { unmount } = render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized />);

      await screen.findByText('1.2.3', { selector: 'dd' });
      for (const label of ['Build commit', 'Build date']) {
        const definition = screen.getByText(label, { selector: 'dt' }).nextElementSibling;
        expect(definition).toHaveTextContent(/^Unknown$/);
        expect(definition?.querySelector('time')).toBeNull();
      }
      unmount();
    }
  });

  it('requires terminal permission for both terminal and browser capabilities', async () => {
    const onClose = vi.fn();
    const runner = makeRunner();
    const { rerender } = render(
      <AboutKodeletDialog onClose={onClose} runner={runner} terminalAuthorized={false} />
    );

    await screen.findByText('1.2.3', { selector: 'dd' });
    for (const capability of capabilities) {
      expectCapability(
        capability,
        capability === 'Terminal' || capability === 'Browser' ? 'Not enabled' : 'Enabled'
      );
    }

    rerender(<AboutKodeletDialog onClose={onClose} runner={runner} terminalAuthorized />);

    expectCapability('Terminal', 'Enabled');
    expectCapability('Browser', 'Enabled');
  });

  it('treats false and missing flags as not enabled and falls back to workspace name and unknown version', async () => {
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        runner={makeRunner({
          displayName: undefined,
          kodeletVersion: undefined,
          workspaceGitDiff: false,
          workspaceTerminal: undefined,
          workspaceBrowser: false,
          workspaceDiscovery: undefined,
          workspaceCwd: false,
        })}
        terminalAuthorized
      />
    );

    await screen.findByText('1.2.3', { selector: 'dd' });
    expect(screen.getByText('kodelet workspace')).toBeInTheDocument();
    expect(screen.getByText('Unknown', { selector: 'dd' })).toBeInTheDocument();
    for (const capability of capabilities) {
      expectCapability(capability, 'Not enabled');
    }
  });

  it('shows unavailable capabilities when no runner is selected', async () => {
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized />);

    await screen.findByText('1.2.3', { selector: 'dd' });
    for (const capability of capabilities) {
      expectCapability(capability, 'Unavailable');
    }
  });

  it.each([
    { label: 'disconnected', connected: false, status: 'idle' as const },
    { label: 'offline', connected: false, status: 'offline' as const },
    { label: 'incompatible', connected: true, status: 'incompatible' as const },
  ])('shows unavailable capabilities for a $label runner', async ({ connected, status }) => {
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        runner={makeRunner({ connected, status })}
        terminalAuthorized
      />
    );

    await screen.findByText('1.2.3', { selector: 'dd' });
    for (const capability of capabilities) {
      expectCapability(capability, 'Unavailable');
    }
  });

  it('shows loading while the server version is pending, then replaces it with the version', async () => {
    let resolveStatus!: (status: ServerStatus) => void;
    mockGetServerStatus.mockReturnValueOnce(
      new Promise<ServerStatus>((resolve) => {
        resolveStatus = resolve;
      })
    );
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized={false} />);

    expect(screen.getByText('Loading version…')).toBeInTheDocument();
    expect(screen.queryByText('1.2.3')).not.toBeInTheDocument();

    await act(async () => resolveStatus(serverStatus));

    expect(screen.getByText('1.2.3', { selector: 'dd' })).toBeInTheDocument();
    expect(screen.queryByText('Loading version…')).not.toBeInTheDocument();
  });

  it('shows a fetch failure and retries without losing the runner capabilities', async () => {
    const user = userEvent.setup();
    mockGetServerStatus.mockRejectedValueOnce(new Error('Server unavailable'));
    render(<AboutKodeletDialog onClose={vi.fn()} runner={makeRunner()} terminalAuthorized />);

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Could not load the server version.'
    );
    expectCapability('Git diffs', 'Enabled');
    await user.click(screen.getByRole('button', { name: 'Try again' }));

    expect(await screen.findByText('1.2.3', { selector: 'dd' })).toBeInTheDocument();
    expect(mockGetServerStatus).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Try again' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus();
  });

  it('focuses the close button, traps Tab in both directions and dismisses with Escape', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<AboutKodeletDialog onClose={onClose} terminalAuthorized={false} />);

    const close = screen.getByRole('button', { name: 'Close about Kodelet' });
    await waitFor(() => expect(close).toHaveFocus());
    await user.tab();
    expect(screen.getByRole('tab', { name: 'About' })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('tabpanel', { name: 'About' })).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.tab({ shift: true });
    expect(screen.getByRole('tabpanel', { name: 'About' })).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('wraps keyboard focus between the close and retry buttons after a fetch failure', async () => {
    const user = userEvent.setup();
    mockGetServerStatus.mockRejectedValueOnce(new Error('Server unavailable'));
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized={false} />);

    const retry = await screen.findByRole('button', { name: 'Try again' });
    const close = screen.getByRole('button', { name: 'Close about Kodelet' });
    await waitFor(() => expect(close).toHaveFocus());
    await user.tab({ shift: true });
    expect(retry).toHaveFocus();
    await user.tab();
    expect(close).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('tab', { name: 'About' })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('tabpanel', { name: 'About' })).toHaveFocus();
    await user.tab();
    expect(retry).toHaveFocus();
  });

  it('supports roving tab focus, arrow keys, Home and End without refetching status', async () => {
    const user = userEvent.setup();
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized />);
    await screen.findByText('1.2.3');
    const about = screen.getByRole('tab', { name: 'About' });
    const context = screen.getByRole('tab', { name: 'Context' });
    expect(about).toHaveAttribute('aria-selected', 'true');
    expect(context).toHaveAttribute('tabindex', '-1');
    about.focus();
    for (const [key, selected] of [
      ['{ArrowRight}', context],
      ['{ArrowRight}', about],
      ['{ArrowLeft}', context],
      ['{Home}', about],
      ['{End}', context],
    ] as const) {
      await user.keyboard(key);
      expect(selected).toHaveFocus();
      expect(selected).toHaveAttribute('aria-selected', 'true');
      expect(selected).toHaveAttribute('aria-controls', screen.getByRole('tabpanel').id);
      expect(screen.getByRole('tabpanel')).toHaveAttribute('aria-labelledby', selected.id);
    }
    await user.tab();
    expect(screen.getByRole('tabpanel', { name: 'Context' })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus();
    expect(mockGetServerStatus).toHaveBeenCalledOnce();
  });

  it.each([
    'only',
    'on',
    'off',
  ])('shows %s mode in two stacked bars with distinct composition and tool shares', async (codeMode) => {
    const user = userEvent.setup();
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{
          currentContextWindow: 1500,
          maxContextWindow: 3000,
          contextBreakdown: { ...contextBreakdown, codeMode },
        }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    expect(screen.queryByText('Server version')).not.toBeInTheDocument();
    const meter = screen.getByRole('meter', { name: 'Context window usage' });
    expect(meter).toHaveAttribute('aria-valuenow', '1500');
    expect(meter).toHaveAttribute('aria-valuemax', '3000');
    expect(meter).toHaveAttribute('aria-valuemin', '0');
    expect(meter).toHaveAttribute('aria-valuetext', '1,500 of 3,000 tokens, 50.0% used');
    expect(screen.getByText('1.5K / 3.0K tokens (50.0%)')).toBeInTheDocument();
    expect(screen.getByText('Used split estimated · remainder available')).toBeInTheDocument();
    expect(screen.getByText('~1.0K tokens')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: 'Composition' })).toBeInTheDocument();
    expect(screen.getByText(`Code mode: ${codeMode}`)).toBeInTheDocument();
    const composition = screen.getByRole('region', { name: 'Composition' });
    const rows = [
      ['System prompt', '100 tokens', '10.0%', 'system', '5%'],
      ['Tool definitions', '200 tokens', '20.0%', 'definitions', '10%'],
      ['Input text', '100 tokens', '10.0%', 'input', '5%'],
      ['Output text', '100 tokens', '10.0%', 'output', '5%'],
      ['Thinking', '25 tokens', '2.5%', 'thinking', '1.25%'],
      ['Other', '25 tokens', '2.5%', 'other', '1.25%'],
      ['Tool usage', '450 tokens', '45.0%', 'tool', '22.5%'],
    ];
    expect(Array.from(composition.querySelectorAll('dt'), (dt) => dt.textContent)).toEqual(
      rows.map(([name]) => name)
    );
    for (const [name, tokens, share, tone, width] of rows) {
      const row = within(composition).getByText(name, { selector: 'dt' }).parentElement;
      expect(row).toHaveTextContent(tokens);
      expect(row).toHaveTextContent(share);
      expect(row).toHaveClass(`about-context-${tone}`);
      expect(row?.querySelector('.about-context-swatch')).toHaveAttribute('aria-hidden', 'true');
      const segment = meter.querySelector(`.about-context-${tone}`);
      expect(segment).toHaveStyle({ width });
      expect(segment).toHaveAttribute('aria-hidden', 'true');
    }
    expect(meter.querySelector('.about-context-available')).toHaveStyle({ width: '50%' });
    expect(within(composition).queryByText('Last response')).not.toBeInTheDocument();
    const toolBar = screen.getByRole('img', { name: 'Tool usage breakdown' });
    expect(toolBar).not.toHaveAttribute('aria-valuenow');
    expect(toolBar.querySelector('.about-context-available')).toBeNull();
    expect(toolBar.children).toHaveLength(2);
    for (const [index, name, tokens, percent, tone, width] of [
      [0, 'code_execute', '350 tokens', '77.8%', 'tool', (350 / 450) * 100],
      [1, 'skill', '100 tokens', '22.2%', 'system', (100 / 450) * 100],
    ] as const) {
      expect(toolBar.children[index]).toHaveStyle({ width: `${width}%` });
      expect(toolBar.children[index]).toHaveClass(`about-context-${tone}`);
      const row = screen.getByText(name, { selector: 'dt' }).parentElement;
      expect(row).toHaveTextContent(tokens);
      expect(row).toHaveTextContent(percent);
      expect(row).toHaveClass(`about-context-${tone}`);
    }
    expect(screen.getByText('~450 tokens')).toBeInTheDocument();
    expect(screen.getByText('3 calls · 100 input · 250 output tokens')).toBeInTheDocument();
    expect(screen.getByText('1 call · 20 input · 80 output tokens')).toBeInTheDocument();
    expect(screen.getByRole('tabpanel').querySelectorAll('.about-context-meter')).toHaveLength(2);
    expect(screen.getByRole('tabpanel').querySelector('.about-context-bar')).toBeNull();
  });

  it.each([
    undefined,
    null,
    0,
    100,
  ])('distinguishes reported and unknown thinking: %s', async (thinkingTokens) => {
    const user = userEvent.setup();
    const totalTokens = 100 + (thinkingTokens ?? 0);
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{
          currentContextWindow: 150,
          maxContextWindow: 300,
          contextBreakdown: {
            systemPromptTokens: 0,
            toolDefinitionTokens: 0,
            inputTextTokens: 100,
            outputTextTokens: 0,
            thinkingTokens,
            otherTokens: 0,
            totalTokens,
            tools: [],
            codeMode: 'off',
            estimated: true,
          },
        }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    const row = screen.getByText('Thinking', { selector: 'dt' }).parentElement;
    const meter = screen.getByRole('meter');
    const segment = meter.querySelector('.about-context-thinking');
    expect(segment).toHaveStyle({
      width: `${(50 * (thinkingTokens ?? 0)) / totalTokens}%`,
    });
    expect(meter.querySelector('.about-context-available')).toHaveStyle({ width: '50%' });
    expect(
      Array.from(meter.children).reduce(
        (sum, part) => sum + Number.parseFloat((part as HTMLElement).style.width),
        0
      )
    ).toBe(100);
    if (thinkingTokens == null) {
      expect(row).toHaveTextContent(/^Thinking—$/);
      expect(row).not.toHaveTextContent(/tokens|%/);
      expect(segment).toHaveAttribute('title', 'Thinking: —');
    } else {
      expect(row).toHaveTextContent(`${thinkingTokens} tokens`);
      expect(row).toHaveTextContent(`${((thinkingTokens / totalTokens) * 100).toFixed(1)}%`);
    }
    expect(row?.querySelector('.about-context-detail')).toBeNull();
    expect(segment?.getAttribute('title')).not.toContain('Last response');
    expect(screen.getByText('Other', { selector: 'dt' }).parentElement).toHaveTextContent(
      '0 tokens'
    );
    expect(screen.queryByText('Other / reasoning')).not.toBeInTheDocument();
  });

  it.each([
    undefined,
    {},
    { currentContextWindow: 400, maxContextWindow: 1000 },
  ])('handles missing breakdowns without inventing estimates: %j', async (usage) => {
    const user = userEvent.setup();
    render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized usage={usage} />);
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    expect(screen.getByText('Breakdown unavailable until a model turn.')).toBeInTheDocument();
    expect(screen.queryByText(/tokens · estimated/)).not.toBeInTheDocument();
    expect(screen.queryByRole('img', { name: 'Tool usage breakdown' })).not.toBeInTheDocument();
    if (usage?.currentContextWindow !== undefined) {
      const meter = screen.getByRole('meter');
      expect(meter).toHaveAttribute('aria-valuenow', '400');
      expect(meter.querySelector('.about-context-unclassified')).toHaveStyle({ width: '40%' });
      expect(meter.querySelector('.about-context-available')).toHaveStyle({ width: '60%' });
    } else {
      expect(screen.queryByRole('meter')).not.toBeInTheDocument();
      expect(screen.getByText('Context usage unavailable until a model turn.')).toBeInTheDocument();
    }
  });

  it('keeps all tool names and usage accessible in a long breakdown', async () => {
    const user = userEvent.setup();
    const tools = Array.from({ length: 30 }, (_, index) => ({
      name: `extension_with_a_long_name_and_no_spaces_tool_${index}`,
      calls: 1,
      inputTokens: 10,
      outputTokens: 20,
    }));
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{
          currentContextWindow: 2000,
          maxContextWindow: 4000,
          contextBreakdown: { ...contextBreakdown, tools, totalTokens: 1450 },
        }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    const toolBar = screen.getByRole('img', { name: 'Tool usage breakdown' });
    const palette = ['tool', 'system', 'definitions', 'input', 'output', 'thinking'];
    for (const [index, tool] of tools.entries()) {
      const row = screen.getByText(tool.name, { selector: 'dt' }).parentElement;
      expect(row).toHaveTextContent('3.3%');
      expect(row).toHaveClass(`about-context-${palette[index % palette.length]}`);
      expect(toolBar.children[index]).toHaveClass(
        `about-context-${palette[index % palette.length]}`
      );
    }
    expect(screen.getAllByText('1 call · 10 input · 20 output tokens')).toHaveLength(30);
    expect(screen.getByRole('meter').querySelectorAll('.about-context-tool')).toHaveLength(1);
    expect(toolBar.children).toHaveLength(30);
    expect(screen.getByText('Thinking', { selector: 'dt' })).toBeInTheDocument();
    expect(screen.getByText('Other', { selector: 'dt' })).toBeInTheDocument();
  });

  it('handles unknown capacity and an empty estimate, then updates without reopening', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = render(
      <AboutKodeletDialog
        onClose={onClose}
        terminalAuthorized
        usage={{
          currentContextWindow: 0,
          maxContextWindow: 0,
          contextBreakdown: {
            ...contextBreakdown,
            systemPromptTokens: 0,
            toolDefinitionTokens: 0,
            inputTextTokens: 0,
            outputTextTokens: 0,
            thinkingTokens: 0,
            otherTokens: 0,
            tools: [],
            totalTokens: 0,
          },
        }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    const panel = screen.getByRole('tabpanel');
    expect(within(panel).queryByRole('meter')).not.toBeInTheDocument();
    expect(panel).toHaveTextContent('Estimated composition · capacity unavailable');
    expect(panel).toHaveTextContent('~0 tokens');
    expect(screen.getByRole('img', { name: 'Estimated composition' })).not.toHaveAttribute(
      'aria-valuenow'
    );
    expect(screen.queryByRole('img', { name: 'Tool usage breakdown' })).not.toBeInTheDocument();
    expect(panel).toHaveTextContent('No tool usage in this context.');
    expect(panel.querySelector('.about-context-unclassified')).toHaveStyle({ width: '0%' });
    expect(panel).not.toHaveTextContent(/NaN|Infinity/);
    rerender(
      <AboutKodeletDialog
        onClose={onClose}
        terminalAuthorized
        usage={{ currentContextWindow: 2000, maxContextWindow: 1000, contextBreakdown }}
      />
    );
    const meter = screen.getByRole('meter');
    expect(meter).toHaveAttribute('aria-valuenow', '1000');
    expect(meter).toHaveAttribute('aria-valuetext', '2,000 of 1,000 tokens, 200.0% used');
    expect(meter.querySelector('.about-context-available')).toHaveStyle({ width: '0%' });
    expect(meter.querySelector('.about-context-system')).toHaveStyle({ width: '10%' });
    expect(screen.getByText('~1.0K tokens')).toBeInTheDocument();
    expect(mockGetServerStatus).toHaveBeenCalledOnce();
  });

  it.each([
    undefined,
    0,
  ])('shows full composition without inventing capacity: %s', async (capacity) => {
    const user = userEvent.setup();
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{ currentContextWindow: 500, maxContextWindow: capacity, contextBreakdown }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    expect(screen.getByText('500 tokens · Capacity unavailable.')).toBeInTheDocument();
    expect(screen.getByText('Estimated composition · capacity unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('meter')).not.toBeInTheDocument();
    const bar = screen.getByRole('img', { name: 'Estimated composition' });
    expect(bar).not.toHaveAttribute('aria-valuenow');
    expect(bar.querySelector('.about-context-system')).toHaveStyle({ width: '10%' });
    expect(bar.querySelector('.about-context-tool')).toHaveStyle({ width: '45%' });
    expect(bar.querySelector('.about-context-available')).toBeNull();
    expect(
      Array.from(bar.children).reduce(
        (sum, part) => sum + Number.parseFloat((part as HTMLElement).style.width),
        0
      )
    ).toBe(100);
  });

  it('keeps reported usage visible with an empty estimate and zero-token tool calls', async () => {
    const user = userEvent.setup();
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{
          currentContextWindow: 500,
          maxContextWindow: 1000,
          contextBreakdown: {
            systemPromptTokens: 0,
            toolDefinitionTokens: 0,
            inputTextTokens: 0,
            outputTextTokens: 0,
            otherTokens: 0,
            totalTokens: 0,
            codeMode: 'off',
            estimated: true,
            tools: [{ name: 'empty', calls: 1, inputTokens: 0, outputTokens: 0 }],
          },
        }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    const meter = screen.getByRole('meter');
    expect(meter.querySelector('.about-context-unclassified')).toHaveStyle({ width: '50%' });
    expect(meter.querySelector('.about-context-available')).toHaveStyle({ width: '50%' });
    expect(screen.getByRole('region', { name: 'Composition' }).querySelectorAll('dt')).toHaveLength(
      7
    );
    expect(screen.queryByRole('img', { name: 'Tool usage breakdown' })).not.toBeInTheDocument();
    expect(
      within(screen.getByRole('region', { name: 'Tool usage' })).getByText('~0 tokens')
    ).toBeInTheDocument();
    expect(screen.getByText('1 call · 0 input · 0 output tokens')).toBeInTheDocument();
    expect(screen.getByText('empty', { selector: 'dt' }).parentElement).toHaveTextContent('0.0%');
    expect(screen.getByRole('tabpanel').innerHTML).not.toMatch(/NaN|Infinity/);
  });

  it.each([
    0, -10,
  ])('clamps non-positive reported usage to an empty used area: %d', async (current) => {
    const user = userEvent.setup();
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{ currentContextWindow: current, maxContextWindow: 1000, contextBreakdown }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    const meter = screen.getByRole('meter');
    expect(meter).toHaveAttribute('aria-valuenow', '0');
    expect(meter.querySelector('.about-context-system')).toHaveStyle({ width: '0%' });
    expect(meter.querySelector('.about-context-available')).toHaveStyle({ width: '100%' });
  });

  it.each([
    [999, '999'],
    [1000, '1.0K'],
    [125200, '125.2K'],
    [1000000, '1.0M'],
  ])('uses TUI-compatible compact token counts for %d', async (tokens, formatted) => {
    const user = userEvent.setup();
    render(
      <AboutKodeletDialog
        onClose={vi.fn()}
        terminalAuthorized
        usage={{ currentContextWindow: tokens, maxContextWindow: 2000000 }}
      />
    );
    await user.click(screen.getByRole('tab', { name: 'Context' }));
    expect(
      screen.getByText(
        `${formatted} / 2.0M tokens (${((Number(tokens) / 2000000) * 100).toFixed(1)}%)`
      )
    ).toBeInTheDocument();
  });

  it('dismisses with the close button', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<AboutKodeletDialog onClose={onClose} terminalAuthorized={false} />);

    await user.click(screen.getByRole('button', { name: 'Close about Kodelet' }));

    expect(onClose).toHaveBeenCalledOnce();
  });

  it('restores focus to a connected trigger and removes its keyboard handler on unmount', async () => {
    render(<button type="button">Open about Kodelet</button>);
    const trigger = screen.getByRole('button', { name: 'Open about Kodelet' });
    trigger.focus();
    const onClose = vi.fn();
    const { unmount } = render(<AboutKodeletDialog onClose={onClose} terminalAuthorized={false} />);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus()
    );
    unmount();

    await waitFor(() => expect(trigger).toHaveFocus());
    fireEvent.keyDown(trigger, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
  });

  it('restores focus to the sidebar account trigger when the original menu item unmounts', async () => {
    const accountButton = (
      <button aria-haspopup="menu" className="sidebar-account-trigger" type="button">
        Account menu
      </button>
    );
    const { rerender: rerenderSidebar } = render(
      <>
        {accountButton}
        <button role="menuitem" type="button">
          About Kodelet
        </button>
      </>
    );
    screen.getByRole('menuitem', { name: 'About Kodelet' }).focus();
    const { unmount } = render(<AboutKodeletDialog onClose={vi.fn()} terminalAuthorized={false} />);

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Close about Kodelet' })).toHaveFocus()
    );
    rerenderSidebar(accountButton);
    unmount();

    await waitFor(() => expect(screen.getByRole('button', { name: 'Account menu' })).toHaveFocus());
  });
});
