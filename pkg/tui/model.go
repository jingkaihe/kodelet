// Package tui implements Kodelet's native terminal chat interface.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	chat "github.com/jingkaihe/kodelet/pkg/chat"
	"github.com/jingkaihe/kodelet/pkg/conversations"
	"github.com/jingkaihe/kodelet/pkg/extensions"
	"github.com/jingkaihe/kodelet/pkg/fragments"
	"github.com/jingkaihe/kodelet/pkg/messagehistory"
	"github.com/jingkaihe/kodelet/pkg/runner/protocol"
	"github.com/jingkaihe/kodelet/pkg/slashcommands"
	"github.com/pkg/errors"
	"golang.org/x/term"
)

func Run(ctx context.Context, config Config) error {
	if config.Runner == nil && config.Initialize == nil {
		return errors.New("chat requires a server connection; start 'kodelet serve' before opening chat")
	}
	// Bubble Tea otherwise opens the controlling TTY when stdin is redirected,
	// which can suspend a background process instead of failing non-interactively.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("chat requires an interactive terminal on stdin and stdout; use 'kodelet run' for non-interactive input")
	}
	// A TUI owns presentation and input, never a local execution environment.
	config.Remote = true
	theme, err := resolveTheme(config.Theme)
	if err != nil {
		return err
	}
	applyTheme(theme)

	initialModel := newModel(ctx, config)
	if closer, ok := initialModel.runner.(interface{ Close() error }); ok && config.Initialize == nil {
		defer func() {
			_ = closer.Close()
		}()
	}

	program := tea.NewProgram(initialModel)
	finalModel, err := program.Run()
	initialModel.cancel()
	final, isModel := finalModel.(model)
	if isModel && final.closeInitializedRunner != nil {
		final.closeInitializedRunner()
	}
	if err != nil {
		return err
	}
	if isModel {
		if final.startupErr != nil {
			return final.startupErr
		}
		if summary := renderExitSummary(final.conversationID, final.usage); summary != "" {
			fmt.Fprintln(os.Stdout, summary)
		}
	}
	return err
}

func newModel(ctx context.Context, config Config) model {
	if config.Initialize != nil {
		config.Remote = true
	}
	mctx, cancel := context.WithCancel(ctx)
	runCh := make(chan tea.Msg, 256)
	mctx = extensions.ContextWithDiagnosticSink(mctx, newTUIDiagnosticSink(runCh))
	extensionUI := newTUIExtensionUIHost(runCh, mctx.Done())
	mctx = extensions.ContextWithExtensionUIHost(mctx, extensionUI)
	mctx = extensions.ContextWithUIInputBroker(mctx, newTUIUIBroker(runCh, 0))
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

	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.MouseWheelEnabled = true

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := model{
		initialize:          config.Initialize,
		startupPending:      config.Initialize != nil,
		startupStartedAt:    time.Now(),
		ctx:                 mctx,
		cancel:              cancel,
		extensionUI:         extensionUI,
		extensionWidgets:    map[extensionUIKey]tuiExtensionWidget{},
		widgetOrder:         []extensionUIKey{},
		collapsedWidgets:    map[extensionUIKey]bool{},
		widgetOffsets:       map[extensionWidgetOffsetKey]int{},
		extensionSurfaces:   map[extensionUIKey]tuiExtensionSurface{},
		nextConversationKey: 1,
		theme:               theme,
		themeSelection:      themeSelection,
		viewport:            vp,
		textarea:            ta,
		spinner:             sp,
		runs:                map[int]*conversationRun{},
		runByState:          map[string]int{},
		shortcutCalls:       map[int]*extensionShortcutCall{},
		runCh:               runCh,
		terminalTitleEpoch:  time.Now(),
	}
	m.configure(config)
	return m
}

