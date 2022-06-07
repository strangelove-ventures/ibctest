package tui

import (
	"context"
	"errors"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/strangelove-ventures/ibctest/internal/blockdb"
)

// QueryService queries a database and returns results.
type QueryService interface {
	Chains(ctx context.Context, testCaseID int64) ([]blockdb.ChainResult, error)
}

// Model is a tea.Model.
type Model struct {
	// See NewModel for rationale behind capturing context in a struct field.
	ctx        context.Context
	headerView string
	testCases  []blockdb.TestCaseResult

	currentFocus int
	list         list.Model
}

// NewModel returns a valid *Model.
// The args ctx, querySvc, dbFilePath, and schemaGitSha are required or this function panics.
// We capture ctx into a struct field which is not idiomatic. However, the tea.Model interface does not allow
// passing a context. Therefore, we must capture it in the constructor.
func NewModel(
	ctx context.Context,
	querySvc QueryService,
	dbFilePath string,
	schemaGitSha string,
	testCases []blockdb.TestCaseResult) *Model {
	if querySvc == nil {
		panic(errors.New("querySvc missing"))
	}
	if dbFilePath == "" {
		panic(errors.New("dbFilePath missing"))
	}
	if schemaGitSha == "" {
		panic(errors.New("schemaGitSha missing"))
	}

	lm := newListModel("Select Test Case:")
	lm.SetItems(testCasesToItems(testCases))

	return &Model{
		ctx:        ctx,
		headerView: schemaVersionView(dbFilePath, schemaGitSha),
		testCases:  testCases,
		list:       lm,
	}
}

// Init implements tea.Model. Currently, a nop.
func (m *Model) Init() tea.Cmd { return nil }

// View implements tea.Model.
func (m *Model) View() string {
	if m.currentFocus == focusBlocks {
		panic("TODO")
	}
	return docStyle.Render(
		lipgloss.JoinVertical(0,
			m.headerView, m.list.View(),
		),
	)
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v-4) // TODO: the 4 is the header view height
	}

	if m.currentFocus == focusBlocks {
		return m, nil
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

const (
	focusTestCases = iota
	focusChains
	focusBlocks
)
