// Package tui implements Kodelet's native terminal chat interface.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/pkg/errors"
)

var newDefaultChatRunner = func(defaultCWD string, extensionRuntimes chat.ExtensionRuntimeProvider) chat.ChatRunner {
	return chat.NewDefaultChatRunner(defaultCWD, extensionRuntimes)
}

func Run(ctx context.Context, config Config) error {
	theme, err := resolveTheme(config.Theme)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.ConversationID) == "" && !config.Remote {
		if _, _, err := resolveReasoningSettings(displayProfile(config.Profile), config.ReasoningEffort); err != nil {
			return errors.Wrap(err, "invalid reasoning effort configuration")
		}
	}
	applyTheme(theme)

	initialModel := newModel(ctx, config)
	if initialModel.extensionRuntimes != nil {
		defer func() {
			_ = initialModel.extensionRuntimes.Close()
		}()
	}
	if closer, ok := initialModel.runner.(interface{ Close() error }); ok {
		defer func() {
			_ = closer.Close()
		}()
	}

	program := tea.NewProgram(initialModel, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	initialModel.cancel()
	final, isModel := finalModel.(model)
	// Cleared here so signal-driven exits also reset the title.
	if isModel && final.terminalTitleWritten {
		fmt.Fprint(os.Stdout, xansi.SetWindowTitle(""))
	}
	if err != nil {
		return err
	}
	if isModel {
		if summary := renderExitSummary(final.conversationID, final.usage); summary != "" {
			fmt.Fprintln(os.Stdout, summary)
		}
	}
	return err
}

