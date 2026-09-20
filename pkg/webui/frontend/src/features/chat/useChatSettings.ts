import type { RefObject } from 'react';
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react';
import apiService from '../../services/api';
import type { ChatSettings, Conversation, Runner } from '../../types';
import { showToast } from '../../utils';
import { useCWDSuggestions } from './useCWDSuggestions';

const DEFAULT_REASONING_EFFORT = 'medium';

interface ModelSettings {
  profile: string;
  model: string;
  modelOptions: string[];
  reasoningEffort: string;
  reasoningEffortOptions: string[];
  reasoningEffortExplicit: boolean;
}

export interface ChatContextSettings extends ModelSettings {
  runnerId: string;
  environmentProfile: string;
  cwd: string;
}

const modelSettingsFromChatSettings = (settings: Partial<ChatSettings>): ModelSettings => {
  const model = settings.model?.trim() || '';
  const reasoningEffort =
    typeof settings.reasoningEffort === 'string' && settings.reasoningEffort.trim()
      ? settings.reasoningEffort.trim().toLowerCase()
      : DEFAULT_REASONING_EFFORT;
  const reasoningEffortOptions = Array.from(
    new Set(
      (settings.reasoningEffortOptions || [])
        .map((option) => option.trim().toLowerCase())
        .filter(Boolean)
    )
  );
  if (!reasoningEffortOptions.includes(reasoningEffort)) {
    reasoningEffortOptions.push(reasoningEffort);
  }
  return {
    profile: settings.currentProfile?.trim() || '',
    model,
    modelOptions: Array.from(
      new Set(
        [model, ...(settings.modelOptions || [])].map((option) => option.trim()).filter(Boolean)
      )
    ),
    reasoningEffort,
    reasoningEffortOptions,
    reasoningEffortExplicit: false,
  };
};

const defaultContext = (settings: Partial<ChatSettings>, runnerId = ''): ChatContextSettings => ({
  ...modelSettingsFromChatSettings(settings),
  runnerId,
  environmentProfile: '',
  cwd: '',
});

const preserveReasoningEffort = (previous: ModelSettings, next: ModelSettings) => {
  const explicit =
    previous.reasoningEffortExplicit &&
    next.reasoningEffortOptions.includes(previous.reasoningEffort);
  return {
    reasoningEffort: explicit ? previous.reasoningEffort : next.reasoningEffort,
    reasoningEffortOptions: next.reasoningEffortOptions,
    reasoningEffortExplicit: explicit,
  };
};

interface ProfileModelOption {
  profile: string;
  model: string;
  settings?: ChatSettings;
}

const profileModelKey = (profile: string, model: string): string => `${profile}\u0000${model}`;

const appendProfileModelOptions = (
  options: ProfileModelOption[],
  profile: string,
  models: string[],
  settings?: ChatSettings
) => {
  for (const candidate of models) {
    const model = candidate.trim();
    if (!model || options.some((option) => option.profile === profile && option.model === model))
      continue;
    options.push({ profile, model, settings });
  }
};

interface ChatSettingsOptions {
  conversationId: string | null;
  viewedConversationIdRef: RefObject<string | null>;
  runners: Runner[];
}

