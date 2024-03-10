package table

import (
	"slices"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/leg100/pug/internal/resource"
	"github.com/mattn/go-runewidth"
	"golang.org/x/exp/maps"
)

// TODO:
// * sortable
// * max-min col width

// Model defines a state for the table widget.
type Model[T Item] struct {
	KeyMap KeyMap

	cols      []Column
	rows      []Row[T]
	cellsFunc func(T) []string
	cursor    int
	focus     bool
	styles    Styles

	items    map[resource.ID]T
	parent   resource.Resource
	sortFunc func(T, T) int

	Selected   map[resource.ID]T
	selectable bool
	selectAll  bool

	viewport viewport.Model
	start    int
	end      int
}

type Item interface {
	resource.Entity

	HasAncestor(id resource.ID) bool
}

// Row represents one line in the table.
type Row[T resource.Entity] struct {
	Cells  []string
	Entity T
}

// Column defines the table structure.
type Column struct {
	Title          string
	Width          int
	TruncationFunc func(s string, w int, tail string) string
}

type CellsFunc[T Item] func(T) []string

// Styles contains style definitions for this list component. By default, these
// values are generated by DefaultStyles.
type Styles struct {
	Header      lipgloss.Style
	Cell        lipgloss.Style
	Highlighted lipgloss.Style
	Selected    lipgloss.Style
}

// DefaultStyles returns a set of default style definitions for this table.
func DefaultStyles() Styles {
	return Styles{
		Highlighted: lipgloss.NewStyle().Bold(true).
			// light pink
			Foreground(lipgloss.Color("212")).
			// purple
			Background(lipgloss.Color("57")),
		Selected: lipgloss.NewStyle().Bold(true).
			// yellow
			Background(lipgloss.Color("227")),
		Header: lipgloss.NewStyle().Bold(true).Padding(0, 1),
		Cell:   lipgloss.NewStyle().Padding(0, 1),
	}
}

// SetStyles sets the table styles.
func (m *Model[T]) SetStyles(s Styles) {
	m.styles = s
	m.UpdateViewport()
}

// New creates a new model for the table widget.
func New[T Item](columns []Column) Model[T] {
	m := Model[T]{
		cursor:   0,
		viewport: viewport.New(0, 20),

		KeyMap:     DefaultKeyMap(),
		styles:     DefaultStyles(),
		items:      make(map[resource.ID]T),
		Selected:   make(map[resource.ID]T),
		selectable: true,
		focus:      true,
	}
	// Set default truncation function for each column if unset
	for i := range columns {
		if columns[i].TruncationFunc == nil {
			columns[i].TruncationFunc = runewidth.Truncate
		}
	}
	m.cols = columns

	//TODO: should this be called before rows are defined?
	m.UpdateViewport()

	return m
}

// WithColumns sets the table columns (headers).
func (m Model[T]) WithColumns(cols []Column) Model[T] {
	m.cols = cols
	return m
}

// WithCellsFunc specifies a function that creates row cells for an entity.
func (m Model[T]) WithCellsFunc(fn func(T) []string) Model[T] {
	m.cellsFunc = fn
	return m
}

// WithCellsFunc specifies a function that creates row cells for an entity.
func (m Model[T]) WithSortFunc(sortFunc func(T, T) int) Model[T] {
	m.sortFunc = sortFunc
	return m
}

// WithParent specifies a parent resource for the table, restricting table items
// to descendents of the parent.
func (m Model[T]) WithParent(parent resource.Resource) Model[T] {
	m.parent = parent
	return m
}

// WithHeight sets the height of the table.
func (m Model[T]) WithHeight(h int) Model[T] {
	m.viewport.Height = h
	return m
}

// WithWidth sets the width of the table.
func (m Model[T]) WithWidth(w int) Model[T] {
	m.viewport.Width = w
	return m
}

// WithFocused sets the focus state of the table.
func (m Model[T]) WithFocused(f bool) Model[T] {
	m.focus = f
	return m
}

// WithSelectable sets whether rows are selectable.
func (m Model[T]) WithSelectable(s bool) Model[T] {
	m.selectable = s
	return m
}

// WithStyles sets the table styles.
func (m Model[T]) WithStyles(s Styles) Model[T] {
	m.styles = s
	return m
}

// WithKeyMap sets the key map.
func (m Model[T]) WithKeyMap(km KeyMap) Model[T] {
	m.KeyMap = km
	return m
}