func newModel(ctx context.Context, config Config) model {
	mctx, cancel := context.WithCancel(ctx)
	runCh := make(chan tea.Msg, 256)
	mctx = extensions.ContextWithDiagnosticSink(mctx, newTUIDiagnosticSink(runCh))
	extensionUI := newTUIExtensionUIHost(runCh, mctx.Done())
	mctx = extensions.ContextWithExtensionUIHost(mctx, extensionUI)
	mctx = extensions.ContextWithUIInputBroker(mctx, newTUIUIBroker(runCh, 0))
	extensionRuntimes := extensions.NewRuntimeManager()
	themeSelection := normalizedThemeSelection(config.Theme)
	theme, err := resolveTheme(themeSelection)
	if err != nil {
		theme = themes[DefaultThemeName]
		themeSelection = DefaultThemeName
	}
	applyTheme(theme)

	ta := textarea.New()
	ta.Placeholder = "Ask kodelet..."
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	applyThemeToTextarea(&ta)
	ta.SetHeight(inputHeight)
	ta.Focus()

	vp := viewport.New(80, 20)
	vp.MouseWheelEnabled = true

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	runner := config.Runner
	if runner == nil {
		// The TUI sends --cwd as a per-request override below; leave the runner
		// default empty so relative overrides resolve against the process cwd.
		runner = newDefaultChatRunner("", extensionRuntimes)
	}
	conversationSource, _ := runner.(chat.ConversationSource)
	requestedCWD := strings.TrimSpace(config.CWD)
	cwd := requestedCWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if config.Remote {
		requestedCWD = ""
	}
	messageHistoryStore, _ := messagehistory.NewStore()
	conversationID := strings.TrimSpace(config.ConversationID)
	conversationWasResumed := conversationID != ""
	initialHistoryPending := conversationID != ""
	var messageHistoryScopeCWD string
	if !initialHistoryPending && !config.Remote {
		messageHistoryScopeCWD, _ = messagehistory.ResolveScopeCWD(cwd)
	}
	profile := displayProfile(config.Profile)
	profileOptionsInput := config.ProfileOptions
	if len(profileOptionsInput) == 0 {
		if config.Remote {
			profileOptionsInput = []string{profile}
		} else {
			profileOptionsInput = loadProfileOptions()
		}
	}
	profileOptions := normalizeProfileOptions(profileOptionsInput, profile)
	reasoningEffort := strings.TrimSpace(config.ReasoningEffort)
	reasoningEffortOptions := append([]string(nil), config.ReasoningEffortOptions...)
	if config.Remote {
		if settings, ok := profileSettingsFor(config.ProfileSettings, profile); ok {
			if reasoningEffort == "" {
				reasoningEffort = settings.ReasoningEffort
			}
			if len(reasoningEffortOptions) == 0 {
				reasoningEffortOptions = append([]string(nil), settings.ReasoningEffortOptions...)
			}
		}
	} else {
		if resolvedEffort, resolvedOptions, err := resolveReasoningSettings(profile, reasoningEffort); err == nil {
			reasoningEffort = resolvedEffort
			if len(reasoningEffortOptions) == 0 {
				reasoningEffortOptions = resolvedOptions
			}
		}
	}
	reasoningEffort = normalizeReasoningEffort(reasoningEffort)
	reasoningEffortOptions = normalizeReasoningEffortOptions(reasoningEffortOptions, reasoningEffort)
	var defaultSlashCommands []slashcommands.Command
	if config.Remote {
		defaultSlashCommands = withTUIBuiltInSlashCommands(nil)
	}
	defaults := conversationDefaults{
		profile:                 profile,
		profileOptions:          append([]string(nil), profileOptions...),
		reasoningEffort:         reasoningEffort,
		reasoningEffortOptions:  append([]string(nil), reasoningEffortOptions...),
		reasoningEffortExplicit: config.ReasoningEffortExplicit,
		cwd:                     cwd,
		requestedCWD:            requestedCWD,
		slashCommands:           defaultSlashCommands,
	}
	conversationKey := conversationID
	if conversationKey == "" {
		conversationKey = "new:1"
	}
	conversation := newConversationState(conversationKey, conversationID, conversationWasResumed, defaults)
	conversation.initialHistoryPending = initialHistoryPending
	conversation.messageHistoryScopeCWD = messageHistoryScopeCWD
	conversation.extensionDiscoveryBlocked = config.Remote

	return model{
		conversationState:     conversation,
		ctx:                   mctx,
		cancel:                cancel,
		runner:                runner,
		conversationSource:    conversationSource,
		remote:                config.Remote,
		environmentProfile:    strings.TrimSpace(config.EnvironmentProfile),
		profileSettings:       cloneProfileSettings(config.ProfileSettings),
		extensionRuntimes:     extensionRuntimes,
		extensionUI:           extensionUI,
		extensionWidgets:      map[extensionUIKey]tuiExtensionWidget{},
		widgetOrder:           []extensionUIKey{},
		collapsedWidgets:      map[extensionUIKey]bool{},
		widgetOffsets:         map[extensionWidgetOffsetKey]int{},
		extensionSurfaces:     map[extensionUIKey]tuiExtensionSurface{},
		conversations:         map[string]*conversationState{conversationKey: conversation},
		activeConversationKey: conversationKey,
		nextConversationKey:   1,
		conversationDefaults:  defaults,
		messageHistoryStore:   messageHistoryStore,
		theme:                 theme,
		themeSelection:        themeSelection,
		viewport:              vp,
		textarea:              ta,
		spinner:               sp,
		runs:                  map[int]*conversationRun{},
		runByState:            map[string]int{},
		shortcutCalls:         map[int]*extensionShortcutCall{},
		runCh:                 runCh,
		terminalTitleEpoch:    time.Now(),
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		m.spinner.Tick,
		waitForMsg(m.runCh),
		loadConversationHistoryFromSource(m.ctx, m.activeConversationKey, m.conversationID, m.requestedCWD, m.conversationSource),
	}
	if !m.remote {
		cmds = append(cmds, loadMessageHistoryForConversation(m.ctx, m.activeConversationKey, m.messageHistoryStore, m.messageHistoryScopeCWD))
	}
	if !m.initialHistoryPending && !m.remote {
		cmds = append(cmds, loadSlashCommandsForConversation(m.ctx, m.activeConversationKey, m.slashCommandCWD()))
	}
	return tea.Batch(cmds...)
}

func loadSlashCommands(ctx context.Context, cwd string) tea.Cmd {
	return loadSlashCommandsForConversation(ctx, "", cwd)
}

func loadSlashCommandsForConversation(ctx context.Context, conversationKey, cwd string) tea.Cmd {
	return func() tea.Msg {
		commands, err := listBaseSlashCommands(ctx, cwd)
		return slashCommandsMsg{conversationKey: conversationKey, cwd: strings.TrimSpace(cwd), commands: commands, err: err}
	}
}

