package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/davecgh/go-spew/spew"
	"github.com/leg100/pug/internal/logging"
	"github.com/leg100/pug/internal/module"
	"github.com/leg100/pug/internal/resource"
	"github.com/leg100/pug/internal/run"
	"github.com/leg100/pug/internal/task"
	"github.com/leg100/pug/internal/workspace"
)

type mainModel struct {
	runs *run.Service

	*navigator

	width  int
	height int

	showHelp bool
	err      string

	dump *os.File
}

type Options struct {
	ModuleService    *module.Service
	WorkspaceService *workspace.Service
	RunService       *run.Service
	TaskService      *task.Service

	Logger    *logging.Logger
	Workdir   string
	FirstPage int
	MaxTasks  int
}

func New(opts Options) (mainModel, error) {
	messages, err := os.OpenFile("messages.log", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return mainModel{}, err
	}
	makers := map[modelKind]maker{
		ModuleListKind: &moduleListModelMaker{
			svc:        opts.ModuleService,
			workspaces: opts.WorkspaceService,
			workdir:    opts.Workdir,
		},
		WorkspaceListKind: &workspaceListModelMaker{
			svc:     opts.WorkspaceService,
			modules: opts.ModuleService,
			runs:    opts.RunService,
		},
		RunListKind: &runListModelMaker{
			svc:   opts.RunService,
			tasks: opts.TaskService,
		},
		TaskListKind: &taskListModelMaker{
			svc:      opts.TaskService,
			maxTasks: opts.MaxTasks,
		},
		TaskKind: &taskModelMaker{
			svc: opts.TaskService,
		},
		LogsKind: &logsModelMaker{
			logger: opts.Logger,
		},
	}
	navigator, err := newNavigator(modelKind(opts.FirstPage), makers)
	if err != nil {
		return mainModel{}, err
	}
	m := mainModel{
		runs:      opts.RunService,
		navigator: navigator,
		dump:      messages,
	}
	return m, nil
}

func (m mainModel) Init() tea.Cmd {
	return m.currentModel().Init()
}

func (m mainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	spew.Fdump(m.dump, msg)

	switch msg := msg.(type) {
	case resource.Event[*workspace.Workspace]:
		switch msg.Type {
		case resource.CreatedEvent:
			//return m, navigate(page{kind: WorkspaceListKind, resource: *msg.Payload.Parent})
			cmds = append(cmds, runCmd(m.runs, msg.Payload.ID()))
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, m.resizeCmd
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, Keys.Quit):
			// ctrl-c quits the app
			return m, tea.Quit
		case key.Matches(msg, Keys.Escape):
			// <esc> whilst in help turns off help
			if m.showHelp {
				m.showHelp = false
			} else {
				m.goBack()
			}
		case key.Matches(msg, Keys.Help):
			// '?' toggles help
			m.showHelp = !m.showHelp
		case key.Matches(msg, Keys.Logs):
			// 'l' shows logs
			return m, navigate(page{kind: LogsKind})
		case key.Matches(msg, Keys.Modules):
			// 'm' lists all modules
			return m, navigate(page{kind: ModuleListKind})
		case key.Matches(msg, Keys.Workspaces):
			// 'W' lists all workspaces
			return m, navigate(page{kind: WorkspaceListKind})
		case key.Matches(msg, Keys.Runs):
			// 'R' lists all runs
			return m, navigate(page{kind: RunListKind})
		case key.Matches(msg, Keys.Tasks):
			// 'T' lists all tasks
			return m, navigate(page{kind: TaskListKind})
		default:
			// Send other keys to current model.
			cmd := m.updateCurrent(msg)
			return m, cmd
		}
	case tea.MouseMsg:
		return m, nil
	case spinner.TickMsg, currentMsg:
		// Events to be sent only to the current model.
		cmd := m.updateCurrent(msg)
		return m, cmd
	case navigationMsg:
		created, err := m.setCurrent(msg)
		if err != nil {
			return m, newErrorCmd(err, "setting current page")
		}
		if created {
			return m, tea.Batch(m.currentModel().Init(), m.resizeCmd)
		}
		return m, tea.Batch(m.resizeCmd, cmdHandler(currentMsg{}))
	case errorMsg:
		m.err = fmt.Sprintf("%s: %s", fmt.Sprintf(msg.Message, msg.Args...), msg.Error.Error())
	default:
		// Send remaining msg types to all cached models
		cmds = append(cmds, m.cache.updateAll(msg)...)
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

var (
	logo = strings.Join([]string{
		"▄▄▄ ▄ ▄ ▄▄▄",
		"█▄█ █ █ █ ▄",
		"▀   ▀▀▀ ▀▀▀",
	}, "\n")
	renderedLogo = Bold.
			Copy().
			Padding(0, 1).
			Foreground(Pink).
			Render(logo)
	logoWidth         = lipgloss.Width(renderedLogo)
	headerHeight      = 3
	breadcrumbsHeight = 1
	messageFooter     = 1
)

func (m mainModel) View() string {
	var (
		content    string
		pagination string
	)

	if m.showHelp {
		content = renderHelp(m.currentModel().HelpBindings(), max(1, m.height-2))
	} else {
		content = m.currentModel().View()
		pagination = m.currentModel().Pagination()
	}

	return lipgloss.JoinVertical(
		lipgloss.Top,
		// header
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			// key bindings
			lipgloss.NewStyle().
				Width(m.width-logoWidth).
				Render(RenderShort(m.currentModel().HelpBindings())),
			renderedLogo,
		),
		// breadcrumbs
		lipgloss.NewStyle().
			// Prohibit overflowing breadcrumb components wrapping to another line.
			MaxHeight(1).
			Width(m.width).
			Background(grey).
			Render(m.currentModel().Title()),
		// content
		lipgloss.NewStyle().
			Height(m.viewHeight()).
			Width(m.viewWidth()).
			//MaxHeight(m.viewHeight()).
			Render(content),
		// footer
		lipgloss.JoinHorizontal(
			lipgloss.Top,
			// error messages
			lipgloss.NewStyle().
				Width(m.width-Width(pagination)).
				Padding(0, 1).
				Padding(0, 1).
				Foreground(Red).
				Render(m.err),
			// pagination
			pagination,
		),
	)
}

// viewHeight retrieves the height available beneath the header and breadcrumbs,
// and the message footer.
func (m mainModel) viewHeight() int {
	return m.height - headerHeight - breadcrumbsHeight - messageFooter
}

// viewWidth retrieves the width available within the main view
func (m mainModel) viewWidth() int {
	return m.width
}

func (m mainModel) resizeCmd() tea.Msg {
	return ViewSizeMsg{Width: m.viewWidth(), Height: m.viewHeight()}
}