// Update is the Bubble Tea update loop.
func (m Model[T]) Update(msg tea.Msg) (Model[T], tea.Cmd) {
	if !m.focus {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.KeyMap.LineUp):
			m.MoveUp(1)
		case key.Matches(msg, m.KeyMap.LineDown):
			m.MoveDown(1)
		case key.Matches(msg, m.KeyMap.PageUp):
			m.MoveUp(m.viewport.Height)
		case key.Matches(msg, m.KeyMap.PageDown):
			m.MoveDown(m.viewport.Height)
		case key.Matches(msg, m.KeyMap.HalfPageUp):
			m.MoveUp(m.viewport.Height / 2)
		case key.Matches(msg, m.KeyMap.HalfPageDown):
			m.MoveDown(m.viewport.Height / 2)
		case key.Matches(msg, m.KeyMap.LineDown):
			m.MoveDown(1)
		case key.Matches(msg, m.KeyMap.GotoTop):
			m.GotoTop()
		case key.Matches(msg, m.KeyMap.GotoBottom):
			m.GotoBottom()
		case key.Matches(msg, m.KeyMap.Select):
			m.ToggleSelection()
		case key.Matches(msg, m.KeyMap.SelectAll):
			m.ToggleSelectAll()
		}
	case DeselectMsg:
		m.DeselectAll()
	case BulkInsertMsg[T]:
		existing := maps.Values(m.Items())
		m.SetItems(append(existing, msg...))
	case resource.Event[T]:
		switch msg.Type {
		case resource.CreatedEvent:
			existing := maps.Values(m.Items())
			m.SetItems(append(existing, msg.Payload))
		case resource.UpdatedEvent:
			existing := m.Items()
			existing[msg.Payload.ID()] = msg.Payload
			m.SetItems(maps.Values(existing))
		case resource.DeletedEvent:
			existing := m.Items()
			delete(existing, msg.Payload.ID())
			m.SetItems(maps.Values(existing))
		}
		//case common.ViewSizeMsg:
		//	// subtract 2 to account for margins (1: left, 1: right)
		//	m.viewport.Width = msg.Width - 2
		//	m.viewport.Height = msg.Height - 3
		//	m.UpdateViewport()
	}

	return m, nil
}

// Focused returns the focus state of the table.
func (m Model[T]) Focused() bool {
	return m.focus
}

// Focus focuses the table, allowing the user to move around the rows and
// interact.
func (m *Model[T]) Focus() {
	m.focus = true
	m.UpdateViewport()
}

// Blur blurs the table, preventing selection or movement.
func (m *Model[T]) Blur() {
	m.focus = false
	m.UpdateViewport()
}

// View renders the component.
func (m Model[T]) View() string {
	return m.headersView() + "\n" + m.viewport.View()
}

// UpdateViewport updates the list content based on the previously defined
// columns and rows.
func (m *Model[T]) UpdateViewport() {
	renderedRows := make([]string, 0, len(m.rows))

	// Render only rows from: m.cursor-m.viewport.Height to: m.cursor+m.viewport.Height
	// Constant runtime, independent of number of rows in a table.
	// Limits the number of renderedRows to a maximum of 2*m.viewport.Height
	if m.cursor >= 0 {
		m.start = clamp(m.cursor-m.viewport.Height, 0, m.cursor)
	} else {
		m.start = 0
	}
	m.end = clamp(m.cursor+m.viewport.Height, m.cursor, len(m.rows))
	for i := m.start; i < m.end; i++ {
		renderedRows = append(renderedRows, m.renderRow(i))
	}

	m.viewport.SetContent(
		lipgloss.JoinVertical(lipgloss.Left, renderedRows...),
	)
}

// Highlighted returns the currently highlighted entity.
func (m Model[T]) Highlighted() (T, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return *new(T), false
	}

	return m.rows[m.cursor].Entity, true
}

// HighlightedOrSelected returns either the selected entities, if there are no
// selections, the currently highlighted entity.
func (m Model[T]) HighlightedOrSelected() map[resource.ID]T {
	if len(m.Selected) > 0 {
		return m.Selected
	}
	if res, ok := m.Highlighted(); ok {
		return map[resource.ID]T{
			res.ID(): res,
		}
	}
	return nil
}

// ToggleSelection toggles the selection of the currently highlighted row.
func (m *Model[T]) ToggleSelection() {
	if !m.selectable {
		return
	}

	highlighted, ok := m.Highlighted()
	if !ok {
		return
	}

	if entity, isSelected := m.Selected[highlighted.ID()]; isSelected {
		delete(m.Selected, highlighted.ID())
	} else {
		m.Selected[highlighted.ID()] = entity
	}
	m.UpdateViewport()
}

// ToggleSelectAll toggles the selection of all rows.
func (m *Model[T]) ToggleSelectAll() {
	if !m.selectable {
		return
	}

	if m.selectAll {
		m.DeselectAll()
		return
	}

	// Select all
	m.Selected = make(map[resource.ID]T, len(m.rows))
	for _, r := range m.rows {
		m.Selected[r.Entity.ID()] = r.Entity
	}
	m.selectAll = true
	m.UpdateViewport()
}

// DeselectAll de-selects any rows that are currently selected
func (m *Model[T]) DeselectAll() {
	if !m.selectable {
		return
	}

	m.Selected = make(map[resource.ID]T)
	m.selectAll = false
	m.UpdateViewport()
}

