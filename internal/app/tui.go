package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type tuiScreen int

const (
	tuiMenu tuiScreen = iota
	tuiContent
	tuiFilters
	tuiPreview
	tuiURL
	tuiResult
)

var tuiActions = []string{
	"Content browser",
	"Build site",
	"Run site doctor",
	"Audit SEO",
	"Audit links",
}

var tuiDraftFilters = []string{"All content", "Drafts only", "Published only"}

type tuiInput int

const (
	tuiNoInput tuiInput = iota
	tuiSearchInput
	tuiSectionInput
	tuiCategoryInput
	tuiTagInput
	tuiFromInput
	tuiToInput
)

type tuiCommandResult struct {
	output string
}

type tuiSpinnerTick struct{}

type tuiModel struct {
	project       string
	screen        tuiScreen
	menuCursor    int
	contentCursor int
	draftFilter   int
	query         string
	section       string
	category      string
	tag           string
	from          string
	to            string
	filterCursor  int
	input         tuiInput
	items         []contentItem
	err           error
	result        string
	command       string
	resultOffset  int
	previewSource string
	previewText   string
	previewOffset int
	selectedURL   string
	urlNote       string
	height        int
	running       bool
	spinner       int
}

// runTUI starts the interactive terminal interface. It deliberately delegates
// work to the same command handlers used by the non-interactive CLI.
func runTUI(args []string, out io.Writer) error {
	project, err := tuiProject(args)
	if err != nil {
		return err
	}
	_ = out
	_, err = tea.NewProgram(newTUIModel(project), tea.WithAltScreen()).Run()
	return err
}

func tuiProject(args []string) (string, error) {
	if len(args) > 1 || (len(args) == 1 && strings.HasPrefix(args[0], "-")) {
		return "", fmt.Errorf("usage: hs tui [project-directory]")
	}
	if len(args) == 0 {
		return os.Getwd()
	}
	project, err := filepath.Abs(args[0])
	if err != nil {
		return "", err
	}
	info, err := os.Stat(project)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory: %s", project)
	}
	return project, nil
}

func newTUIModel(project string) tuiModel {
	return tuiModel{project: project, height: 24}
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case tuiCommandResult:
		m.running = false
		m.result = msg.output
		return m, nil
	case tuiSpinnerTick:
		if m.running {
			m.spinner = (m.spinner + 1) % 4
			return m, tuiSpinnerTickCmd()
		}
		return m, nil
	case tea.KeyMsg:
		key := msg.String()
		if key == "ctrl+c" || key == "q" && m.input == tuiNoInput {
			return m, tea.Quit
		}
		switch m.screen {
		case tuiMenu:
			return m.updateMenu(key)
		case tuiContent:
			return m.updateContent(key)
		case tuiFilters:
			return m.updateFilters(key)
		case tuiPreview:
			return m.updatePreview(key)
		case tuiURL:
			return m.updateURL(key)
		case tuiResult:
			return m.updateResult(key)
		}
	}
	return m, nil
}

func (m tuiModel) updateMenu(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		m.menuCursor = (m.menuCursor + len(tuiActions) - 1) % len(tuiActions)
	case "down", "j":
		m.menuCursor = (m.menuCursor + 1) % len(tuiActions)
	case "home":
		m.menuCursor = 0
	case "end":
		m.menuCursor = len(tuiActions) - 1
	case "pgup":
		m.menuCursor = max(0, m.menuCursor-5)
	case "pgdown", "space", " ":
		m.menuCursor = min(len(tuiActions)-1, m.menuCursor+5)
	case "enter":
		if m.menuCursor == 0 {
			m.loadContent()
			m.screen = tuiContent
			return m, nil
		}
		return m, m.startAction(m.menuActionArgs())
	}
	return m, nil
}

