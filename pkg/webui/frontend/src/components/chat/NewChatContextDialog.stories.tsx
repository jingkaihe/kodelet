import React from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { fn } from 'storybook/test';
import NewChatContextDialog from './NewChatContextDialog';
import { sampleCwdHints, sampleProfiles } from '../../stories/fixtures';

type NewChatContextDialogStoryProps = React.ComponentProps<typeof NewChatContextDialog>;

const InteractiveDialog = (args: NewChatContextDialogStoryProps) => {
  const [profileDraft, setProfileDraft] = React.useState(args.profileDraft);
  const [reasoningEffortDraft, setReasoningEffortDraft] = React.useState(args.reasoningEffortDraft);
  const [cwdQuery, setCwdQuery] = React.useState(args.cwdQuery);
  const [cwdSuggestionsOpen, setCwdSuggestionsOpen] = React.useState(args.cwdSuggestionsOpen);
  const [runnerIdDraft, setRunnerIdDraft] = React.useState(args.runnerIdDraft);

  return (
    <NewChatContextDialog
      {...args}
      cwdQuery={cwdQuery}
      cwdSuggestionsOpen={cwdSuggestionsOpen}
      profileDraft={profileDraft}
      reasoningEffortDraft={reasoningEffortDraft}
      runnerIdDraft={runnerIdDraft}
      onRunnerDraftChange={(runnerId) => {
        setRunnerIdDraft(runnerId);
        args.onRunnerDraftChange(runnerId);
      }}
      onCwdInputBlur={() => {
        setCwdSuggestionsOpen(false);
        args.onCwdInputBlur();
      }}
      onCwdInputChange={(value) => {
        setCwdQuery(value);
        setCwdSuggestionsOpen(value.trim().length > 0);
        args.onCwdInputChange(value);
      }}
      onCwdInputFocus={() => {
        setCwdSuggestionsOpen(args.cwdSuggestions.length > 0);
        args.onCwdInputFocus();
      }}
      onProfileDraftChange={(profileName) => {
        setProfileDraft(profileName);
        args.onProfileDraftChange(profileName);
      }}
      onReasoningEffortDraftChange={(reasoningEffort) => {
        setReasoningEffortDraft(reasoningEffort);
        args.onReasoningEffortDraftChange(reasoningEffort);
      }}
      onSelectCwdSuggestion={(path) => {
        setCwdQuery(path);
        setCwdSuggestionsOpen(false);
        args.onSelectCwdSuggestion(path);
      }}
    />
  );
};

const meta = {
  title: 'Chat/NewChatContextDialog',
  component: NewChatContextDialog,
  render: (args) => <InteractiveDialog {...args} />,
  parameters: {
    layout: 'fullscreen',
  },
  args: {
    availableProfiles: sampleProfiles,
    cwdQuery: '/home/jingkaihe/workspace/kodelet',
    cwdSuggestionIndex: 0,
    cwdSuggestions: sampleCwdHints,
    cwdSuggestionsOpen: true,
    profileDraft: 'default',
    reasoningEffortDraft: 'medium',
    reasoningEffortLoading: false,
    reasoningEffortOptions: ['low', 'medium', 'high'],
    runners: [{
      id: 'runner-1',
      host: { instanceId: 'host-1', hostname: 'worker', os: 'linux', arch: 'amd64' },
      workspace: { path: '/home/jingkaihe/workspace/kodelet', name: 'kodelet' },
      manifestChanged: false,
      status: 'idle',
      connected: true,
      generation: 1,
    }],
    runnerIdDraft: 'runner-1',
    environmentProfileDraft: '',
    onCancel: fn(),
    onCommit: fn(),
    onCwdInputBlur: fn(),
    onCwdInputChange: fn(),
    onCwdInputFocus: fn(),
    onCwdInputKeyDown: fn(),
    onProfileDraftChange: fn(),
    onReasoningEffortDraftChange: fn(),
    onRunnerDraftChange: fn(),
    onEnvironmentProfileDraftChange: fn(),
    onSelectCwdSuggestion: fn(),
  },
} satisfies Meta<typeof NewChatContextDialog>;

export default meta;

type Story = StoryObj<typeof meta>;

export const WithSuggestions: Story = {};

export const Compact: Story = {
  args: {
    cwdQuery: '',
    cwdSuggestions: [],
    cwdSuggestionsOpen: false,
  },
};

export const RunnerRequired: Story = {
  args: { runnerIdDraft: '' },
};
