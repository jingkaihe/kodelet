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

var newDefaultChatRunner = func(defaultCWD string) chat.ChatRunner {
	return chat.NewDefaultChatRunner(defaultCWD)
}

func Run(ctx context.Context, config Config) error {
	theme, ok := themeByName(config.Theme)
	if !ok {
		return ValidateThemeName(config.Theme)
	}
	applyTheme(theme)

	initialModel := newModel(ctx, config)
	if closer, ok := initialModel.runner.(interface{ Close() error }); ok {
		defer func() {
			_ = closer.Close()
		}()
	}

	program := tea.NewProgram(initialModel, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
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
	theme, ok := themeByName(config.Theme)
	if !ok {
		theme = themes[DefaultThemeName]
	}
	applyTheme(theme)

	ta := textarea.New()
	ta.Placeholder = "Ask kodelet..."
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.FocusedStyle.Base = composerTextStyle
	ta.FocusedStyle.CursorLine = composerTextStyle
	ta.FocusedStyle.Placeholder = inputPlaceholderStyle
	ta.FocusedStyle.Text = composerTextStyle
	ta.FocusedStyle.EndOfBuffer = composerTextStyle
	ta.FocusedStyle.Prompt = composerTextStyle
	ta.BlurredStyle.Base = composerTextStyle
	ta.BlurredStyle.CursorLine = composerTextStyle
	ta.BlurredStyle.Placeholder = inputPlaceholderStyle
	ta.BlurredStyle.Text = composerTextStyle
	ta.BlurredStyle.EndOfBuffer = composerTextStyle
	ta.BlurredStyle.Prompt = composerTextStyle
	ta.Cursor.Style = composerCursorStyle
	ta.Cursor.TextStyle = composerTextStyle
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
		runner = newDefaultChatRunner("")
	}
	requestedCWD := strings.TrimSpace(config.CWD)
	cwd := requestedCWD
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	messageHistoryStore, _ := messagehistory.NewStore()
	conversationID := strings.TrimSpace(config.ConversationID)
	initialHistoryPending := conversationID != ""
	var messageHistoryScopeCWD string
	if !initialHistoryPending {
		messageHistoryScopeCWD, _ = messagehistory.ResolveScopeCWD(cwd)
	}
	profile := displayProfile(config.Profile)
	profileOptionsInput := config.ProfileOptions
	if len(profileOptionsInput) == 0 {
		profileOptionsInput = loadProfileOptions()
	}
	profileOptions := normalizeProfileOptions(profileOptionsInput, profile)
	profileIndex := profileOptionIndex(profileOptions, profile)
	if profileIndex < 0 {
		profileIndex = 0
	}

	return model{
		ctx:                    mctx,
		cancel:                 cancel,
		runner:                 runner,
		conversationID:         conversationID,
		profile:                profile,
		profileOptions:         profileOptions,
		profileIndex:           profileIndex,
		cwd:                    cwd,
		requestedCWD:           requestedCWD,
		messageHistoryStore:    messageHistoryStore,
		messageHistoryScopeCWD: messageHistoryScopeCWD,
		initialHistoryPending:  initialHistoryPending,
		theme:                  theme,
		slashCommandIndex:      -1,
		viewport:               vp,
		textarea:               ta,
		spinner:                sp,
		autoFollow:             true,
		runCh:                  make(chan tea.Msg, 256),
		status:                 "ready",
		terminalTitleEpoch:     time.Now(),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		waitForMsg(m.runCh),
		loadInitialHistory(m.ctx, m.conversationID),
		loadMessageHistory(m.ctx, m.messageHistoryStore, m.messageHistoryScopeCWD),
		loadSlashCommands(m.ctx, m.slashCommandCWD()),
	)
}

func loadSlashCommands(ctx context.Context, cwd string) tea.Cmd {
	return func() tea.Msg {
		commands, err := listBaseSlashCommands(ctx, cwd)
		return slashCommandsMsg{cwd: strings.TrimSpace(cwd), commands: commands, err: err}
	}
}

func loadExtensionSlashCommands(ctx context.Context, cwd string) tea.Cmd {
	return func() tea.Msg {
		commands, err := listExtensionSlashCommands(ctx, cwd)
		return slashCommandsMsg{cwd: strings.TrimSpace(cwd), commands: commands, extensionsOnly: true, err: err}
	}
}

func listSlashCommands(ctx context.Context, cwd string) ([]slashcommands.Command, error) {
	commands, err := listBaseSlashCommands(ctx, cwd)
	if err != nil {
		return commands, err
	}
	extensionCommands, err := listExtensionSlashCommands(ctx, cwd)
	if err != nil {
		return commands, err
	}
	return append(commands, extensionCommands...), nil
}

func listBaseSlashCommands(ctx context.Context, cwd string) ([]slashcommands.Command, error) {
	resolvedCWD, err := resolveSlashCommandCWD(cwd)
	if err != nil {
		return slashcommands.BuiltIns(), err
	}

	processor, err := fragments.NewFragmentProcessor(fragments.WithDefaultDirsForCWD(resolvedCWD))
	if err != nil {
		return slashcommands.BuiltIns(), errors.Wrap(err, "failed to initialize slash commands")
	}

	return slashcommands.List(ctx, processor), nil
}

func listExtensionSlashCommands(ctx context.Context, cwd string) ([]slashcommands.Command, error) {
	resolvedCWD, err := resolveSlashCommandCWD(cwd)
	if err != nil {
		return nil, err
	}

	extensionRuntime, err := extensions.NewRuntimeFromViper(ctx, resolvedCWD)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize extensions for slash commands")
	}
	if extensionRuntime == nil {
		return nil, nil
	}
	defer func() { _ = extensionRuntime.Close() }()

	return extensionRuntime.SlashCommands(), nil
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
	if strings.TrimSpace(m.requestedCWD) != "" {
		return m.requestedCWD
	}
	return m.cwd
}