// Selected context is committed configuration; draft context belongs to the
// dialog. Discovery only edits the draft until commit, never a running chat.
export const useChatSettings = ({
  conversationId,
  viewedConversationIdRef,
  runners,
}: ChatSettingsOptions) => {
  const [settings, setSettings] = useState<ChatSettings>({
    currentProfile: '',
    profiles: [],
    reasoningEffort: DEFAULT_REASONING_EFFORT,
    reasoningEffortOptions: [DEFAULT_REASONING_EFFORT],
  });
  const [loaded, setLoaded] = useState(false);
  const [selected, setSelected] = useState(() => defaultContext({}));
  const [draft, setDraft] = useState(() => defaultContext({}));
  const [dialogOpen, setDialogOpen] = useState(false);
  const [discovering, setDiscovering] = useState(false);
  const discoveryRequestRef = useRef(0);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  const cwd = useCWDSuggestions({
    conversationId,
    viewedConversationIdRef,
    open: dialogOpen,
    runnerId: draft.runnerId,
    environmentProfile: draft.environmentProfile,
    profile: draft.profile,
    runners,
  });
  const [catalog, setCatalog] = useState<{
    runnerId: string;
    options: ProfileModelOption[];
  } | null>(null);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const catalogRequestRef = useRef(0);

  useEffect(() => {
    // Bootstrap once; runner-specific settings are discovered in the dialog.
    void apiService
      .getChatSettings()
      .then((nextSettings) => {
        const context = defaultContext(nextSettings);
        if (!context.profile) throw new Error('Chat settings did not return a model profile');
        setSettings(nextSettings);
        setLoaded(true);
        // Preserve any runner choice made while bootstrap settings were loading.
        setSelected((current) => ({
          ...current,
          ...context,
          runnerId: current.runnerId,
          environmentProfile: current.environmentProfile,
        }));
        setDraft((current) => ({
          ...current,
          ...context,
          runnerId: current.runnerId,
          environmentProfile: current.environmentProfile,
        }));
        setDiscovering(false);
        cwd.setQuery('');
      })
      .catch((error) => {
        console.error('Failed to load chat settings', error);
        setLoaded(false);
      });
    return () => {
      discoveryRequestRef.current += 1;
    };
  }, [cwd.setQuery]);

  const defaultRunner = runners.find((runner) => runner.id === settings.defaultRunnerId);
  const defaultRunnerId =
    settings.defaultRunnerReady &&
    defaultRunner?.connected &&
    (defaultRunner.status === 'idle' ||
      (defaultRunner.status === 'busy' && defaultRunner.concurrentRuns))
      ? defaultRunner.id
      : '';

  useEffect(() => {
    if (conversationId || selected.runnerId || draft.runnerId || !defaultRunnerId) return;
    setSelected((current) => ({ ...current, runnerId: defaultRunnerId }));
    setDraft((current) => ({ ...current, runnerId: defaultRunnerId }));
  }, [conversationId, defaultRunnerId, selected.runnerId, draft.runnerId]);

  const discoverProfile = (
    profileName: string,
    runnerId = draft.runnerId,
    discoverProfiles = false
  ) => {
    const previous = draft;
    const requestId = ++discoveryRequestRef.current;
    setDraft((current) => ({
      ...current,
      ...(!discoverProfiles ? { profile: profileName } : {}),
      ...(profileName !== previous.profile || runnerId !== selected.runnerId
        ? { model: '', modelOptions: [] }
        : {}),
    }));
    setDiscovering(true);
    void apiService
      .getChatSettings(discoverProfiles ? undefined : profileName, runnerId || undefined)
      .then((nextSettings) => {
        if (discoveryRequestRef.current !== requestId) return;
        // A previously selected profile need not exist on the new runner.
        if (
          discoverProfiles &&
          profileName !== nextSettings.currentProfile &&
          nextSettings.profiles.some((profile) => profile.name === profileName)
        ) {
          return apiService.getChatSettings(profileName, runnerId || undefined);
        }
        return nextSettings;
      })
      .then((nextSettings) => {
        if (!nextSettings || discoveryRequestRef.current !== requestId) return;
        const next = modelSettingsFromChatSettings(nextSettings);
        if (!next.profile) throw new Error('Chat settings did not return a model profile');
        const preserveModel =
          next.profile === previous.profile &&
          runnerId === selected.runnerId &&
          next.modelOptions.includes(previous.model);
        setSettings((current) => ({ ...current, profiles: nextSettings.profiles }));
        setDraft((current) => ({
          ...current,
          ...next,
          model: preserveModel ? previous.model : next.model,
          ...preserveReasoningEffort(previous, next),
        }));
        setDiscovering(false);
      })
      .catch((error) => {
        if (discoveryRequestRef.current !== requestId) return;
        console.error('Failed to load profile reasoning settings', error);
        setDraft((current) => ({
          ...current,
          profile: previous.profile,
          model: discoverProfiles ? selected.model : previous.model,
          modelOptions: discoverProfiles ? selected.modelOptions : previous.modelOptions,
          reasoningEffort: previous.reasoningEffort,
          reasoningEffortOptions: previous.reasoningEffortOptions,
          reasoningEffortExplicit: previous.reasoningEffortExplicit,
          ...(discoverProfiles ? { runnerId: selected.runnerId } : {}),
        }));
        setDiscovering(false);
      });
  };

  const onDiscoverRunnerProfile = useEffectEvent((runnerId: string) => {
    discoverProfile(draft.profile, runnerId, true);
  });
  useEffect(() => {
    if (!dialogOpen || !draft.runnerId || !loaded) return;
    onDiscoverRunnerProfile(draft.runnerId);
    return () => {
      discoveryRequestRef.current += 1;
    };
  }, [dialogOpen, draft.runnerId, loaded]);

  const invalidateDiscovery = () => {
    discoveryRequestRef.current += 1;
    setDiscovering(false);
  };
  const rememberFocus = () => {
    returnFocusRef.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
  };
  const resetForNewChat = () => {
    rememberFocus();
    const context = defaultContext(settings, defaultRunnerId);
    setSelected(context);
    setDraft(context);
    invalidateDiscovery();
    cwd.reset('', '');
    setDialogOpen(true);
  };
  const openDialog = (profile = selected.profile) => {
    rememberFocus();
    setDraft({ ...selected, profile });
    invalidateDiscovery();
    cwd.setQuery(selected.runnerId ? selected.cwd : '');
    setDialogOpen(true);
  };
  const closeDialog = () => {
    invalidateDiscovery();
    setDraft({ ...selected, profile: selected.profile || settings.currentProfile || '' });
    cwd.reset(selected.cwd || settings.defaultCWD || '');
    setDialogOpen(false);
  };
  const interruptDialog = () => {
    invalidateDiscovery();
    cwd.cancel();
    setDialogOpen(false);
  };
  const commitDialog = (): ChatContextSettings | null => {
    if (discovering || !loaded || !draft.runnerId || !draft.profile.trim()) return null;
    const context = {
      ...draft,
      profile: draft.profile.trim(),
      environmentProfile: draft.environmentProfile.trim(),
      cwd: cwd.query.trim(),
    };
    setSelected(context);
    cwd.reset(cwd.query);
    setDialogOpen(false);
    return context;
  };

  // Like the TUI's /model picker: discover every visible profile's models.
  const loadModelCatalog = () => {
    if (!loaded) return;
    const runnerId = selected.runnerId;
    const requestId = ++catalogRequestRef.current;
    const profileNames = (settings.profiles || [])
      .filter((profile) => !profile.hidden)
      .map((profile) => profile.name.trim())
      .filter(Boolean);
    if (selected.profile && !profileNames.includes(selected.profile))
      profileNames.push(selected.profile);
    setCatalogLoading(true);
    void Promise.all(
      profileNames.map((name) => apiService.getChatSettings(name, runnerId || undefined))
    )
      .then((settingsList) => {
        if (catalogRequestRef.current !== requestId) return;
        const options: ProfileModelOption[] = [];
        settingsList.forEach((profileSettings, index) => {
          const modelSettings = modelSettingsFromChatSettings(profileSettings);
          appendProfileModelOptions(
            options,
            modelSettings.profile || profileNames[index],
            modelSettings.modelOptions,
            profileSettings
          );
        });
        setCatalog({ runnerId, options });
        setCatalogLoading(false);
      })
      .catch((error) => {
        if (catalogRequestRef.current !== requestId) return;
        console.error('Failed to load profile models', error);
        showToast('Failed to load models for the visible profiles', 'error');
        setCatalogLoading(false);
      });
  };
  const modelOptions = useMemo(() => {
    const options: ProfileModelOption[] = [];
    if (selected.model) appendProfileModelOptions(options, selected.profile, [selected.model]);
    appendProfileModelOptions(options, selected.profile, selected.modelOptions);
    if (catalog?.runnerId === selected.runnerId) {
      for (const option of catalog.options) {
        appendProfileModelOptions(options, option.profile, [option.model], option.settings);
      }
    }
    // Selection first, then higher versions first within a family.
    return options.sort((a, b) => {
      const aSelected = a.profile === selected.profile && a.model === selected.model;
      const bSelected = b.profile === selected.profile && b.model === selected.model;
      if (aSelected !== bSelected) return aSelected ? -1 : 1;
      return (
        b.model.localeCompare(a.model, 'en', { numeric: true }) ||
        a.profile.localeCompare(b.profile, 'en')
      );
    });
  }, [catalog, selected.model, selected.modelOptions, selected.profile, selected.runnerId]);

  const selectModel = (value: string): Partial<Conversation> | null => {
    const option = modelOptions.find(
      (candidate) => profileModelKey(candidate.profile, candidate.model) === value
    );
    if (!option) return null;
    const update: Partial<Conversation> = { model: option.model };
    const next: ChatContextSettings = { ...selected, model: option.model };
    if (option.profile !== selected.profile) {
      next.profile = option.profile;
      setDraft((current) => ({ ...current, profile: option.profile }));
      update.profile = option.profile;
      if (option.settings) {
        Object.assign(
          next,
          preserveReasoningEffort(selected, modelSettingsFromChatSettings(option.settings))
        );
        update.reasoningEffort = next.reasoningEffort;
      }
    }
    if (option.settings)
      next.modelOptions = modelSettingsFromChatSettings(option.settings).modelOptions;
    setSelected(next);
    return update;
  };
  const selectReasoningEffort = (effort: string): Partial<Conversation> => {
    setSelected((current) => ({
      ...current,
      reasoningEffort: effort,
      reasoningEffortExplicit: true,
    }));
    return { reasoningEffort: effort };
  };

  const configuredProfiles = settings.profiles || [];
  const availableProfiles =
    !draft.profile || configuredProfiles.some((profile) => profile.name === draft.profile)
      ? configuredProfiles
      : [
          ...configuredProfiles,
          { name: draft.profile, scope: conversationId ? 'conversation' : 'selected' },
        ];

  return {
    settings,
    loaded,
    selected,
    dialogOpen,
    resetForNewChat,
    openDialog,
    interruptDialog,
    commitDialog,
    selectModel,
    selectReasoningEffort,
    quickPick:
      loaded && selected.model
        ? {
            modelValue: profileModelKey(selected.profile, selected.model),
            modelOptions: modelOptions.map((option) => ({
              value: profileModelKey(option.profile, option.model),
              label: option.profile ? `${option.profile}/${option.model}` : option.model,
            })),
            modelOptionsLoading: catalogLoading,
            reasoningEffort: selected.reasoningEffort,
            reasoningEffortOptions: selected.reasoningEffortOptions,
            onModelMenuOpen: loadModelCatalog,
          }
        : undefined,
    dialogProps: {
      ...cwd.props,
      availableProfiles,
      profileDraft: draft.profile,
      modelDraft: draft.model,
      modelOptions: draft.modelOptions,
      reasoningEffortDraft: draft.reasoningEffort,
      reasoningEffortOptions: draft.reasoningEffortOptions,
      reasoningEffortLoading: discovering || !loaded,
      runners,
      runnerIdDraft: draft.runnerId,
      environmentProfileDraft: draft.environmentProfile,
      returnFocusRef,
      onCancel: closeDialog,
      onProfileDraftChange: discoverProfile,
      onModelDraftChange: (model: string) => setDraft((current) => ({ ...current, model })),
      onReasoningEffortDraftChange: (reasoningEffort: string) =>
        setDraft((current) => ({ ...current, reasoningEffort, reasoningEffortExplicit: true })),
      onRunnerDraftChange: (runnerId: string) => {
        setDraft((current) => ({
          ...current,
          runnerId,
          ...(runnerId !== current.runnerId ? { model: '', modelOptions: [] } : {}),
        }));
        cwd.reset('', '');
      },
      onEnvironmentProfileDraftChange: (environmentProfile: string) =>
        setDraft((current) => ({ ...current, environmentProfile })),
    },
  };
};
