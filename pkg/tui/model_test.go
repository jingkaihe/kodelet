package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type stringMsg string

func (m stringMsg) String() string {
	return string(m)
}

func numberedLines(count int) string {
	return strings.TrimRight(strings.Repeat("line\n", count), "\n")
}

var _ tea.Model = model{}
