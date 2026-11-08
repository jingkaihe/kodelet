import type { Meta, StoryObj } from '@storybook/react-vite';
import { GitCompareArrows, Globe, PanelRight, SquareTerminal } from 'lucide-react';
import { type ComponentProps, useEffect, useRef, useState } from 'react';
import { expect, fn, userEvent, waitFor, within } from 'storybook/test';
import { sampleChatMessages, sampleConversations, sampleGitDiff } from '../../stories/fixtures';
import type { ChatRenderMessage, Conversation } from '../../types';
import { cn } from '../../utils';
import GitDiffModal from '../workspace/GitDiffModal';
import ChatComposer from './ChatComposer';
import ChatSidebar from './ChatSidebar';
import ChatTranscript from './ChatTranscript';
import ConversationTabs, {
  adjacentConversationTabId,
  type ConversationTab,
  type ConversationTabStatus,
} from './ConversationTabs';
import NewChatContextDialog from './NewChatContextDialog';
import newChatDialogStories from './NewChatContextDialog.stories';

const KODELET = '/home/jingkaihe/workspace/kodelet';
const PLUGINS = '/home/jingkaihe/workspace/plugins';

const DESIGN_NOTES = `
Browser-style conversation tabs in the header strip above the transcript.

- **Placement:** tabs fill the strip; no extra vertical space is used.
- **Tab menu:** each tab has a ▾ where a close button would be, shown on hover and on the active tab. It also opens on right-click or Shift+F10. The menu shows the full title and workspace path, then **Copy conversation ID**, **Copy workspace path** and **Close tab**, replacing today's hidden click-the-path-to-copy. A new chat has nothing to copy yet, so it offers **Config** (the new chat settings: profile, runner and workspace) and **Close tab**.
- **Open conversations only:** a tab exists for each conversation you open. Clicking one in the sidebar opens it in a new tab, or focuses its tab if it is already open.
- **Model:** a tab is just a conversation ID (or one unsaved draft). The URL \`/c/:id\` stays the source of truth for the active tab, and only the active conversation is mounted, so there is still one stream per window.
- **Background status:** running comes from the conversation list the sidebar already polls. New activity and failed mark a run that finished while its tab was in the background, and needs input marks a tab waiting on an extension prompt. Opening the tab clears them.
- **Closing:** close from the tab menu, by middle-clicking the tab or with Delete. Closing never stops the agent. It keeps running in the background, the sidebar still shows it as running, and reopening it picks up the progress. There is no confirmation because nothing is lost.
- **Keyboard:** arrow keys, Home and End move between tabs, Delete closes, Shift+F10 or the Menu key opens the tab menu, and Alt/⌥+[ and ] cycle tabs from anywhere.
- **Narrow panels:** when the workspace panel or a small screen leaves less than ~34rem, the strip collapses into a single switcher with a count badge, plus a ▾ for the active tab's menu.
`;

const titleOf = (conversation: Conversation): string =>
  conversation.summary || conversation.preview || conversation.firstMessage || 'Untitled chat';

const baseConversations: Conversation[] = [
  ...sampleConversations,
  {
    id: 'conv-release',
    createdAt: '2026-05-20T08:00:00Z',
    updatedAt: '2026-05-20T09:45:00Z',
    messageCount: 7,
    summary: 'Confirm the release rollout',
    cwd: KODELET,
  },
  {
    id: 'conv-flaky',
    createdAt: '2026-05-20T07:10:00Z',
    updatedAt: '2026-05-20T07:40:00Z',
    messageCount: 4,
    summary: 'Fix flaky runner enrollment test',
    cwd: KODELET,
  },
];

const user = (content: string): ChatRenderMessage => ({ role: 'user', content });
const assistant = (content: string): ChatRenderMessage => ({
  role: 'assistant',
  blocks: [{ type: 'message', content }],
});

