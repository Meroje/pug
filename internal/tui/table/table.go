package table

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/leg100/go-runewidth"
	"github.com/leg100/pug/internal"
	"github.com/leg100/pug/internal/tui"
	"github.com/leg100/pug/internal/tui/keys"
	"golang.org/x/exp/maps"
)

const (
	// Height of the table header
	headerHeight = 1
	// Height of filter widget
	filterHeight = 2
)

// Model defines a state for the table widget.
type Model[K comparable, V any] struct {
	cols        []Column
	rows        []Row[K, V]
	rowRenderer RowRenderer[V]
	cursor      int
	focus       bool
	styles      Styles

	items    map[K]V
	sortFunc SortFunc[V]

	Selected   map[K]V
	selectable bool
	selectAll  bool

	filter textinput.Model

	viewport viewport.Model
	start    int
	end      int

	// dimensions calcs
	width  int
	height int
}

// Column defines the table structure.
type Column struct {
	Key ColumnKey
	// TODO: Default to upper case of key
	Title          string
	Width          int
	FlexFactor     int
	TruncationFunc func(s string, w int, tail string) string
}

type ColumnKey string

type Row[K comparable, V any] struct {
	Key   K
	Value V
}

type RowRenderer[V any] func(V) RenderedRow

// RenderedRow provides the rendered string for each column in a row.
type RenderedRow map[ColumnKey]string

type SortFunc[V any] func(V, V) int

// Styles contains style definitions for this list component. By default, these
// values are generated by DefaultStyles.
type Styles struct {
	Header      lipgloss.Style
	Highlighted lipgloss.Style
	Selected    lipgloss.Style
}

// DefaultStyles returns a set of default style definitions for this table.
func DefaultStyles() Styles {
	return Styles{
		Highlighted: tui.Regular.Copy().
			Background(tui.HighlightBackground).
			Foreground(tui.HighlightForeground),
		Selected: tui.Regular.Copy().
			Background(tui.SelectedBackground).
			Foreground(tui.SelectedForeground),
		Header: tui.Regular.Copy().Padding(0, 1),
	}
}

// SetStyles sets the table styles.
func (m *Model[K, V]) SetStyles(s Styles) {
	m.styles = s
	m.UpdateViewport()
}

// New creates a new model for the table widget.
func New[K comparable, V any](columns []Column, fn RowRenderer[V], width, height int) Model[K, V] {
	filter := textinput.New()
	filter.Prompt = "Filter: "

	m := Model[K, V]{
		cursor:      0,
		viewport:    viewport.New(0, 0),
		rowRenderer: fn,
		styles:      DefaultStyles(),
		items:       make(map[K]V),
		Selected:    make(map[K]V),
		selectable:  true,
		focus:       true,
		filter:      filter,
	}
	// Deliberately use range to copy column structs onto receiver, because the
	// caller may be using columns in multiple tables and columns are modified
	// by each table.
	//
	// TODO: use copy, which is more explicit
	for _, col := range columns {
		// Set default truncation function if unset
		if col.TruncationFunc == nil {
			col.TruncationFunc = defaultTruncationFunc
		}
		m.cols = append(m.cols, col)
	}

	// Recalculates width of columns
	//
	// TODO: this also unnecessarily renders 0 rows
	m.setDimensions(width, height)

	return m
}

// WithSortFunc configures the table to sort rows using the given func.
func (m Model[K, V]) WithSortFunc(sortFunc func(V, V) int) Model[K, V] {
	m.sortFunc = sortFunc
	return m
}

func (m *Model[K, V]) filterVisible() bool {
	// Filter is visible if it's either in focus, or it has a non-empty value.
	return m.filter.Focused() || m.filter.Value() != ""
}