func (m tuiModel) updateContent(key string) (tea.Model, tea.Cmd) {
	if m.input == tuiSearchInput {
		m.updateInput(key)
		m.contentCursor = 0
		return m, nil
	}
	items := m.filteredItems()
	switch key {
	case "up", "k":
		if len(items) > 0 {
			m.contentCursor = (m.contentCursor + len(items) - 1) % len(items)
		}
	case "down", "j":
		if len(items) > 0 {
			m.contentCursor = (m.contentCursor + 1) % len(items)
		}
	case "home":
		m.contentCursor = 0
	case "end":
		if len(items) > 0 {
			m.contentCursor = len(items) - 1
		}
	case "pgup":
		m.contentCursor = max(0, m.contentCursor-m.contentHeight())
	case "pgdown", "space", " ":
		if len(items) > 0 {
			m.contentCursor = min(len(items)-1, m.contentCursor+m.contentHeight())
		}
	case "r":
		m.loadContent()
	case "enter":
		if len(items) > 0 {
			m.openContent(items[m.contentCursor])
		}
	case "d":
		if len(items) > 0 {
			return m, m.startAction([]string{"doctor", m.project, "--only", "content", "--source", items[m.contentCursor].Source})
		}
	case "u":
		if len(items) > 0 {
			m.showContentURL(items[m.contentCursor])
			m.screen = tuiURL
		}
	case "/":
		m.input = tuiSearchInput
	case "f":
		m.screen = tuiFilters
		m.filterCursor = 0
	case "c":
		m.clearFilters()
	case "esc", "backspace":
		m.screen = tuiMenu
	}
	return m, nil
}

func (m tuiModel) updatePreview(key string) (tea.Model, tea.Cmd) {
	lines := strings.Split(m.previewText, "\n")
	maxOffset := max(0, len(lines)-m.previewHeight())
	switch key {
	case "up", "k":
		m.previewOffset = max(0, m.previewOffset-1)
	case "down", "j":
		m.previewOffset = min(maxOffset, m.previewOffset+1)
	case "home":
		m.previewOffset = 0
	case "end":
		m.previewOffset = maxOffset
	case "pgup":
		m.previewOffset = max(0, m.previewOffset-m.previewHeight())
	case "pgdown", "space", " ":
		m.previewOffset = min(maxOffset, m.previewOffset+m.previewHeight())
	case "esc", "backspace", "enter":
		m.screen = tuiContent
	}
	return m, nil
}

func (m tuiModel) updateURL(key string) (tea.Model, tea.Cmd) {
	if key == "esc" || key == "backspace" || key == "enter" {
		m.screen = tuiContent
	}
	return m, nil
}

func (m tuiModel) updateFilters(key string) (tea.Model, tea.Cmd) {
	if m.input != tuiNoInput {
		m.updateInput(key)
		return m, nil
	}
	switch key {
	case "up", "k":
		m.filterCursor = (m.filterCursor + tuiFilterCount - 1) % tuiFilterCount
	case "down", "j":
		m.filterCursor = (m.filterCursor + 1) % tuiFilterCount
	case "home":
		m.filterCursor = 0
	case "end":
		m.filterCursor = tuiFilterCount - 1
	case "pgup":
		m.filterCursor = max(0, m.filterCursor-5)
	case "pgdown", "space", " ":
		m.filterCursor = min(tuiFilterCount-1, m.filterCursor+5)
	case "left", "h":
		m.cycleFilter(-1)
	case "right", "l":
		m.cycleFilter(1)
	case "enter":
		if m.filterCursor == 0 {
			m.cycleFilter(1)
		} else {
			m.input = tuiInput(m.filterCursor + 1)
		}
	case "c":
		m.clearFilters()
	case "esc", "backspace":
		m.screen = tuiContent
		m.contentCursor = 0
	}
	return m, nil
}

const tuiFilterCount = 6

func (m *tuiModel) cycleFilter(step int) {
	switch m.filterCursor {
	case 0:
		m.draftFilter = (m.draftFilter + step + len(tuiDraftFilters)) % len(tuiDraftFilters)
	case 1:
		m.section = cycleTUIFilterValue(m.section, m.filterValues(func(item contentItem) []string { return []string{item.Section} }), step)
	case 2:
		m.category = cycleTUIFilterValue(m.category, m.filterValues(func(item contentItem) []string { return item.Categories }), step)
	case 3:
		m.tag = cycleTUIFilterValue(m.tag, m.filterValues(func(item contentItem) []string { return item.Tags }), step)
	}
	m.contentCursor = 0
}