// Items returns the current items.
func (m Model[T]) Items() map[resource.ID]T {
	return m.items
}

// SetItems sets new items on the table, overwriting existing items.
func (m *Model[T]) SetItems(items []T) {
	// Overwrite existing items
	m.items = make(map[resource.ID]T)

	// Carry over existing selections.
	seen := make(map[resource.ID]T)

	// Sort items in-place
	slices.SortFunc(items, m.sortFunc)

	// Overwrite existing rows
	m.rows = make([]Row[T], len(items))
	// Convert items into rows, and carry across matching selections
	for i, it := range items {
		// Skip items that are not a descendent of the parent.
		//if !it.HasAncestor(m.parent.ID()) {
		//	continue
		//}
		m.rows[i] = Row[T]{
			Cells:  m.cellsFunc(it),
			Entity: it,
		}
		if m.selectable {
			if _, ok := m.Selected[it.ID()]; ok {
				seen[it.ID()] = it
			}
		}
		m.items[it.ID()] = it
	}
	// Overwrite existing selections, removing any that no longer have a
	// corresponding item.
	m.Selected = seen

	m.UpdateViewport()
}

// SetColumns sets a new columns state.
func (m *Model[T]) SetColumns(c []Column) {
	m.cols = c
	m.UpdateViewport()
}

// SetWidth sets the width of the viewport of the table.
func (m *Model[T]) SetWidth(w int) {
	m.viewport.Width = w
	m.UpdateViewport()
}

// SetHeight sets the height of the viewport of the table.
func (m *Model[T]) SetHeight(h int) {
	m.viewport.Height = h
	m.UpdateViewport()
}

// Height returns the viewport height of the table.
func (m Model[T]) Height() int {
	return m.viewport.Height
}

// Width returns the viewport width of the table.
func (m Model[T]) Width() int {
	return m.viewport.Width
}

// Cursor returns the index of the highlighted row.
func (m Model[T]) Cursor() int {
	return m.cursor
}

// SetCursor sets the cursor position in the table.
func (m *Model[T]) SetCursor(n int) {
	m.cursor = clamp(n, 0, len(m.rows)-1)
	m.UpdateViewport()
}

// MoveUp moves the highlightion up by any number of rows.
// It can not go above the first row.
func (m *Model[T]) MoveUp(n int) {
	m.cursor = clamp(m.cursor-n, 0, len(m.rows)-1)
	switch {
	case m.start == 0:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset, 0, m.cursor))
	case m.start < m.viewport.Height:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset+n, 0, m.cursor))
	case m.viewport.YOffset >= 1:
		m.viewport.YOffset = clamp(m.viewport.YOffset+n, 1, m.viewport.Height)
	}
	m.UpdateViewport()
}

// MoveDown moves the highlightion down by any number of rows.
// It can not go below the last row.
func (m *Model[T]) MoveDown(n int) {
	m.cursor = clamp(m.cursor+n, 0, len(m.rows)-1)
	m.UpdateViewport()

	switch {
	case m.end == len(m.rows):
		m.viewport.SetYOffset(clamp(m.viewport.YOffset-n, 1, m.viewport.Height))
	case m.cursor > (m.end-m.start)/2:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset-n, 1, m.cursor))
	case m.viewport.YOffset > 1:
	case m.cursor > m.viewport.YOffset+m.viewport.Height-1:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset+1, 0, 1))
	}
}

// GotoTop moves the highlightion to the first row.
func (m *Model[T]) GotoTop() {
	m.MoveUp(m.cursor)
}

// GotoBottom moves the highlightion to the last row.
func (m *Model[T]) GotoBottom() {
	m.MoveDown(len(m.rows))
}

func (m Model[T]) headersView() string {
	var s = make([]string, 0, len(m.cols))
	for _, col := range m.cols {
		style := lipgloss.NewStyle().Width(col.Width).MaxWidth(col.Width).Inline(true)
		renderedCell := style.Render(runewidth.Truncate(col.Title, col.Width, "…"))
		s = append(s, m.styles.Header.Render(renderedCell))
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, s...)
}

func (m *Model[T]) renderRow(rowID int) string {
	var s = make([]string, 0, len(m.cols))
	for i, value := range m.rows[rowID].Cells {
		style := lipgloss.NewStyle().Width(m.cols[i].Width).MaxWidth(m.cols[i].Width).Inline(true)
		renderedCell := m.styles.Cell.Render(style.Render(m.cols[i].TruncationFunc(value, m.cols[i].Width, "…")))
		s = append(s, renderedCell)
	}

	row := lipgloss.JoinHorizontal(lipgloss.Left, s...)

	if rowID == m.cursor {
		return m.styles.Highlighted.Render(row)
	}
	if _, ok := m.Selected[m.rows[rowID].Entity.ID()]; ok {
		return m.styles.Selected.Render(row)
	}

	return row
}

func clamp(v, low, high int) int {
	return min(max(v, low), high)
}
