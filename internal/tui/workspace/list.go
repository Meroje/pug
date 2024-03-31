package workspace

import (
	"fmt"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/leg100/pug/internal/module"
	"github.com/leg100/pug/internal/resource"
	"github.com/leg100/pug/internal/run"
	"github.com/leg100/pug/internal/tui"
	"github.com/leg100/pug/internal/tui/keys"
	"github.com/leg100/pug/internal/tui/table"
	"github.com/leg100/pug/internal/workspace"
)

var currentColumn = table.Column{
	Key:   "current",
	Title: "CURRENT",
	// width of "CURRENT"; the actual content is a '✓' or nothing
	Width:      7,
	FlexFactor: 1,
}

type ListMaker struct {
	ModuleService    *module.Service
	WorkspaceService *workspace.Service
	RunService       *run.Service
}

func (m *ListMaker) Make(parent resource.Resource, width, height int) (tui.Model, error) {
	columns := []table.Column{
		table.WorkspaceColumn,
	}
	if parent.Kind == resource.Global {
		// Show module column in global workspaces table
		columns = append(columns, table.ModuleColumn)
	}
	columns = append(columns,
		currentColumn,
		table.RunStatusColumn,
		table.RunChangesColumn,
	)

	rowRenderer := rowRenderer{
		ModuleService: m.ModuleService,
		RunService:    m.RunService,
		parent:        parent,
	}

	table := table.NewResource(table.ResourceOptions[*workspace.Workspace]{
		ModuleService:    m.ModuleService,
		WorkspaceService: m.WorkspaceService,
		Columns:          columns,
		Renderer:         rowRenderer.renderRow,
		Width:            width,
		Height:           height,
		Parent:           parent,
		SortFunc:         workspace.Sort(m.ModuleService),
	})

	return list{
		table:   table,
		svc:     m.WorkspaceService,
		modules: m.ModuleService,
		runs:    m.RunService,
		parent:  parent.ID,
	}, nil
}

type list struct {
	table   table.Resource[resource.ID, *workspace.Workspace]
	svc     *workspace.Service
	modules *module.Service
	runs    *run.Service
	parent  resource.ID
}

func (m list) Init() tea.Cmd {
	return func() tea.Msg {
		workspaces := m.svc.List(workspace.ListOptions{
			ModuleID: m.parent,
		})
		return table.BulkInsertMsg[*workspace.Workspace](workspaces)
	}
}

func (m list) Update(msg tea.Msg) (tui.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case resource.Event[*run.Run]:
		// Update current run status and changes
		m.table.UpdateViewport()
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Global.Enter):
			if row, ok := m.table.Highlighted(); ok {
				return m, tui.NavigateTo(tui.WorkspaceKind, tui.WithParent(row.Value.Resource))
			}
		case key.Matches(msg, localKeys.Init):
			cmd := tui.CreateTasks("init", m.modules.Init, m.highlightedOrSelectedModuleIDs()...)
			m.table.DeselectAll()
			return m, cmd
		case key.Matches(msg, localKeys.Format):
			cmd := tui.CreateTasks("format", m.modules.Format, m.highlightedOrSelectedModuleIDs()...)
			m.table.DeselectAll()
			return m, cmd
		case key.Matches(msg, localKeys.Validate):
			cmd := tui.CreateTasks("validate", m.modules.Validate, m.highlightedOrSelectedModuleIDs()...)
			m.table.DeselectAll()
			return m, cmd
		case key.Matches(msg, localKeys.Plan):
			workspaceIDs := m.table.HighlightedOrSelectedKeys()
			m.table.DeselectAll()
			return m, tui.CreateRuns(m.runs, workspaceIDs...)
		}
	}

	// Handle keyboard and mouse events in the table widget
	m.table, cmd = m.table.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m list) Title() string {
	return tui.GlobalBreadcrumb("Workspaces")
}

func (m list) View() string {
	return m.table.View()
}

func (m list) Pagination() string {
	return ""
}

func (m list) TabStatus() string {
	return fmt.Sprintf("(%d)", len(m.table.Items()))
}

func (m list) HelpBindings() (bindings []key.Binding) {
	return keys.KeyMapToSlice(localKeys)
}

func (m list) highlightedOrSelectedModuleIDs() []resource.ID {
	selected := m.table.HighlightedOrSelected()
	moduleIDs := make([]resource.ID, len(selected))
	for i, row := range selected {
		moduleIDs[i] = row.Value.ModuleID()
	}
	return moduleIDs
}