func (m *Model[K, V]) setDimensions(width, height int) {
	// Accommodate height of table header
	m.viewport.Height = height - headerHeight
	if m.filterVisible() {
		// Accommodate height of filter widget
		m.viewport.Height -= filterHeight
	}
	m.height = height

	// Set available width for table to expand into, whilst respecting a
	// minimum width of 80.
	m.width = max(80, width)
	m.viewport.Width = m.width
	m.recalculateWidth()

	// TODO: should this always be called?
	m.UpdateViewport()
}

// WithSelectable sets whether rows are selectable.
func (m Model[K, V]) WithSelectable(s bool) Model[K, V] {
	m.selectable = s
	return m
}

// Update is the Bubble Tea update loop.
func (m Model[K, V]) Update(msg tea.Msg) (Model[K, V], tea.Cmd) {
	if !m.focus {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Navigation.LineUp):
			m.MoveUp(1)
		case key.Matches(msg, keys.Navigation.LineDown):
			m.MoveDown(1)
		case key.Matches(msg, keys.Navigation.PageUp):
			m.MoveUp(m.viewport.Height)
		case key.Matches(msg, keys.Navigation.PageDown):
			m.MoveDown(m.viewport.Height)
		case key.Matches(msg, keys.Navigation.HalfPageUp):
			m.MoveUp(m.viewport.Height / 2)
		case key.Matches(msg, keys.Navigation.HalfPageDown):
			m.MoveDown(m.viewport.Height / 2)
		case key.Matches(msg, keys.Navigation.GotoTop):
			m.GotoTop()
		case key.Matches(msg, keys.Navigation.GotoBottom):
			m.GotoBottom()
		case key.Matches(msg, keys.Global.Select):
			m.ToggleSelection()
		case key.Matches(msg, keys.Global.SelectAll):
			m.ToggleSelectAll()
		case key.Matches(msg, keys.Global.SelectClear):
			m.DeselectAll()
		case key.Matches(msg, keys.Global.SelectRange):
			m.SelectRange()
		}
	case tea.WindowSizeMsg:
		m.setDimensions(msg.Width, msg.Height)
	case spinner.TickMsg:
		// Rows can contain spinners, so we re-render them whenever a tick is
		// received.
		m.UpdateViewport()
	case tui.FilterFocusReqMsg:
		// Focus the filter widget
		blink := m.filter.Focus()
		// Resize the viewport to accommodate the filter widget
		m.setDimensions(m.width, m.height)
		// Acknowledge the request, and start blinking the cursor.
		return m, tea.Batch(tui.CmdHandler(tui.FilterFocusAckMsg{}), blink)
	case tui.FilterBlurMsg:
		// Blur the filter widget
		m.filter.Blur()
		return m, nil
	case tui.FilterCloseMsg:
		// Close the filter widget
		m.filter.Blur()
		m.filter.SetValue("")
		// Unfilter table items
		m.SetItems(m.items)
		// Resize the viewport to take up the space now unoccupied
		m.setDimensions(m.width, m.height)
		return m, nil
	case tui.FilterKeyMsg:
		// unwrap key and send to filter widget
		kmsg := tea.KeyMsg(msg)
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(kmsg)
		// Filter table items
		m.SetItems(m.items)
		return m, cmd
	default:
		// Send any other messages to the filter if it is focused.
		if m.filter.Focused() {
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			return m, cmd
		}
	}

	return m, nil
}

// Focused returns the focus state of the table.
func (m Model[K, V]) Focused() bool {
	return m.focus
}

// Focus focuses the table, allowing the user to move around the rows and
// interact.
func (m *Model[K, V]) Focus() {
	m.focus = true
	m.UpdateViewport()
}

// Blur blurs the table, preventing selection or movement.
func (m *Model[K, V]) Blur() {
	m.focus = false
	m.UpdateViewport()
}

// View renders the component.
func (m Model[K, V]) View() string {
	components := make([]string, 0, 3)
	if m.filterVisible() {
		components = append(components, tui.Regular.Margin(0, 1).Render(m.filter.View()))
		components = append(components, strings.Repeat("─", m.width))
	}
	components = append(components, m.headersView())
	components = append(components, m.viewport.View())
	return lipgloss.JoinVertical(lipgloss.Top, components...)
}

