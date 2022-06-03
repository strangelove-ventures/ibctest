package tui

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// QueryService queries a database and returns results.
type QueryService interface {
}

// Model is a tea.Model.
// The struct must be initialized with all exported fields set to non-empty values.
type Model struct {
	SchemaCreatedAt time.Time
	SchemaGitSha    string

	err error
}

// Init implements tea.Model.
// Init panics if any exported field is not set.
func (m *Model) Init() tea.Cmd {
	if m.SchemaCreatedAt.IsZero() {
		panic(errors.New("missing SchemaCreatedAt"))
	}
	if m.SchemaGitSha == "" {
		panic(errors.New("missing SchemaGitSha"))
	}
	return nil
}

// View implements tea.Model.
func (m *Model) View() string {
	return schemaVersionView(m.SchemaGitSha, m.SchemaCreatedAt)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {

		case "ctrl+c", "q":
			return m, tea.Quit
		}
	}
	return m, nil
}
