import type { Meta, StoryObj } from '@storybook/react-vite';
import React from 'react';
import { expect, fn, userEvent, waitFor, within } from 'storybook/test';
import {
  sampleAttachment,
  sampleChatMessages,
  sampleConversations,
  sampleSlashCommands,
} from '../../stories/fixtures';
import type { UIWidgetEvent } from '../../types';
import { showToast } from '../../utils';
import ChatComposer from './ChatComposer';
import ChatSidebar from './ChatSidebar';
import ChatTranscript from './ChatTranscript';
import ExtensionWidgets from './ExtensionWidgets';

type ChatComposerStoryProps = React.ComponentProps<typeof ChatComposer> & {
  widgets: UIWidgetEvent[];
};

const InteractiveComposer = (args: ChatComposerStoryProps) => {
  const [draft, setDraft] = React.useState(args.draft);
  const [attachments, setAttachments] = React.useState(args.attachments);

  return (
    <ChatComposer
      {...args}
      attachments={attachments}
      draft={draft}
      onAttachImages={(files) => {
        void args.onAttachImages(files);
      }}
      onDraftChange={setDraft}
      onRemoveAttachment={(attachmentId) => {
        setAttachments((currentAttachments) =>
          currentAttachments.filter((attachment) => attachment.id !== attachmentId)
        );
        args.onRemoveAttachment(attachmentId);
      }}
    />
  );
};

const meta = {
  title: 'Chat/ChatComposer',
  component: ChatComposer,
  render: (args) => <InteractiveComposer {...args} />,
  play: async ({ args, canvasElement }) => {
    await canvasElement.ownerDocument.fonts.ready;
    const canvas = within(canvasElement);
    await waitFor(() => {
      const editor = canvas.getByTestId('composer-textarea');
      const leading = canvas.getByRole('button', { name: 'Add image' }).getBoundingClientRect();
      const submit = canvas
        .getByRole('button', { name: args.submitActionLabel })
        .getBoundingClientRect();
      expect(Math.abs(leading.y - submit.y)).toBeLessThan(1);
      expect(Math.abs(leading.height - submit.height)).toBeLessThan(1);

      if (window.matchMedia('(max-width: 600px)').matches) {
        const editorRect = editor.getBoundingClientRect();
        expect(Math.abs(editorRect.left - leading.left)).toBeLessThan(1);
        expect(Math.abs(editorRect.right - submit.right)).toBeLessThan(1);
        expect(editorRect.bottom).toBeLessThanOrEqual(submit.top);
        expect(leading.height).toBeGreaterThanOrEqual(44);
        if (!args.draft) {
          expect(editor.scrollHeight).toBeLessThanOrEqual(editor.clientHeight + 1);
        }
      } else if (!editor.parentElement?.classList.contains('is-multiline')) {
        const styles = getComputedStyle(editor);
        const textCenter =
          editor.getBoundingClientRect().top +
          Number.parseFloat(styles.paddingTop) +
          Number.parseFloat(styles.lineHeight) / 2;
        expect(Math.abs(textCenter - (submit.y + submit.height / 2))).toBeLessThan(1);
      }
    });
  },
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    addImageDisabled: false,
    attachments: [],
    canStop: false,
    contextDisabled: false,
    contextIsStatic: false,
    contextText: 'default · kodelet',
    dragActive: false,
    draft: 'Extract the reusable component and add a story.',
    placeholder: 'Ask kodelet anything...',
    showStop: false,
    slashCommandIndex: -1,
    slashCommandSuggestions: [],
    slashCommandSuggestionsOpen: false,
    slashUsageHint: '',
    stopActionLabel: 'Stop',
    streamError: null,
    submitActionLabel: 'Send',
    submitDisabled: false,
    textareaDisabled: false,
    widgets: [],
    onAttachImages: fn(),
    onContextOpen: fn(),
    onDragLeave: fn(),
    onDragOver: fn(),
    onDrop: fn(),
    onDraftChange: fn(),
    onDraftKeyDown: fn(),
    onPaste: fn(),
    onRemoveAttachment: fn(),
    onSelectSlashCommand: fn(),
    onStop: fn(),
    onSubmit: fn(),
  },
} satisfies Meta<ChatComposerStoryProps>;

export default meta;

type Story = StoryObj<typeof meta>;

export const ReadyToSend: Story = {};

export const Multiline: Story = {
  args: {
    draft: 'Review these points:\n- mobile layout\n- terminal behavior',
  },
};

export const WithSlashSuggestions: Story = {
  args: {
    draft: '/re',
    slashCommandIndex: 0,
    slashCommandSuggestions: sampleSlashCommands,
    slashCommandSuggestionsOpen: true,
    slashUsageHint: '/review frontend extraction',
  },
};