const baseTranscripts: Record<string, ChatRenderMessage[]> = {
  'conv-active': sampleChatMessages,
  'conv-review': [
    user('Review terminal session handling, especially reconnects after a pop-out.'),
    assistant(
      'Reading `TerminalTabs.tsx` and `TerminalModal.tsx`. The tab state is keyed by workspace and persisted per runner, so reconnects should land on the same shell.'
    ),
  ],
  'conv-docs': [
    user('Document the plugin install flow.'),
    assistant(
      'Done. I added an **Install a plugin** section to the manual covering `kodelet plugin add`, trust prompts and where skills are linked.'
    ),
  ],
  'conv-release': [
    user('Prepare the release notes and tag the release.'),
    assistant(
      'The changelog is ready. Before I push the `v0.9.0` tag I need you to confirm the rollout window. Reply **yes** to continue.'
    ),
  ],
  'conv-flaky': [
    user('Fix the flaky runner enrollment test.'),
    assistant(
      'The run stopped: the provider returned `529 overloaded` three times. Send another message to retry.'
    ),
  ],
};

const initialStatuses: Record<string, ConversationTabStatus> = {
  'conv-review': 'running',
  'conv-docs': 'unread',
  'conv-release': 'attention',
  'conv-flaky': 'error',
};

const initialTabIds = ['conv-active', 'conv-review', 'conv-docs', 'conv-release', 'conv-flaky'];

const isDraft = (id: string) => id.startsWith('draft-');

interface ShellProps {
  workspacePanelOpen?: boolean;
}