func (m tuiModel) filterValues(values func(contentItem) []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, item := range m.items {
		for _, value := range values(item) {
			if value != "" && !seen[strings.ToLower(value)] {
				seen[strings.ToLower(value)] = true
				result = append(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}

func cycleTUIFilterValue(current string, values []string, step int) string {
	options := append([]string{""}, values...)
	index := 0
	for i, value := range options {
		if strings.EqualFold(value, current) {
			index = i
			break
		}
	}
	return options[(index+step+len(options))%len(options)]
}

func (m *tuiModel) updateInput(key string) {
	switch key {
	case "enter":
		m.input = tuiNoInput
		m.contentCursor = 0
	case "esc":
		m.input = tuiNoInput
	case "backspace":
		value := []rune(m.inputValue())
		if len(value) > 0 {
			m.setInputValue(string(value[:len(value)-1]))
		}
	default:
		if len([]rune(key)) == 1 {
			m.setInputValue(m.inputValue() + key)
		}
	}
}

func (m tuiModel) inputValue() string {
	switch m.input {
	case tuiSearchInput:
		return m.query
	case tuiSectionInput:
		return m.section
	case tuiCategoryInput:
		return m.category
	case tuiTagInput:
		return m.tag
	case tuiFromInput:
		return m.from
	case tuiToInput:
		return m.to
	}
	return ""
}

func (m *tuiModel) setInputValue(value string) {
	switch m.input {
	case tuiSearchInput:
		m.query = value
	case tuiSectionInput:
		m.section = value
	case tuiCategoryInput:
		m.category = value
	case tuiTagInput:
		m.tag = value
	case tuiFromInput:
		m.from = value
	case tuiToInput:
		m.to = value
	}
}

func (m tuiModel) updateResult(key string) (tea.Model, tea.Cmd) {
	if m.running {
		return m, nil
	}
	lines := strings.Split(m.result, "\n")
	maxOffset := max(0, len(lines)-m.resultHeight())
	switch key {
	case "up", "k":
		m.resultOffset = max(0, m.resultOffset-1)
	case "down", "j":
		m.resultOffset = min(maxOffset, m.resultOffset+1)
	case "home":
		m.resultOffset = 0
	case "end":
		m.resultOffset = maxOffset
	case "pgup":
		m.resultOffset = max(0, m.resultOffset-m.resultHeight())
	case "pgdown", "space", " ":
		m.resultOffset = min(maxOffset, m.resultOffset+m.resultHeight())
	case "esc", "backspace", "enter":
		m.screen = tuiMenu
	}
	return m, nil
}

func (m *tuiModel) loadContent() {
	m.items, m.err = collectContent(m.project)
	m.contentCursor = 0
}

func (m *tuiModel) openContent(item contentItem) {
	m.previewSource = item.Source
	m.previewOffset = 0
	data, err := os.ReadFile(filepath.Join(m.project, filepath.FromSlash(item.Source)))
	if err != nil {
		m.previewText = fmt.Sprintf("Could not read %s: %v", item.Source, err)
	} else {
		m.previewText = string(data)
	}
	m.screen = tuiPreview
}

func (m *tuiModel) showContentURL(item contentItem) {
	files, err := hugoConfigFiles(m.project)
	if err != nil {
		m.selectedURL = item.GeneratedURL
		m.urlNote = fmt.Sprintf("Could not read the Hugo baseURL: %v", err)
		return
	}
	baseURL := strings.TrimRight(doctorBaseURL(files), "/")
	if baseURL == "" {
		m.selectedURL = item.GeneratedURL
		m.urlNote = "No baseURL is configured, so this is the generated page path."
		return
	}
	m.selectedURL = baseURL + "/" + strings.TrimLeft(item.GeneratedURL, "/")
	m.urlNote = "Built from the Hugo baseURL and generated page path."
}

func (m tuiModel) filteredItems() []contentItem {
	filtered := make([]contentItem, 0, len(m.items))
	from, to := parseDate(m.from), parseDate(m.to)
	for _, item := range m.items {
		haystack := strings.ToLower(strings.Join([]string{item.Title, strings.Join(item.Tags, " "), item.Body}, " "))
		if m.draftFilter == 1 && !item.Draft ||
			m.draftFilter == 2 && item.Draft ||
			m.query != "" && !containsAll(haystack, strings.ToLower(m.query)) ||
			m.section != "" && !strings.EqualFold(item.Section, m.section) ||
			m.category != "" && !hasString(item.Categories, m.category) ||
			m.tag != "" && !hasString(item.Tags, m.tag) ||
			!from.IsZero() && (item.Date.IsZero() || item.Date.Before(from)) ||
			!to.IsZero() && (item.Date.IsZero() || item.Date.After(to)) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func (m *tuiModel) clearFilters() {
	m.draftFilter = 0
	m.query = ""
	m.section = ""
	m.category = ""
	m.tag = ""
	m.from = ""
	m.to = ""
	m.contentCursor = 0
}

func (m *tuiModel) startAction(args []string) tea.Cmd {
	m.command = "hs " + strings.Join(args, " ")
	m.result = ""
	m.resultOffset = 0
	m.running = true
	m.spinner = 0
	m.screen = tuiResult
	return tea.Batch(tuiSpinnerTickCmd(), func() tea.Msg {
		var stdout, stderr bytes.Buffer
		err := run(args, &stdout, &stderr)
		result := strings.TrimSpace(stdout.String())
		if stderr.Len() > 0 {
			result = strings.TrimSpace(result + "\n" + stderr.String())
		}
		if err != nil {
			result = strings.TrimSpace(result + "\nerror: " + err.Error())
		}
		if result == "" {
			result = "Command completed without output."
		}
		return tuiCommandResult{output: result}
	})
}

func (m tuiModel) menuActionArgs() []string {
	var args []string
	switch m.menuCursor {
	case 1:
		args = []string{"build", m.project}
	case 2:
		args = []string{"doctor", m.project}
	case 3:
		args = []string{"audit", "seo", m.project}
	case 4:
		args = []string{"audit", "links", m.project}
	}
	return args
}

func tuiSpinnerTickCmd() tea.Cmd {
	return tea.Tick(125*time.Millisecond, func(time.Time) tea.Msg { return tuiSpinnerTick{} })
}

func (m tuiModel) View() string {
	switch m.screen {
	case tuiContent:
		return m.contentView()
	case tuiFilters:
		return m.filtersView()
	case tuiPreview:
		return m.previewView()
	case tuiURL:
		return m.urlView()
	case tuiResult:
		return m.resultView()
	default:
		return m.menuView()
	}
}

func (m tuiModel) menuView() string {
	var view strings.Builder
	fmt.Fprintf(&view, "hs | Hugo site tools\n%s\n\n", m.project)
	view.WriteString("Choose an action\n\n")
	for i, action := range tuiActions {
		marker := "  "
		if i == m.menuCursor {
			marker = "> "
		}
		fmt.Fprintf(&view, "%s%s\n", marker, action)
	}
	view.WriteString("\n↑/↓ select • Enter open • q quit\n")
	return view.String()
}

func (m tuiModel) contentView() string {
	var view strings.Builder
	items := m.filteredItems()
	stats := makeContentStats(items)
	fmt.Fprintf(&view, "Content browser | %s\n", m.filterDescription())
	if m.input == tuiSearchInput {
		fmt.Fprintf(&view, "Search: %s█\n", m.query)
	} else if m.query != "" {
		fmt.Fprintf(&view, "Search: %s\n", m.query)
	}
	view.WriteString("/ search • f filters • c clear • ↑/↓ select • PgUp/PgDn scroll • Enter view • d doctor • u URL • r refresh • Esc back\n\n")
	if m.err != nil {
		fmt.Fprintf(&view, "Could not read content: %v\n", m.err)
		return view.String()
	}
	fmt.Fprintf(&view, "Posts: %d | Words: %d | Avg: %d words/post\n", stats.Posts, stats.TotalWords, stats.AverageWords)
	if stats.FirstPublished != "" {
		if stats.AverageInterval != "" {
			fmt.Fprintf(&view, "Cadence: %s | Published: %s to %s\n", stats.AverageInterval, stats.FirstPublished, stats.LatestPublished)
		} else {
			fmt.Fprintf(&view, "Published: %s to %s\n", stats.FirstPublished, stats.LatestPublished)
		}
	}
	view.WriteByte('\n')
	if len(items) == 0 {
		view.WriteString("No matching content.\n")
		return view.String()
	}
	start := max(0, m.contentCursor-(m.contentHeight()/2))
	end := min(len(items), start+m.contentHeight())
	for i := start; i < end; i++ {
		item := items[i]
		marker := "  "
		if i == m.contentCursor {
			marker = "> "
		}
		date := "          "
		if !item.Date.IsZero() {
			date = item.Date.Format("2006-01-02")
		}
		state := "published"
		if item.Draft {
			state = "draft"
		}
		fmt.Fprintf(&view, "%s%s  %-9s %-12s %5d words  %s\n", marker, date, state, item.Section, item.Words, item.Title)
	}
	view.WriteString(fmt.Sprintf("\n%d item(s)\n", len(items)))
	return view.String()
}

func (m tuiModel) previewView() string {
	var view strings.Builder
	fmt.Fprintf(&view, "%s\n\n", m.previewSource)
	lines := strings.Split(m.previewText, "\n")
	end := min(len(lines), m.previewOffset+m.previewHeight())
	for _, line := range lines[m.previewOffset:end] {
		view.WriteString(line + "\n")
	}
	view.WriteString("\n↑/↓ scroll • PgUp/PgDn or Space page • Home/End jump • Enter or Esc back\n")
	return view.String()
}

func (m tuiModel) urlView() string {
	return fmt.Sprintf("Post URL\n\n%s\n\n%s\nCopy the URL, then press Enter or Esc to return.\n", m.selectedURL, m.urlNote)
}

func (m tuiModel) filtersView() string {
	var view strings.Builder
	view.WriteString("Content filters\n\n")
	labels := []string{"Draft state", "Section", "Category", "Tag", "From date", "To date"}
	values := []string{tuiDraftFilters[m.draftFilter], m.section, m.category, m.tag, m.from, m.to}
	for i, label := range labels {
		marker := "  "
		if i == m.filterCursor {
			marker = "> "
		}
		value := values[i]
		if value == "" {
			value = "Any"
		}
		if m.input == tuiInput(i+1) {
			value += "█"
		}
		fmt.Fprintf(&view, "%s%-12s %s\n", marker, label+":", value)
	}
	view.WriteString("\n↑/↓ select • PgUp/PgDn page • Home/End jump • ←/→ choose values • Enter type • c clear • Esc apply\n")
	view.WriteString("Dates use YYYY-MM-DD. Enter a field to type an exact value.\n")
	return view.String()
}

func (m tuiModel) filterDescription() string {
	parts := []string{tuiDraftFilters[m.draftFilter]}
	if m.section != "" {
		parts = append(parts, "section="+m.section)
	}
	if m.category != "" {
		parts = append(parts, "category="+m.category)
	}
	if m.tag != "" {
		parts = append(parts, "tag="+m.tag)
	}
	if m.from != "" {
		parts = append(parts, "from="+m.from)
	}
	if m.to != "" {
		parts = append(parts, "to="+m.to)
	}
	return strings.Join(parts, ", ")
}

func (m tuiModel) resultView() string {
	var view strings.Builder
	fmt.Fprintf(&view, "%s\n\n", m.command)
	if m.running {
		frames := []string{"⠋", "⠙", "⠹", "⠸"}
		fmt.Fprintf(&view, "%s Running. This may take a moment.\n", frames[m.spinner])
		view.WriteString("The result will appear here when the command finishes.\n")
		return view.String()
	}
	lines := strings.Split(m.result, "\n")
	end := min(len(lines), m.resultOffset+m.resultHeight())
	for _, line := range lines[m.resultOffset:end] {
		view.WriteString(line + "\n")
	}
	view.WriteString("\n↑/↓ scroll • PgUp/PgDn or Space page • Home/End jump • Enter or Esc back • q quit\n")
	return view.String()
}

func (m tuiModel) contentHeight() int { return max(4, m.height-11) }
func (m tuiModel) resultHeight() int  { return max(4, m.height-5) }
func (m tuiModel) previewHeight() int { return max(4, m.height-5) }