export const SteeringActiveConversation: Story = {
  args: {
    contextIsStatic: true,
    contextText: 'code-review · /home/jingkaihe/workspace/kodelet',
    draft: 'Focus the review on the extracted components.',
    showStop: true,
    canStop: true,
    stopActionLabel: 'Stop',
    submitActionLabel: 'Steer',
  },
};

export const RepeatedPunctuation: Story = {
  args: {
    ...SteeringActiveConversation.args,
    contextText: 'deep · effort:xhigh · /home/jingkaihe/workspace/kodelet',
    draft: '',
    placeholder: 'Steer the active conversation…',
  },
  play: async (context) => {
    await meta.play(context);
    const canvas = within(context.canvasElement);
    const editor = canvas.getByTestId<HTMLTextAreaElement>('composer-textarea');
    const text = '... ............ >= -> => != <= :: ';

    // Type rather than assign the value so browser screenshots exercise incremental glyph painting.
    await userEvent.type(editor, text);

    expect(editor).toHaveValue(text);
    expect(editor).toHaveFocus();
    expect(editor.selectionStart).toBe(text.length);
    expect(editor.selectionEnd).toBe(text.length);
    expect(getComputedStyle(editor).fontVariantLigatures).toBe('no-contextual');
  },
};

export const ErrorWithAttachment: Story = {
  args: {
    attachments: [sampleAttachment],
    draft: '',
    streamError: 'Failed to send message',
    submitDisabled: false,
  },
};

export const InWorkspace: Story = {
  args: {
    ...SteeringActiveConversation.args,
    draft: '',
    placeholder: 'Steer the active conversation…',
    submitDisabled: true,
  },
  render: (args) => (
    <div className="flex h-dvh">
      <div className="hidden w-80 shrink-0 border-r border-black/10 lg:block">
        <ChatSidebar
          activeConversationId="conv-active"
          authPrincipal={{
            id: 'https://issuer.example.com|jingkai-he',
            issuer: 'https://issuer.example.com',
            subject: 'jingkai-he',
            name: 'Jingkai He',
            roles: ['user'],
          }}
          conversations={[
            ...sampleConversations,
            {
              ...sampleConversations[0],
              id: 'child-review',
              summary: 'Review sidebar changes',
              isRunning: true,
              metadata: { parent_conversation_id: 'conv-active' },
            },
          ]}
          loading={false}
          onDeleteConversation={fn()}
          onForkConversation={fn()}
          onHide={fn()}
          onNewChat={fn()}
          onSearch={fn()}
          onSelectConversation={fn()}
        />
      </div>
      <main className="chat-main-panel flex min-w-0 flex-1 flex-col overflow-hidden">
        <div className="chat-main-scroll min-h-0 flex-1 overflow-y-auto">
          <ChatTranscript
            isStreaming={false}
            messages={[
              {
                role: 'user',
                content: 'Please make the composer and sidebar match the transcript.',
              },
              sampleChatMessages[1],
            ]}
          />
        </div>
        <ExtensionWidgets placement="aboveComposer" widgets={args.widgets} />
        <InteractiveComposer {...args} />
        <ExtensionWidgets placement="belowComposer" widgets={args.widgets} />
      </main>
    </div>
  ),
};

export const WithExtensionFeedback: Story = {
  ...InWorkspace,
  args: {
    ...InWorkspace.args,
    widgets: [
      {
        key: 'background-agents',
        extension_id: 'subagent',
        id: 'background-agents',
        frame: {
          sequence: 1,
          lines: [
            {
              spans: [
                { text: 'Background agents', style: { bold: true } },
                { text: '  1 active · 1 complete', style: { dim: true } },
              ],
            },
            {
              spans: [
                { text: '› ', style: { foreground: 'cyan' } },
                { text: 'Reviewing authentication', style: { foreground: 'cyan' } },
                { text: '  running', style: { dim: true } },
              ],
            },
            {
              spans: [
                { text: '✓ ', style: { foreground: 'green' } },
                { text: 'Checking frontend tests' },
                { text: '  complete', style: { dim: true } },
              ],
            },
          ],
        },
      },
      {
        key: 'workspace',
        extension_id: 'workspace',
        id: 'workspace-status',
        placement: 'belowComposer',
        frame: { sequence: 1, lines: ['Workspace ready · 2 extensions connected'] },
      },
    ],
  },
  play: async (context) => {
    await meta.play(context);
    const canvas = within(context.canvasElement);
    const toggle = canvas.getByRole('button', { name: /Background agents/ });
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await userEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(canvas.queryByText('Reviewing authentication')).not.toBeInTheDocument();
    await userEvent.keyboard('{Enter}');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(canvas.getByText('Reviewing authentication')).toBeVisible();
    expect(canvas.queryByRole('button', { name: /Workspace ready/ })).not.toBeInTheDocument();
    showToast('Ready to help. 2 extensions connected.', 'info', 'Workspace extension ready');
  },
};