/** Wires the real chat components together to show how tabs behave in the page. */
const ChatShell = ({ workspacePanelOpen = false }: ShellProps) => {
  const [conversations, setConversations] = useState(baseConversations);
  const [ids, setIds] = useState(initialTabIds);
  const [activeId, setActiveId] = useState('conv-active');
  const [statuses, setStatuses] = useState(initialStatuses);
  const [transcripts, setTranscripts] = useState(baseTranscripts);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [draftCwd, setDraftCwd] = useState(KODELET);
  const [configOpen, setConfigOpen] = useState(false);
  const [cwdQuery, setCwdQuery] = useState(KODELET);
  const [cwdSuggestionsOpen, setCwdSuggestionsOpen] = useState(false);
  const openConfig = () => {
    setCwdQuery(draftCwd);
    setConfigOpen(true);
  };
  const [panelOpen, setPanelOpen] = useState(workspacePanelOpen);
  const activeRef = useRef(activeId);
  const counterRef = useRef(1);
  activeRef.current = activeId;

  const conversationById = (id: string) => conversations.find((item) => item.id === id);
  const tabs: ConversationTab[] = ids.map((id) => {
    const conversation = conversationById(id);
    return isDraft(id)
      ? { id, title: 'New chat', cwd: draftCwd, draft: true }
      : {
          id,
          title: conversation ? titleOf(conversation) : id,
          cwd: conversation?.cwd,
          status: statuses[id] ?? 'idle',
        };
  });
  const activeIsDraft = isDraft(activeId);

  const select = (id: string) => {
    setActiveId(id);
    // Viewing a tab acknowledges background activity.
    setStatuses((current) => (current[id] === 'unread' ? { ...current, [id]: 'idle' } : current));
  };

  const close = (id: string) => {
    const index = ids.indexOf(id);
    const remaining = ids.filter((item) => item !== id);
    if (remaining.length === 0) {
      const draftId = `draft-${counterRef.current++}`;
      setIds([draftId]);
      setActiveId(draftId);
      return;
    }
    setIds(remaining);
    if (id === activeId) select(remaining[Math.min(index, remaining.length - 1)]);
  };

  const openNew = () => {
    const existing = ids.find(isDraft);
    if (existing) {
      setActiveId(existing);
      return;
    }
    const draftId = `draft-${counterRef.current++}`;
    setIds((current) => {
      const next = [...current];
      next.splice(current.indexOf(activeRef.current) + 1, 0, draftId);
      return next;
    });
    setActiveId(draftId);
  };

  const openConversation = (id: string) => {
    if (ids.includes(id)) {
      select(id);
      return;
    }
    setIds((current) => {
      const index = current.indexOf(activeRef.current);
      const next = [...current];
      next.splice(index + 1, 0, id);
      return next;
    });
    select(id);
  };

  const submit = () => {
    const text = (drafts[activeId] ?? '').trim();
    if (!text) return;
    let id = activeId;
    if (isDraft(id)) {
      // The draft becomes a real conversation in the same tab.
      id = `conv-new-${counterRef.current++}`;
      const now = new Date().toISOString();
      setConversations((current) => [
        {
          id,
          createdAt: now,
          updatedAt: now,
          messageCount: 1,
          summary: text.slice(0, 60),
          cwd: draftCwd,
        },
        ...current,
      ]);
      setIds((current) => current.map((item) => (item === activeId ? id : item)));
      setActiveId(id);
    }
    setDrafts((current) => ({ ...current, [activeId]: '' }));
    setTranscripts((current) => ({ ...current, [id]: [...(current[id] ?? []), user(text)] }));
    setStatuses((current) => ({ ...current, [id]: 'running' }));
    window.setTimeout(() => {
      setTranscripts((current) => ({
        ...current,
        [id]: [
          ...(current[id] ?? []),
          assistant('Done. Switch tabs while a turn runs to see background status.'),
        ],
      }));
      setStatuses((current) => ({
        ...current,
        [id]: activeRef.current === id ? 'idle' : 'unread',
      }));
    }, 4000);
  };

  useEffect(() => {
    const cycle = (event: KeyboardEvent) => {
      if (!event.altKey || event.ctrlKey || event.metaKey) return;
      const delta =
        event.code === 'BracketRight' ? 1 : event.code === 'BracketLeft' ? -1 : undefined;
      if (delta === undefined) return;
      event.preventDefault();
      const next = adjacentConversationTabId(tabs, activeRef.current, delta);
      if (next) select(next);
    };
    window.addEventListener('keydown', cycle);
    return () => window.removeEventListener('keydown', cycle);
  });

  return (
    <div className="flex h-screen bg-transparent">
      <section className="relative z-20 h-full w-[300px] shrink-0">
        <ChatSidebar
          activeConversationId={activeIsDraft ? null : activeId}
          authPrincipal={null}
          conversations={conversations.map((conversation) => ({
            ...conversation,
            isRunning: statuses[conversation.id] === 'running',
          }))}
          loading={false}
          onDeleteConversation={fn()}
          onForkConversation={fn()}
          onHide={fn()}
          onNewChat={openNew}
          onOpenAbout={fn()}
          onSearch={fn()}
          onSelectConversation={openConversation}
          searchActive={false}
        />
      </section>

      <main className="chat-main-panel relative flex h-full min-w-0 flex-1 flex-col overflow-hidden">
        <ConversationTabs
          activeId={activeId}
          onConfigOpen={(id) => {
            select(id);
            openConfig();
          }}
          onClose={close}
          onNew={openNew}
          onReorder={setIds}
          onSelect={select}
          tabs={tabs}
        />
        <div className="chat-main-scroll min-h-0 flex-1 overflow-y-auto" key={activeId}>
          <ChatTranscript
            isStreaming={statuses[activeId] === 'running'}
            messages={transcripts[activeId] ?? []}
          />
        </div>
        <ChatComposer
          addImageDisabled={false}
          attachments={[]}
          canStop={statuses[activeId] === 'running'}
          contextDisabled={false}
          contextIsStatic={!activeIsDraft}
          contextText="flair/claude-sonnet-4-6 · medium"
          dragActive={false}
          draft={drafts[activeId] ?? ''}
          onAttachImages={fn()}
          onContextOpen={activeIsDraft ? openConfig : fn()}
          onDraftChange={(value) => setDrafts((current) => ({ ...current, [activeId]: value }))}
          onDraftKeyDown={fn()}
          onDragLeave={fn()}
          onDragOver={fn()}
          onDrop={fn()}
          onPaste={fn()}
          onRemoveAttachment={fn()}
          onSelectSlashCommand={fn()}
          onStop={fn()}
          onSubmit={submit}
          placeholder={activeIsDraft ? 'Ask kodelet...' : 'Reply to kodelet...'}
          showStop={statuses[activeId] === 'running'}
          slashCommandIndex={-1}
          slashCommandSuggestions={[]}
          slashCommandSuggestionsOpen={false}
          slashUsageHint=""
          stopActionLabel="Stop"
          streamError={null}
          submitActionLabel="Send"
          submitDisabled={statuses[activeId] === 'running'}
          textareaDisabled={false}
        />
      </main>

      <aside
        aria-label="Workspace tools"
        className={cn('workspace-tools-shell', panelOpen && 'is-open')}
      >
        {panelOpen ? (
          <div className="workspace-tools-dock">
            <div className="workspace-tools-content">
              <GitDiffModal
                error={null}
                gitDiff={sampleGitDiff}
                loading={false}
                onRefresh={fn()}
                open
              />
            </div>
          </div>
        ) : null}
        <div className="workspace-tools-rail">
          <button
            aria-label={panelOpen ? 'Hide workspace panel' : 'Show workspace panel'}
            aria-pressed={panelOpen}
            className="sidebar-toggle-button workspace-tools-toggle"
            onClick={() => setPanelOpen((open) => !open)}
            type="button"
          >
            <PanelRight aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
          </button>
          {[
            { label: 'Terminal', Icon: SquareTerminal },
            { label: 'Changes', Icon: GitCompareArrows },
            { label: 'Browser', Icon: Globe },
          ].map(({ label, Icon }) => (
            <button
              aria-label={`Show ${label.toLowerCase()}`}
              className={cn(
                'sidebar-toggle-button',
                panelOpen && label === 'Changes' && 'is-active'
              )}
              key={label}
              onClick={() => setPanelOpen(true)}
              title={label}
              type="button"
            >
              <Icon aria-hidden="true" className="h-4 w-4" strokeWidth={1.9} />
            </button>
          ))}
        </div>
      </aside>

      {configOpen ? (
        <NewChatContextDialog
          {...newChatDialogStories.args}
          cwdQuery={cwdQuery}
          cwdSuggestionsOpen={cwdSuggestionsOpen}
          onCancel={() => setConfigOpen(false)}
          onCommit={() => {
            if (cwdQuery.trim()) setDraftCwd(cwdQuery.trim());
            setConfigOpen(false);
          }}
          onCwdInputBlur={() => setCwdSuggestionsOpen(false)}
          onCwdInputChange={setCwdQuery}
          onCwdInputFocus={() => setCwdSuggestionsOpen(true)}
          onSelectCwdSuggestion={(path) => {
            setCwdQuery(path);
            setCwdSuggestionsOpen(false);
          }}
        />
      ) : null}
    </div>
  );
};

