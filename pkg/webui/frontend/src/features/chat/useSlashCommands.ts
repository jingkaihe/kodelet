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
  discoveryAvailable: boolean;
  runnerId: string;
  generation?: number;
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
  discoveryAvailable,
  runnerId,
  generation,
  conversationId,
  cwd,
  environmentProfile,
  profile,
  onSubmit,
}: SlashCommandsOptions) => {
  const [commands, setCommands] = useState<SlashCommandOption[]>([]);
  const [index, setIndex] = useState(-1);
  const [dismissedDraft, setDismissedDraft] = useState<string | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies(generation): Reconnected runners must rediscover commands even when their workspace is unchanged.
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
  }, [discoveryAvailable, runnerId, generation, conversationId, cwd, environmentProfile, profile]);

  const suggestions = useMemo(() => filterCommands(commands, draft), [commands, draft]);
  const open = !disabled && dismissedDraft !== draft && suggestions.length > 0;
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
    placeholder,
    composerProps: {
      slashCommandIndex: index,
      slashCommandSuggestions: suggestions,
      slashCommandSuggestionsOpen: open,
      slashUsageHint: disabled ? '' : placeholder,
      onDraftKeyDown: onKeyDown,
      onSelectSlashCommand: select,
    },
  };
};