// UpdateViewport updates the list content based on the previously defined
// columns and rows.
func (m *Model[K, V]) UpdateViewport() {
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

// currentRow returns the row on which the cursor currently sits. If the cursor
// is out of bounds then false is returned along with an empty row.
func (m Model[K, V]) currentRow() (Row[K, V], bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return *new(Row[K, V]), false
	}
	return m.rows[m.cursor], true
}

// Highlighted returns the currently highlighted entity.
//
// TODO: This is identical to currentRow above; remove.
func (m Model[K, V]) Highlighted() (Row[K, V], bool) {
	row, ok := m.currentRow()
	if !ok {
		return row, ok
	}
	return row, true
}

// Highlighted returns the currently highlighted entity key.
//
// TODO: rename currentRowKey or currentKey
func (m Model[K, V]) HighlightedKey() (K, bool) {
	row, ok := m.currentRow()
	if !ok {
		return *new(K), ok
	}
	return row.Key, true
}

// HighlightedOrSelected returns either the selected entities, if there are no
// selections, the currently highlighted entity.
func (m Model[K, V]) HighlightedOrSelected() []Row[K, V] {
	if len(m.Selected) > 0 {
		rows := make([]Row[K, V], len(m.Selected))
		var i int
		for k, v := range m.Selected {
			rows[i] = Row[K, V]{Key: k, Value: v}
			i++
		}
		return rows
	}
	if row, ok := m.Highlighted(); ok {
		return []Row[K, V]{row}
	}
	return nil
}

func (m Model[K, V]) HighlightedOrSelectedKeys() []K {
	if len(m.Selected) > 0 {
		return maps.Keys(m.Selected)
	}
	if row, ok := m.Highlighted(); ok {
		return []K{row.Key}
	}
	return nil
}

// ToggleSelection toggles the selection of the currently highlighted row.
//
// TODO: rename 'highlighted' to current
func (m *Model[K, V]) ToggleSelection() {
	if !m.selectable {
		return
	}
	current, ok := m.currentRow()
	if !ok {
		return
	}
	if _, isSelected := m.Selected[current.Key]; isSelected {
		delete(m.Selected, current.Key)
	} else {
		m.Selected[current.Key] = current.Value
	}
	m.UpdateViewport()
}

// ToggleSelectionByKey toggles the selection of the row with the given key. If
// the key does not exist no action is taken.
func (m *Model[K, V]) ToggleSelectionByKey(key K) {
	if !m.selectable {
		return
	}
	v, ok := m.items[key]
	if !ok {
		return
	}
	if _, isSelected := m.Selected[key]; isSelected {
		delete(m.Selected, key)
	} else {
		m.Selected[key] = v
	}
	m.UpdateViewport()
}

// ToggleSelectAll toggles the selection of all rows.
func (m *Model[K, V]) ToggleSelectAll() {
	if !m.selectable {
		return
	}
	if m.selectAll {
		m.DeselectAll()
		return
	}
	m.Selected = make(map[K]V, len(m.rows))
	for _, row := range m.rows {
		m.Selected[row.Key] = row.Value
	}
	m.selectAll = true
	m.UpdateViewport()
}

// DeselectAll de-selects any rows that are currently selected
func (m *Model[K, V]) DeselectAll() {
	if !m.selectable {
		return
	}

	m.Selected = make(map[K]V)
	m.selectAll = false
	m.UpdateViewport()
}

