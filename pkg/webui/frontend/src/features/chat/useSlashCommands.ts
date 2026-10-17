import {
  type Dispatch,
  type KeyboardEvent,
  type SetStateAction,
  useEffect,
  useMemo,
  useState,
} from 'react';
import apiService from '../../services/api';
import type { SlashCommandOption } from '../../types';

interface SlashCommandsOptions {
  draft: string;
  setDraft: Dispatch<SetStateAction<string>>;
  disabled: boolean;
  dictationEnabled: boolean;
  realtimeEnabled?: boolean;
  discoveryAvailable: boolean;
  runnerId: string;
  generation?: number;
  refreshVersion?: number;
  conversationId?: string;
  cwd: string;
  environmentProfile: string;
  profile?: string;
  onSubmit: () => Promise<void>;
}

const filterCommands = (commands: SlashCommandOption[], draft: string): SlashCommandOption[] => {
  const trimmed = draft.trimStart();
  if (!trimmed.startsWith('/')) return [];
  const query = trimmed.slice(1).toLowerCase();
  if (query.includes(' ')) return [];
  return commands.filter(
    (command) =>
      !query ||
      command.name.toLowerCase().includes(query) ||
      command.description.toLowerCase().includes(query)
  );
};

export const useSlashCommands = ({
  draft,
  setDraft,
  disabled,
  dictationEnabled,
  realtimeEnabled = false,
  discoveryAvailable,
  runnerId,
  generation,
  refreshVersion = 0,
  conversationId,
  cwd,
  environmentProfile,
  profile,
  onSubmit,
}: SlashCommandsOptions) => {
  const [remoteCommands, setCommands] = useState<SlashCommandOption[]>([]);
  const localCommands = useMemo<SlashCommandOption[]>(
    () => [
      ...(dictationEnabled
        ? [
            {
              name: 'voice',
              description: 'Turn your voice into text',
              placeholder: 'Turn your voice into text',
            },
          ]
        : []),
      ...(realtimeEnabled
        ? [
            {
              name: 'talk',
              description: 'Chat to Kodelet',
              placeholder: 'Chat to Kodelet',
            },
          ]
        : []),
    ],
    [dictationEnabled, realtimeEnabled]
  );
  const commands = useMemo(
    () => [
      ...remoteCommands.filter(
        (command) => !localCommands.some((local) => local.name === command.name)
      ),
      ...localCommands,
    ],
    [remoteCommands, localCommands]
  );
  const [index, setIndex] = useState(-1);
  const [dismissedDraft, setDismissedDraft] = useState<string | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies(generation): Reconnected runners must rediscover commands even when their workspace is unchanged.
  // biome-ignore lint/correctness/useExhaustiveDependencies(refreshVersion): Reloaded resources must replace command discovery even when the target is unchanged.
  useEffect(() => {
    setCommands([]);
    if (!discoveryAvailable) return;
    let cancelled = false;
    void apiService
      .getSlashCommands(conversationId ? undefined : cwd || undefined, {
        runnerId,
        conversationId,
        environmentProfile,
        profile,
      })
      .then((response) => {
        if (!cancelled) setCommands(response.commands || []);
      })
      .catch((error) => {
        if (!cancelled) console.error('Failed to load slash commands', error);
      });
    return () => {
      cancelled = true;
    };
  }, [
    discoveryAvailable,
    runnerId,
    generation,
    refreshVersion,
    conversationId,
    cwd,
    environmentProfile,
    profile,
  ]);

  const suggestions = useMemo(
    () => filterCommands(disabled ? localCommands : commands, draft),
    [commands, draft, disabled, localCommands]
  );
  const open = dismissedDraft !== draft && suggestions.length > 0;
  const trimmedDraft = draft.trimStart();
  const draftCommand = trimmedDraft.startsWith('/')
    ? trimmedDraft.slice(1).split(/\s+/, 1)[0]
    : null;
  const activeCommand =
    (open ? suggestions[index] : undefined) ||
    commands.find((command) => command.name === draftCommand);
  const placeholder = activeCommand
    ? activeCommand.placeholder ||
      `/${activeCommand.name}${activeCommand.hint ? ` ${activeCommand.hint}` : ''}`
    : '';

  // biome-ignore lint/correctness/useExhaustiveDependencies(commands): Discovery resets selection even when the draft is unchanged.
  useEffect(() => {
    setIndex(-1);
    setDismissedDraft((current) => (current && current !== draft ? null : current));
  }, [draft, commands]);

  const select = (name: string) => {
    setDraft((current) => `${current.match(/^\s*/)?.[0] || ''}/${name} `);
    setIndex(-1);
    setDismissedDraft(null);
  };

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (open) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        setIndex((current) =>
          event.key === 'ArrowDown'
            ? current >= suggestions.length - 1
              ? -1
              : current + 1
            : current < 0
              ? suggestions.length - 1
              : current <= 0
                ? -1
                : current - 1
        );
        return;
      }
      if (event.key === 'Tab' || event.key === 'Enter') {
        event.preventDefault();
        const command = suggestions[index >= 0 ? index : 0] || suggestions[0];
        if (command) select(command.name);
        return;
      }
      if (event.key === 'Escape') {
        event.preventDefault();
        setIndex(-1);
        setDismissedDraft(draft);
        return;
      }
    }
    if (event.key === 'Enter' && event.shiftKey) {
      event.preventDefault();
      void onSubmit();
    }
  };

  return {
    commands,
    placeholder,
    composerProps: {
      slashCommandIndex: index,
      slashCommandSuggestions: suggestions,
      slashCommandSuggestionsOpen: open,
      slashUsageHint:
        disabled && !localCommands.some((command) => command.name === activeCommand?.name)
          ? ''
          : placeholder,
      onDraftKeyDown: onKeyDown,
      onSelectSlashCommand: select,
    },
  };
};