/** Keeps the strip interactive in the isolated stories. */
const InteractiveTabs = (args: ComponentProps<typeof ConversationTabs>) => {
  const [tabs, setTabs] = useState(args.tabs);
  const [activeId, setActiveId] = useState(args.activeId);
  return (
    <ConversationTabs
      {...args}
      activeId={activeId}
      onClose={(id) => {
        args.onClose(id);
        setTabs((current) => current.filter((tab) => tab.id !== id));
      }}
      onReorder={(order) => {
        args.onReorder?.(order);
        setTabs((current) => order.flatMap((id) => current.find((tab) => tab.id === id) ?? []));
      }}
      onSelect={(id) => {
        args.onSelect(id);
        setActiveId(id);
      }}
      tabs={tabs}
    />
  );
};

const sampleTabs: ConversationTab[] = initialTabIds.map((id) => {
  const conversation = baseConversations.find((item) => item.id === id) as Conversation;
  return {
    id,
    title: titleOf(conversation),
    cwd: conversation.cwd,
    status: initialStatuses[id] ?? 'idle',
  };
});

const meta = {
  title: 'Chat/ConversationTabs',
  component: ConversationTabs,
  tags: ['autodocs'],
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: { component: DESIGN_NOTES },
      story: { inline: false, height: '720px' },
    },
  },
  decorators: [
    (Story, context) =>
      context.parameters.shell ? (
        <Story />
      ) : (
        <div className="chat-main-panel h-[26rem]">
          <Story />
        </div>
      ),
  ],
  render: (args) => <InteractiveTabs {...args} />,
  args: {
    activeId: 'conv-active',
    tabs: sampleTabs,
    onClose: fn(),
    onNew: fn(),
    onConfigOpen: fn(),
    onReorder: fn(),
    onSelect: fn(),
  },
} satisfies Meta<typeof ConversationTabs>;