// SelectRange selects a range of rows. If the current row is *below* a selected
// row then rows between them are selected, including the current row.
// Otherwise, if the current row is *above* a selected row then rows between
// them are selected, including the current row. If there are no selected rows
// then no action is taken.
func (m *Model[K, V]) SelectRange() {
	if !m.selectable {
		return
	}
	if len(m.Selected) == 0 {
		return
	}
	// Determine the first row to select, and the number of rows to select.
	first := -1
	n := 0
	for i, row := range m.rows {
		if i == m.cursor && first > -1 && first < m.cursor {
			// Select rows before and including cursor
			n = m.cursor - first + 1
			break
		}
		if _, ok := m.Selected[row.Key]; !ok {
			// Ignore unselected rows
			continue
		}
		if i > m.cursor {
			// Select rows including cursor and all rows up to but not including
			// next selected row
			first = m.cursor
			n = i - m.cursor
			break
		}
		// Start selecting rows after this currently selected row.
		first = i + 1
	}
	for _, row := range m.rows[first : first+n] {
		m.Selected[row.Key] = row.Value
	}
	m.UpdateViewport()
}

// Items returns the current items. Note this is the number of items prior to
// any filtering.
func (m Model[K, V]) Items() map[K]V {
	return m.items
}

// TotalString returns a stringified representation of the total number of items
// in the table. If the table is filtered it is further broken down into number
// of filtered items as well as total items, formatted as
// "<filtered>/<total>".
func (m Model[K, V]) TotalString() string {
	if m.filterVisible() {
		return fmt.Sprintf("%d/%d", len(m.rows), len(m.items))
	}
	return fmt.Sprintf("%d", len(m.items))
}

// SetItems sets new items on the table, overwriting existing items.
func (m *Model[K, V]) SetItems(items map[K]V) {
	// Overwrite existing items
	m.items = items

	// Carry over existing selections.
	selections := make(map[K]V)

	// Overwrite existing rows
	m.rows = make([]Row[K, V], 0, len(items))
	// Convert items into rows, and carry across matching selections
	for id, it := range items {
		if m.filter.Value() != "" {
			// Filter rows using row renderer. If the filter value is a
			// substring of one of the rendered cells then add row. Otherwise,
			// skip row.
			//
			// NOTE: it is highly inefficient to render every row, every time
			// the user edits the filter value, particularly as the row renderer
			// can make data lookups on each invocation. But there is no obvious
			// alternative at present.
			filterMatch := func(v V) bool {
				for _, v := range m.rowRenderer(it) {
					// Remove ANSI escapes code before filtering
					v = internal.StripAnsi(v)
					if strings.Contains(v, m.filter.Value()) {
						return true
					}
				}
				return false
			}
			if !filterMatch(it) {
				// Skip item not matching filter
				continue
			}
		}
		m.rows = append(m.rows, Row[K, V]{Key: id, Value: it})
		if m.selectable {
			if _, ok := m.Selected[id]; ok {
				selections[id] = it
			}
		}
	}

	// Sort rows in-place
	if m.sortFunc != nil {
		slices.SortFunc(m.rows, func(i, j Row[K, V]) int {
			return m.sortFunc(i.Value, j.Value)
		})
	}

	// Overwrite existing selections, removing any that no longer have a
	// corresponding item.
	m.Selected = selections

	// Check if cursor is now out of bounds and if so set it to the last row.
	// This happens when rows are removed.
	if m.cursor > len(m.rows)-1 {
		m.cursor = max(0, len(m.rows)-1)
	}

	m.UpdateViewport()
}

// SetColumns sets a new columns state.
func (m *Model[K, V]) SetColumns(c []Column) {
	m.cols = c
	m.UpdateViewport()
}

// Height returns the viewport height of the table.
func (m Model[K, V]) Height() int {
	return m.viewport.Height
}

// Width returns the viewport width of the table.
func (m Model[K, V]) Width() int {
	return m.viewport.Width
}

// Cursor returns the index of the highlighted row.
func (m Model[K, V]) Cursor() int {
	return m.cursor
}

// SetCursor sets the cursor position in the table.
func (m *Model[K, V]) SetCursor(n int) {
	m.cursor = clamp(n, 0, len(m.rows)-1)
	m.UpdateViewport()
}