func loadExtensionSlashCommands(ctx context.Context, cwd string, runtimeManager *extensions.RuntimeManager) tea.Cmd {
	return loadExtensionSlashCommandsForConversation(ctx, "", cwd, runtimeManager)
}

func loadExtensionSlashCommandsForConversation(ctx context.Context, conversationKey, cwd string, runtimeManager *extensions.RuntimeManager) tea.Cmd {
	return func() tea.Msg {
		commands, shortcuts, err := listExtensionResources(ctx, cwd, runtimeManager)
		return slashCommandsMsg{
			conversationKey: conversationKey,
			cwd:             strings.TrimSpace(cwd),
			commands:        commands,
			shortcuts:       shortcuts,
			extensionsOnly:  true,
			err:             err,
		}
	}
}

func startExtensionLifecycleForConversation(ctx context.Context, conversationKey, cwd, conversationID, provider, model, profile string, runtimeManager *extensions.RuntimeManager) tea.Cmd {
	return func() tea.Msg {
		resolvedCWD, err := resolveSlashCommandCWD(cwd)
		if err == nil {
			_, err = runtimeManager.RuntimeWithCallContext(ctx, resolvedCWD, extensions.ExtensionCallContext{
				ConversationID: strings.TrimSpace(conversationID),
				CWD:            resolvedCWD,
				Provider:       strings.TrimSpace(provider),
				Model:          strings.TrimSpace(model),
				Profile:        strings.TrimSpace(profile),
				InvokedBy:      "main",
			})
		}
		return extensionLifecycleMsg{conversationKey: conversationKey, conversationID: strings.TrimSpace(conversationID), cwd: resolvedCWD, err: err}
	}
}

func closeTUIBrokerAfter(cmd tea.Cmd, broker *tuiUIBroker) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		if broker != nil {
			defer broker.close()
		}
		return cmd()
	}
}

func listBaseSlashCommands(ctx context.Context, cwd string) ([]slashcommands.Command, error) {
	resolvedCWD, err := resolveSlashCommandCWD(cwd)
	if err != nil {
		return withTUIBuiltInSlashCommands(slashcommands.BuiltIns()), err
	}

	processor, err := fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(resolvedCWD))
	if err != nil {
		return withTUIBuiltInSlashCommands(slashcommands.BuiltIns()), errors.Wrap(err, "failed to initialize slash commands")
	}

	return withTUIBuiltInSlashCommands(slashcommands.List(ctx, processor)), nil
}

func listExtensionSlashCommands(ctx context.Context, cwd string, runtimeManager *extensions.RuntimeManager) ([]slashcommands.Command, error) {
	commands, _, err := listExtensionResources(ctx, cwd, runtimeManager)
	return commands, err
}

type extensionResourceRuntimeProvider interface {
	RuntimeForCommandDiscovery(context.Context, string) (*extensions.Runtime, error)
}

func listExtensionResources(ctx context.Context, cwd string, runtimeManager extensionResourceRuntimeProvider) ([]slashcommands.Command, []extensions.Shortcut, error) {
	resolvedCWD, err := resolveSlashCommandCWD(cwd)
	if err != nil {
		return nil, nil, err
	}

	discoveryCtx, cancelDiscovery := context.WithCancel(ctx)
	defer cancelDiscovery()
	extensionRuntime, err := runtimeManager.RuntimeForCommandDiscovery(discoveryCtx, resolvedCWD)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to initialize extensions for interactive resources")
	}
	if extensionRuntime == nil {
		return nil, nil, nil
	}

	commands := extensionRuntime.SlashCommands()
	shortcuts := extensionRuntime.Shortcuts()
	return commands, shortcuts, nil
}

func resolveSlashCommandCWD(cwd string) (string, error) {
	defaultCWD, err := chat.ResolveConfiguredDefaultCWD("")
	if err != nil {
		return "", err
	}

	expandedCWD, err := chat.ExpandCWDInput(cwd, defaultCWD)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(expandedCWD) == "" {
		expandedCWD = defaultCWD
	}

	return conversations.NormalizeCWD(expandedCWD)
}

func (m model) slashCommandCWD() string {
	return slashCommandCWDForState(m.conversationState)
}

func slashCommandCWDForState(state *conversationState) string {
	if state == nil {
		return ""
	}
	if strings.TrimSpace(state.requestedCWD) != "" {
		return state.requestedCWD
	}
	return state.cwd
}
