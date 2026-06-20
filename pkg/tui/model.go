// Package tui implements Kodelet's native terminal chat interface.
package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jingkaihe/kodelet/pkg/webui"
)

func Run(ctx context.Context, config Config) error {
	theme, ok := themeByName(config.Theme)
	if !ok {
		return ValidateThemeName(config.Theme)
	}
	applyTheme(theme)

	initialModel := newModel(ctx, config)

	program := tea.NewProgram(initialModel, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	if final, ok := finalModel.(model); ok {
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
		runner = webui.NewDefaultChatRunner(config.CWD)
	}
	cwd := strings.TrimSpace(config.CWD)
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	return model{
		ctx:            mctx,
		cancel:         cancel,
		runner:         runner,
		conversationID: strings.TrimSpace(config.ConversationID),
		profile:        displayProfile(config.Profile),
		cwd:            cwd,
		theme:          theme,
		viewport:       vp,
		textarea:       ta,
		spinner:        sp,
		autoFollow:     true,
		runCh:          make(chan tea.Msg, 256),
		status:         "ready",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		waitForMsg(m.runCh),
		loadInitialHistory(m.ctx, m.conversationID),
	)
}