// MoveUp moves the highlightion up by any number of rows.
// It can not go above the first row.
func (m *Model[K, V]) MoveUp(n int) {
	m.cursor = clamp(m.cursor-n, 0, len(m.rows)-1)
	switch {
	case m.start == 0:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset, 0, m.cursor))
	case m.start < m.viewport.Height:
		//m.viewport.SetYOffset(clamp(clamp(m.viewport.YOffset+n, 0, m.cursor), 0, m.viewport.Height))
		m.viewport.YOffset = (clamp(clamp(m.viewport.YOffset+n, 0, m.cursor), 0, m.viewport.Height))
	case m.viewport.YOffset >= 1:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset+n, 1, m.viewport.Height))
	}
	m.UpdateViewport()
}

// MoveDown moves the highlightion down by any number of rows.
// It can not go below the last row.
func (m *Model[K, V]) MoveDown(n int) {
	m.cursor = clamp(m.cursor+n, 0, len(m.rows)-1)
	m.UpdateViewport()

	switch {
	case m.end == len(m.rows) && m.viewport.YOffset > 0:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset-n, 1, m.viewport.Height))
	case m.cursor > (m.end-m.start)/2 && m.viewport.YOffset > 0:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset-n, 1, m.cursor))
	case m.viewport.YOffset > 1:
	case m.cursor > m.viewport.YOffset+m.viewport.Height-1:
		m.viewport.SetYOffset(clamp(m.viewport.YOffset+1, 0, 1))
	}
}

// GotoTop moves the highlightion to the first row.
func (m *Model[K, V]) GotoTop() {
	m.MoveUp(m.cursor)
}

// GotoBottom moves the highlightion to the last row.
func (m *Model[K, V]) GotoBottom() {
	m.MoveDown(len(m.rows))
}

func (m Model[K, V]) headersView() string {
	var s = make([]string, 0, len(m.cols))
	for _, col := range m.cols {
		style := lipgloss.NewStyle().Width(col.Width).MaxWidth(col.Width).Inline(true)
		renderedCell := style.Render(runewidth.Truncate(col.Title, col.Width, "…"))
		s = append(s, m.styles.Header.Render(renderedCell))
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, s...)
}

func (m *Model[K, V]) renderRow(rowIdx int) string {
	row := m.rows[rowIdx]

	var (
		background  lipgloss.Color
		foreground  lipgloss.Color
		highlighted bool
		selected    bool
	)
	if _, ok := m.Selected[row.Key]; ok {
		selected = true
	}
	if rowIdx == m.cursor {
		highlighted = true
	}
	if highlighted && selected {
		background = tui.HighlightedAndSelectedBackground
		foreground = tui.HighlightedAndSelectedForeground
	} else if highlighted {
		background = tui.HighlightBackground
		foreground = tui.HighlightForeground
	} else if selected {
		background = tui.SelectedBackground
		foreground = tui.SelectedForeground
	}

	var renderedCells = make([]string, len(m.cols))
	cells := m.rowRenderer(row.Value)
	for i, col := range m.cols {
		content := cells[col.Key]
		// Truncate content if it is wider than column
		truncated := col.TruncationFunc(content, col.Width, "…")
		// Ensure content is all on one line.
		inlined := lipgloss.NewStyle().
			Width(col.Width).
			MaxWidth(col.Width).
			Inline(true).
			Render(truncated)
		// Apply block-styling to content
		boxed := lipgloss.NewStyle().
			Padding(0, 1).
			Render(inlined)
		renderedCells[i] = boxed
	}

	// Join cells together to form a row
	renderedRow := lipgloss.JoinHorizontal(lipgloss.Left, renderedCells...)

	// If highlighted and/or selected, strip colors and apply background color
	if highlighted || selected {
		renderedRow = internal.StripAnsi(renderedRow)
		renderedRow = lipgloss.NewStyle().
			Foreground(foreground).
			Background(background).
			Render(renderedRow)
	}
	return renderedRow
}

func clamp(v, low, high int) int {
	return min(max(v, low), high)
}