// configure installs connection and conversation settings without replacing the
// live program's context, channels, composer, or presentation state.
func (m *model) configure(config Config) {
	m.runner = config.Runner
	m.conversationSource, _ = config.Runner.(chat.ConversationSource)
	m.conversationStream, _ = config.Runner.(chat.ConversationStreamer)
	m.serverURL = strings.TrimSpace(config.ServerURL)
	m.remote = config.Remote
	m.remoteDefaultCWD = strings.TrimSpace(config.DefaultCWD)
	m.environmentProfile = strings.TrimSpace(config.EnvironmentProfile)
	m.profileSettings = cloneProfileSettings(config.ProfileSettings)
	requestedCWD := strings.TrimSpace(config.CWD)
	cwd := requestedCWD
	if cwd == "" && config.Remote {
		cwd = strings.TrimSpace(config.DefaultCWD)
	}
	if cwd == "" && !config.Remote {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	var messageHistoryStore *messagehistory.Store
	if !config.Remote {
		messageHistoryStore, _ = messagehistory.NewStore()
	}
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
	conversation.draft = m.textarea.Value()
	if conversation.conversationID == "" {
		conversation.readinessStartedAt = m.startupStartedAt
		conversation.resourcesLoading = config.Remote
	}
	m.conversationState = conversation
	m.conversations = map[string]*conversationState{conversationKey: conversation}
	m.activeConversationKey = conversationKey
	m.conversationDefaults = defaults
	m.messageHistoryStore = messageHistoryStore
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		m.spinner.Tick,
		waitForMsg(m.runCh),
	}
	if m.startupPending {
		cmds = append(cmds, m.initializeCommand())
	} else {
		cmds = append(cmds, m.initialResourceCommands()...)
	}
	if m.themeSelection == AutoThemeName {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

func (m model) initializeCommand() tea.Cmd {
	return func() tea.Msg {
		config, err := m.initialize(m.ctx)
		var closeRunner func()
		if closer, ok := config.Runner.(interface{ Close() error }); ok {
			// Also owns late results discarded when the program has already quit.
			closeRunner = sync.OnceFunc(func() { _ = closer.Close() })
			context.AfterFunc(m.ctx, closeRunner)
		}
		if err == nil {
			err = m.ctx.Err()
		}
		if err == nil && config.Runner == nil {
			err = errors.New("chat initialization did not return a server connection")
		}
		if err != nil && closeRunner != nil {
			closeRunner()
		}
		return initializedMsg{config: config, err: err, closeRunner: closeRunner}
	}
}

func (m model) initialResourceCommands() []tea.Cmd {
	cmds := []tea.Cmd{loadConversationHistoryFromSource(m.ctx, m.activeConversationKey, m.conversationID, m.conversationSource)}
	if !m.remote {
		cmds = append(cmds, loadMessageHistoryForConversation(m.ctx, m.activeConversationKey, m.messageHistoryStore, m.messageHistoryScopeCWD))
	}
	if !m.initialHistoryPending && !m.remote {
		cmds = append(cmds, loadSlashCommandsForConversation(m.ctx, m.activeConversationKey, m.slashCommandCWD()))
	}
	return cmds
}

func loadSlashCommands(ctx context.Context, cwd string) tea.Cmd {
	return loadSlashCommandsForConversation(ctx, "", cwd)
}

func (m model) loadRemoteSlashCommands(state *conversationState) tea.Cmd {
	discovery, ok := m.runner.(interface {
		DiscoverWorkspace(context.Context, chat.WorkspaceTarget) (protocol.WorkspaceDiscoverResult, error)
	})
	if state == nil {
		return nil
	}
	if !ok {
		state.resourcesLoading = false
		if state.conversationID == "" && !state.readinessStartedAt.IsZero() {
			state.readyDuration = time.Since(state.readinessStartedAt)
			state.readinessStartedAt = time.Time{}
		}
		return nil
	}
	state.resourcesLoading = state.conversationID == ""
	state.discoveryID++
	discoveryID := state.discoveryID
	if state.resourcesLoading && state.readinessStartedAt.IsZero() {
		state.readinessStartedAt = time.Now()
	}
	startedAt := state.readinessStartedAt
	key, cwd := state.key, slashCommandCWDForState(state)
	target := chat.WorkspaceTarget{ConversationID: state.conversationID}
	if target.ConversationID == "" {
		target.CWD, target.Profile, target.EnvironmentProfile = cwd, state.profile, m.environmentProfile
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		result, err := discovery.DiscoverWorkspace(ctx, target)
		var shortcuts []extensions.Shortcut
		for _, shortcut := range result.Shortcuts {
			shortcuts = append(shortcuts, extensions.Shortcut{Key: shortcut.Key, Description: shortcut.Description, ExtensionID: shortcut.ExtensionID, Generation: shortcut.Generation})
		}
		var readyDuration time.Duration
		if !startedAt.IsZero() {
			readyDuration = time.Since(startedAt)
		}
		return slashCommandsMsg{conversationKey: key, conversationID: target.ConversationID, profile: target.Profile, cwd: cwd, commands: withTUIBuiltInSlashCommands(result.Commands), shortcuts: shortcuts, shortcutDigest: result.Digest, extensionCount: result.ExtensionCount, remote: true, discoveryID: discoveryID, readyDuration: readyDuration, err: err}
	}
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