export default meta;

type Story = StoryObj<typeof meta>;

/** Tabs in the chat page; try the sidebar, the composer and Alt+[ / ]. */
export const InChatPage: Story = {
  parameters: { shell: true },
  render: () => <ChatShell />,
};

/** With the workspace panel open the main panel narrows and tabs collapse into a switcher. */
export const WithWorkspacePanelOpen: Story = {
  parameters: { shell: true },
  render: () => <ChatShell workspacePanelOpen />,
};

/** Idle, running, new activity, needs input and failed. */
export const TabStates: Story = {
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    const tabs = canvas.getAllByRole('tab');
    await expect(tabs).toHaveLength(sampleTabs.length);
    await expect(canvas.getByRole('tab', { selected: true })).toHaveTextContent(
      'Extract chat UI into stories'
    );
  },
};

const openTabMenu = async (canvasElement: HTMLElement, title: string) => {
  const canvas = within(canvasElement);
  await userEvent.click(canvas.getByRole('button', { name: `${title} actions` }));
  await waitFor(() => expect(canvas.getByRole('menu', { name: `${title} actions` })).toBeVisible());
};

/** The ▾ on a tab replaces its close button: copy the ID or path, or close. */
export const TabMenu: Story = {
  play: async ({ canvasElement }) => {
    await openTabMenu(canvasElement, 'Extract chat UI into stories');
  },
};

/** A new chat has nothing to copy yet: its menu is just Config and Close tab. */
export const DraftTab: Story = {
  args: {
    activeId: 'draft-1',
    tabs: [
      ...sampleTabs.slice(0, 3),
      { id: 'draft-1', title: 'New chat', cwd: KODELET, draft: true },
    ],
  },
  play: async ({ canvasElement }) => {
    await openTabMenu(canvasElement, 'New chat');
  },
};

/** Many tabs scroll horizontally; the trailing button lists every tab with its status and workspace. */
export const Overflow: Story = {
  args: {
    activeId: 'tab-8',
    tabs: Array.from({ length: 14 }, (_, index) => ({
      id: `tab-${index}`,
      title: [
        'Extract chat UI into stories',
        'Review terminal session handling',
        'Document the plugin install flow',
        'Confirm the release rollout',
        'Fix flaky runner enrollment test',
        'Profile transcript rendering',
        'Audit backend tests',
      ][index % 7],
      cwd: index % 3 === 0 ? PLUGINS : KODELET,
      status: (['idle', 'running', 'idle', 'unread', 'idle', 'attention', 'idle'] as const)[
        index % 7
      ],
    })),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: 'All conversation tabs' }));
    await waitFor(() =>
      expect(canvas.getByRole('dialog', { name: 'Open conversations' })).toBeVisible()
    );
  },
};

/** Below ~34rem the strip becomes one switcher; the badge dot flags tabs needing attention. */
export const CompactSwitcher: Story = {
  decorators: [
    (Story) => (
      <div className="max-w-[26rem]">
        <Story />
      </div>
    ),
  ],
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.click(canvas.getByRole('button', { name: /Switch conversation tab/ }));
    await waitFor(() =>
      expect(canvas.getByRole('dialog', { name: 'Open conversations' })).toBeVisible()
    );
  },
};
