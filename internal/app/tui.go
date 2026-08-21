package app

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
	tuiCampaign
	tuiCampaigns
	tuiCampaignAdd
	tuiCampaignRetire
	tuiResult
)

var tuiActions = []string{
	"Content browser",
	"Build site",
	"Run site doctor",
	"Audit SEO",
	"Audit links",
	"Campaigns",
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
	tuiCampaignContentInput
	tuiCampaignKeyInput
	tuiCampaignLabelInput
	tuiCampaignDescriptionInput
)

type tuiCommandResult struct {
	output string
}

type tuiResultSource struct {
	path string
	line int
}

type tuiResultLine struct {
	text        string
	sourceLine  int
	firstOfLine bool
}

var tuiFindingSource = regexp.MustCompile(`(?m)^(?:WARNING|ERROR|INFO)\s+HS-[^\s]+\s+([^:\s]+\.(?:md|markdown|html|gohtml)):(\d+):`)

type tuiRemoteContentResult struct {
	items []contentItem
	err   error
}

type tuiSpinnerTick struct{}

type tuiModel struct {
	project            string
	remoteURL          string
	screen             tuiScreen
	menuCursor         int
	contentCursor      int
	draftFilter        int
	query              string
	section            string
	category           string
	tag                string
	from               string
	to                 string
	filterCursor       int
	input              tuiInput
	items              []contentItem
	err                error
	result             string
	command            string
	resultOffset       int
	resultFilter       string
	resultCursor       int
	previewSource      string
	previewText        string
	previewOffset      int
	previewReturn      tuiScreen
	resultSources      []tuiResultSource
	resultSourceCursor int
	selectedURL        string
	urlNote            string
	campaignItem       contentItem
	campaignPolicy     campaignPolicy
	campaignField      int
	campaignKey        string
	campaignSource     string
	campaignMedium     string
	campaignContent    string
	campaignCursor     int
	campaignAddField   int
	campaignDraftKey   string
	campaignDraftLabel string
	campaignDraftDesc  string
	campaignMessage    string
	height             int
	width              int
	running            bool
	spinner            int
}

// runTUI starts the interactive terminal interface. It deliberately delegates
// work to the same command handlers used by the non-interactive CLI.
func runTUI(args []string, out io.Writer) error {
	project, remoteURL, err := tuiTarget(args)
	if err != nil {
		return err
	}
	_ = out
	model := newTUIModel(project)
	if remoteURL != "" {
		model = newRemoteTUIModel(remoteURL)
	}
	_, err = tea.NewProgram(model, tea.WithAltScreen()).Run()
	return err
}

func tuiTarget(args []string) (project, remoteURL string, err error) {
	if len(args) >= 1 && args[0] == "--remote" {
		if len(args) != 2 {
			return "", "", fmt.Errorf("usage: hs tui [project-directory] | hs tui --remote <base-url>")
		}
		remoteURL, err = normalizeBaseURL(args[1])
		return "", remoteURL, err
	}
	project, err = tuiProject(args)
	return project, "", err
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
	return tuiModel{project: project, height: 24, width: 100}
}

func newRemoteTUIModel(baseURL string) tuiModel {
	return tuiModel{remoteURL: baseURL, screen: tuiContent, height: 24, width: 100}
}

func (m tuiModel) Init() tea.Cmd {
	if m.remoteURL != "" {
		return m.loadRemoteContentCmd()
	}
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		m.width = msg.Width
		return m, nil
	case tuiCommandResult:
		m.running = false
		m.result = msg.output
		m.resultFilter = "all"
		m.refreshResultSources()
		m.resultCursor = firstResultFindingLine(m.filteredResult())
		return m, nil
	case tuiRemoteContentResult:
		m.items, m.err = msg.items, msg.err
		m.contentCursor = 0
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
		case tuiCampaign:
			return m.updateCampaign(key)
		case tuiCampaigns:
			return m.updateCampaigns(key)
		case tuiCampaignAdd:
			return m.updateCampaignAdd(key)
		case tuiCampaignRetire:
			return m.updateCampaignRetire(key)
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
		if m.menuCursor == 5 {
			m.openCampaigns()
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
		if m.remoteURL != "" {
			return m, m.loadRemoteContentCmd()
		}
		m.loadContent()
	case "enter":
		if len(items) > 0 {
			m.openContent(items[m.contentCursor])
		}
	case "d":
		if len(items) > 0 {
			if m.remoteURL != "" {
				return m, m.startAction([]string{"doctor", "--remote", m.remoteURL})
			}
			return m, m.startAction([]string{"doctor", m.project, "--only", "content", "--source", items[m.contentCursor].Source})
		}
	case "l":
		if len(items) > 0 && m.remoteURL == "" {
			m.openCampaign(items[m.contentCursor])
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

func (m *tuiModel) openCampaign(item contentItem) {
	policy, err := loadCampaignPolicy(m.project)
	if err != nil {
		m.selectedURL, m.urlNote, m.screen = "", fmt.Sprintf("Could not load campaign policy: %v", err), tuiURL
		return
	}
	if _, err := campaignBaseURL(m.project); err != nil {
		m.selectedURL, m.urlNote, m.screen = "", err.Error(), tuiURL
		return
	}
	m.campaignKey = ""
	for _, campaign := range policy.Campaigns {
		if campaign.Status == "active" {
			m.campaignKey = campaign.Key
			break
		}
	}
	if m.campaignKey == "" || len(policy.Sources) == 0 || len(policy.Sources[0].AllowedMediums) == 0 {
		m.selectedURL, m.urlNote, m.screen = "", "Add an active campaign and an approved source before creating a campaign link.", tuiURL
		return
	}
	m.campaignItem, m.campaignPolicy, m.campaignField = item, policy, 0
	m.campaignSource, m.campaignMedium, m.campaignContent = policy.Sources[0].Key, policy.Sources[0].AllowedMediums[0], ""
	m.screen = tuiCampaign
}

func (m tuiModel) updateCampaign(key string) (tea.Model, tea.Cmd) {
	if m.input == tuiCampaignContentInput {
		m.updateInput(key)
		return m, nil
	}
	switch key {
	case "up", "k":
		m.campaignField = (m.campaignField + 3) % 4
	case "down", "j":
		m.campaignField = (m.campaignField + 1) % 4
	case "left", "h":
		if m.campaignField < 3 {
			m.cycleCampaignValue(-1)
		}
	case "right", "l":
		if m.campaignField < 3 {
			m.cycleCampaignValue(1)
		}
	case "enter":
		if m.campaignField == 3 {
			m.input = tuiCampaignContentInput
			return m, nil
		}
		result, err := createCampaignLink(m.project, m.campaignItem.Source, m.campaignKey, m.campaignSource, m.campaignMedium, m.campaignContent)
		if err != nil {
			m.selectedURL, m.urlNote = "", err.Error()
		} else {
			m.selectedURL = result.URL
			m.urlNote = fmt.Sprintf("Expected GA4 channel: %s. Copy the URL, then press Enter or Esc to return.", result.ExpectedChannel)
		}
		m.screen = tuiURL
	case "esc", "backspace":
		m.screen = tuiContent
	}
	return m, nil
}

func (m *tuiModel) openCampaigns() {
	policy, err := loadCampaignPolicy(m.project)
	if err != nil && strings.Contains(err.Error(), "campaign policy is missing") {
		var out bytes.Buffer
		if initErr := run([]string{"campaign", "init", m.project}, &out, &bytes.Buffer{}); initErr != nil {
			m.campaignMessage = fmt.Sprintf("Could not create campaign policy: %v", initErr)
			m.screen = tuiCampaigns
			return
		}
		policy, err = loadCampaignPolicy(m.project)
	}
	if err != nil {
		m.campaignMessage = fmt.Sprintf("Could not load campaign policy: %v", err)
		m.screen = tuiCampaigns
		return
	}
	m.campaignPolicy, m.campaignCursor, m.campaignMessage = policy, 0, ""
	m.screen = tuiCampaigns
}

func (m tuiModel) updateCampaigns(key string) (tea.Model, tea.Cmd) {
	if len(m.campaignPolicy.Campaigns) > 0 {
		switch key {
		case "up", "k":
			m.campaignCursor = (m.campaignCursor + len(m.campaignPolicy.Campaigns) - 1) % len(m.campaignPolicy.Campaigns)
		case "down", "j":
			m.campaignCursor = (m.campaignCursor + 1) % len(m.campaignPolicy.Campaigns)
		case "home":
			m.campaignCursor = 0
		case "end":
			m.campaignCursor = len(m.campaignPolicy.Campaigns) - 1
		}
	}
	switch key {
	case "a":
		m.campaignDraftKey, m.campaignDraftLabel, m.campaignDraftDesc = "", "", ""
		m.campaignAddField, m.campaignMessage, m.screen = 0, "", tuiCampaignAdd
	case "r":
		if len(m.campaignPolicy.Campaigns) > 0 {
			selected := m.campaignPolicy.Campaigns[m.campaignCursor]
			if selected.Status == "active" {
				m.screen = tuiCampaignRetire
			} else {
				m.campaignMessage = "That campaign is already retired."
			}
		}
	case "enter", "esc", "backspace":
		m.screen = tuiMenu
	}
	return m, nil
}

func (m tuiModel) updateCampaignAdd(key string) (tea.Model, tea.Cmd) {
	if m.input != tuiNoInput {
		m.updateInput(key)
		return m, nil
	}
	switch key {
	case "up", "k":
		m.campaignAddField = (m.campaignAddField + 2) % 3
	case "down", "j":
		m.campaignAddField = (m.campaignAddField + 1) % 3
	case "enter":
		m.input = tuiCampaignKeyInput + tuiInput(m.campaignAddField)
	case "s":
		var out bytes.Buffer
		err := run([]string{"campaign", "add", m.campaignDraftKey, m.project, "--label", m.campaignDraftLabel, "--description", m.campaignDraftDesc}, &out, &bytes.Buffer{})
		if err != nil {
			m.campaignMessage = err.Error()
			return m, nil
		}
		m.openCampaigns()
		m.campaignMessage = strings.TrimSpace(out.String())
	case "esc", "backspace":
		m.screen = tuiCampaigns
	}
	return m, nil
}

func (m tuiModel) updateCampaignRetire(key string) (tea.Model, tea.Cmd) {
	if key == "esc" || key == "backspace" {
		m.screen = tuiCampaigns
		return m, nil
	}
	if key != "enter" || len(m.campaignPolicy.Campaigns) == 0 {
		return m, nil
	}
	selected := m.campaignPolicy.Campaigns[m.campaignCursor]
	var out bytes.Buffer
	err := run([]string{"campaign", "retire", selected.Key, m.project}, &out, &bytes.Buffer{})
	if err != nil {
		m.campaignMessage, m.screen = err.Error(), tuiCampaigns
		return m, nil
	}
	m.openCampaigns()
	m.campaignMessage = strings.TrimSpace(out.String())
	return m, nil
}

func (m *tuiModel) cycleCampaignValue(step int) {
	if m.campaignField == 0 {
		var keys []string
		for _, campaign := range m.campaignPolicy.Campaigns {
			if campaign.Status == "active" {
				keys = append(keys, campaign.Key)
			}
		}
		m.campaignKey = cycleTUIFilterValue(m.campaignKey, keys, step)
		return
	}
	if m.campaignField == 1 {
		keys := make([]string, 0, len(m.campaignPolicy.Sources))
		for _, source := range m.campaignPolicy.Sources {
			keys = append(keys, source.Key)
		}
		m.campaignSource = cycleTUIFilterValue(m.campaignSource, keys, step)
		source := findCampaignSource(m.campaignPolicy, m.campaignSource)
		if source != nil && !hasString(source.AllowedMediums, m.campaignMedium) {
			m.campaignMedium = source.AllowedMediums[0]
		}
		return
	}
	source := findCampaignSource(m.campaignPolicy, m.campaignSource)
	if source != nil {
		m.campaignMedium = cycleTUIFilterValue(m.campaignMedium, source.AllowedMediums, step)
	}
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
		m.screen = m.previewReturn
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
	case tuiCampaignContentInput:
		return m.campaignContent
	case tuiCampaignKeyInput:
		return m.campaignDraftKey
	case tuiCampaignLabelInput:
		return m.campaignDraftLabel
	case tuiCampaignDescriptionInput:
		return m.campaignDraftDesc
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
	case tuiCampaignContentInput:
		m.campaignContent = value
	case tuiCampaignKeyInput:
		m.campaignDraftKey = value
	case tuiCampaignLabelInput:
		m.campaignDraftLabel = value
	case tuiCampaignDescriptionInput:
		m.campaignDraftDesc = value
	}
}

func (m tuiModel) updateResult(key string) (tea.Model, tea.Cmd) {
	if m.running {
		return m, nil
	}
	switch key {
	case "up", "k":
		m.moveResultFinding(-1)
	case "down", "j":
		m.moveResultFinding(1)
	case "home":
		m.resultCursor = firstResultFindingLine(m.filteredResult())
	case "end":
		lines := resultFindingLines(m.filteredResult())
		if len(lines) > 0 {
			m.resultCursor = lines[len(lines)-1]
		}
	case "pgup":
		m.moveResultFinding(-m.resultHeight())
	case "pgdown", "space", " ":
		m.moveResultFinding(m.resultHeight())
	case "a":
		m.setResultFilter("all")
	case "w":
		m.setResultFilter("warning")
	case "e":
		m.setResultFilter("error")
	case "i":
		m.setResultFilter("info")
	case "v":
		m.openResultSource()
	case "esc", "backspace", "enter":
		m.screen = tuiMenu
	}
	m.ensureResultCursorVisible()
	return m, nil
}

func (m *tuiModel) setResultFilter(filter string) {
	m.resultFilter = filter
	m.resultOffset = 0
	m.resultSourceCursor = 0
	m.refreshResultSources()
	m.resultCursor = firstResultFindingLine(m.filteredResult())
}

func resultFindingLines(result string) []int {
	var findings []int
	for index, line := range strings.Split(result, "\n") {
		if resultSeverity(line) != "" {
			findings = append(findings, index)
		}
	}
	return findings
}

func firstResultFindingLine(result string) int {
	findings := resultFindingLines(result)
	if len(findings) == 0 {
		return 0
	}
	return findings[0]
}

func (m *tuiModel) moveResultFinding(step int) {
	findings := resultFindingLines(m.filteredResult())
	if len(findings) == 0 {
		return
	}
	current := 0
	for index, line := range findings {
		if line == m.resultCursor {
			current = index
			break
		}
	}
	m.resultCursor = findings[min(len(findings)-1, max(0, current+step))]
	m.ensureResultCursorVisible()
}

func (m *tuiModel) ensureResultCursorVisible() {
	cursor := m.resultCursorDisplayLine()
	if cursor < m.resultOffset {
		m.resultOffset = cursor
	}
	if cursor >= m.resultOffset+m.resultHeight() {
		m.resultOffset = cursor - m.resultHeight() + 1
	}
}

func (m tuiModel) resultCursorDisplayLine() int {
	for index, line := range m.resultDisplayLines() {
		if line.sourceLine == m.resultCursor && line.firstOfLine {
			return index
		}
	}
	return 0
}

func (m tuiModel) resultDisplayLines() []tuiResultLine {
	width := max(20, m.width-2)
	var display []tuiResultLine
	for sourceLine, line := range strings.Split(m.filteredResult(), "\n") {
		wrapped := wrapTUIResultLine(line, width)
		for index, segment := range wrapped {
			display = append(display, tuiResultLine{text: segment, sourceLine: sourceLine, firstOfLine: index == 0})
		}
	}
	return display
}

func wrapTUIResultLine(line string, width int) []string {
	if line == "" || len([]rune(line)) <= width {
		return []string{line}
	}
	runes := []rune(line)
	var wrapped []string
	for len(runes) > width {
		breakAt := width
		for index := width; index > 0; index-- {
			if runes[index] == ' ' {
				breakAt = index
				break
			}
		}
		if breakAt == 0 {
			breakAt = width
		}
		wrapped = append(wrapped, string(runes[:breakAt]))
		runes = runes[breakAt:]
		for len(runes) > 0 && runes[0] == ' ' {
			runes = runes[1:]
		}
	}
	wrapped = append(wrapped, string(runes))
	return wrapped
}

func (m *tuiModel) refreshResultSources() {
	m.resultSources = resultSources(m.filteredResult())
	if m.resultSourceCursor >= len(m.resultSources) {
		m.resultSourceCursor = 0
	}
}

func (m tuiModel) filteredResult() string {
	if m.resultFilter == "" || m.resultFilter == "all" {
		return m.result
	}
	var blocks []string
	var block []string
	severity := ""
	flush := func() {
		if severity == m.resultFilter && len(block) > 0 {
			blocks = append(blocks, strings.Join(block, "\n"))
		}
		block, severity = nil, ""
	}
	for _, line := range strings.Split(m.result, "\n") {
		if next := resultSeverity(line); next != "" {
			flush()
			severity = next
			block = append(block, line)
			continue
		}
		if severity != "" {
			block = append(block, line)
		}
	}
	flush()
	if len(blocks) == 0 {
		return "No " + m.resultFilter + " findings."
	}
	return strings.Join(blocks, "\n")
}

func resultSeverity(line string) string {
	for _, severity := range []string{"warning", "error", "info"} {
		if strings.HasPrefix(line, strings.ToUpper(severity)+" HS-") {
			return severity
		}
	}
	return ""
}

func (m *tuiModel) loadContent() {
	m.items, m.err = collectContent(m.project)
	m.contentCursor = 0
}

func (m tuiModel) loadRemoteContentCmd() tea.Cmd {
	return func() tea.Msg {
		items, err := fetchIndex(m.remoteURL)
		if err != nil {
			urls, sitemapErr := publishedSitemapURLs(m.remoteURL, 100, 20*time.Second)
			if sitemapErr != nil {
				return tuiRemoteContentResult{err: fmt.Errorf("index.json: %v; sitemap.xml: %w", err, sitemapErr)}
			}
			content := make([]contentItem, 0, len(urls))
			for _, target := range urls {
				parsed, _ := url.Parse(target)
				title := parsed.Path
				if title == "" {
					title = "/"
				}
				content = append(content, contentItem{Title: title, URL: target, GeneratedURL: target, Source: target})
			}
			return tuiRemoteContentResult{items: content}
		}
		content := make([]contentItem, 0, len(items))
		for _, item := range items {
			content = append(content, contentItem{Title: item.Title, Date: parseDate(firstNonEmpty(item.Date, item.PublishDate)), Tags: item.Tags, Words: wordCount(firstNonEmpty(item.Content, item.Summary, item.Description)), URL: item.URL, GeneratedURL: item.URL, Source: item.URL, Body: firstNonEmpty(item.Content, item.Summary, item.Description)})
		}
		sort.Slice(content, func(i, j int) bool { return content[i].Date.After(content[j].Date) })
		return tuiRemoteContentResult{items: content}
	}
}

func (m *tuiModel) openContent(item contentItem) {
	m.previewSource = item.Source
	m.previewOffset = 0
	m.previewReturn = tuiContent
	if m.remoteURL != "" {
		m.previewText = fmt.Sprintf("%s\n\n%s", item.Title, item.Body)
		m.screen = tuiPreview
		return
	}
	data, err := os.ReadFile(filepath.Join(m.project, filepath.FromSlash(item.Source)))
	if err != nil {
		m.previewText = fmt.Sprintf("Could not read %s: %v", item.Source, err)
	} else {
		m.previewText = string(data)
	}
	m.screen = tuiPreview
}

func resultSources(result string) []tuiResultSource {
	seen := map[string]bool{}
	var sources []tuiResultSource
	for _, match := range tuiFindingSource.FindAllStringSubmatch(result, -1) {
		line := 0
		fmt.Sscanf(match[2], "%d", &line)
		key := match[1] + ":" + match[2]
		if !seen[key] {
			seen[key] = true
			sources = append(sources, tuiResultSource{path: filepath.ToSlash(match[1]), line: line})
		}
	}
	return sources
}

func (m *tuiModel) openResultSource() {
	lines := strings.Split(m.filteredResult(), "\n")
	if m.resultCursor < 0 || m.resultCursor >= len(lines) {
		return
	}
	match := tuiFindingSource.FindStringSubmatch(lines[m.resultCursor])
	if len(match) != 3 {
		return
	}
	line := 0
	fmt.Sscanf(match[2], "%d", &line)
	source := tuiResultSource{path: filepath.ToSlash(match[1]), line: line}
	data, err := os.ReadFile(filepath.Join(m.project, filepath.FromSlash(source.path)))
	if err != nil {
		m.previewSource = source.path
		m.previewText = fmt.Sprintf("Could not read %s: %v", source.path, err)
	} else {
		m.previewSource = fmt.Sprintf("%s:%d", source.path, source.line)
		m.previewText = numberedSourceExcerpt(string(data), source.line)
	}
	m.previewOffset = 0
	m.previewReturn = tuiResult
	m.screen = tuiPreview
}

func numberedSourceExcerpt(source string, focusLine int) string {
	lines := strings.Split(source, "\n")
	start := max(0, focusLine-4)
	end := min(len(lines), focusLine+3)
	var excerpt strings.Builder
	for i := start; i < end; i++ {
		marker := " "
		if i+1 == focusLine {
			marker = ">"
		}
		fmt.Fprintf(&excerpt, "%s %4d | %s\n", marker, i+1, lines[i])
	}
	return strings.TrimSuffix(excerpt.String(), "\n")
}

func (m *tuiModel) showContentURL(item contentItem) {
	if m.remoteURL != "" {
		m.selectedURL = item.URL
		m.urlNote = "URL from the published site's index.json."
		return
	}
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
	case tuiCampaign:
		return m.campaignView()
	case tuiCampaigns:
		return m.campaignsView()
	case tuiCampaignAdd:
		return m.campaignAddView()
	case tuiCampaignRetire:
		return m.campaignRetireView()
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
	title := "Content browser"
	if m.remoteURL != "" {
		title = "Published-site browser | " + m.remoteURL
	}
	fmt.Fprintf(&view, "%s | %s\n", title, m.filterDescription())
	if m.input == tuiSearchInput {
		fmt.Fprintf(&view, "Search: %s█\n", m.query)
	} else if m.query != "" {
		fmt.Fprintf(&view, "Search: %s\n", m.query)
	}
	if m.remoteURL != "" {
		view.WriteString("/ search • f filters • c clear • ↑/↓ select • PgUp/PgDn scroll • Enter details • d remote doctor • u URL • r refresh • Esc back\n\n")
	} else {
		view.WriteString("/ search • f filters • c clear • ↑/↓ select • PgUp/PgDn scroll • Enter view • d doctor • l campaign link • u URL • r refresh • Esc back\n\n")
	}
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

func (m tuiModel) campaignView() string {
	values := []string{m.campaignKey, m.campaignSource, m.campaignMedium, m.campaignContent}
	labels := []string{"Campaign", "Source", "Medium", "Creative / placement"}
	var view strings.Builder
	fmt.Fprintf(&view, "Create campaign link\n\n%s\n\n", m.campaignItem.Title)
	for i, label := range labels {
		marker := "  "
		if i == m.campaignField {
			marker = "> "
		}
		value := values[i]
		if i == 3 && value == "" {
			value = "(optional)"
		}
		if i == 3 && m.input == tuiCampaignContentInput {
			value += "█"
		}
		fmt.Fprintf(&view, "%s%s: %s\n", marker, label, value)
	}
	view.WriteString("\n↑/↓ choose field • ←/→ change lists • Enter edit or create link • Esc cancel\n")
	return view.String()
}

func (m tuiModel) campaignsView() string {
	var view strings.Builder
	view.WriteString("Campaign registry\n\n")
	if m.campaignMessage != "" {
		fmt.Fprintf(&view, "%s\n\n", m.campaignMessage)
	}
	if len(m.campaignPolicy.Campaigns) == 0 {
		view.WriteString("No campaigns. Press a to add one.\n")
	} else {
		for i, campaign := range m.campaignPolicy.Campaigns {
			marker := "  "
			if i == m.campaignCursor {
				marker = "> "
			}
			fmt.Fprintf(&view, "%s%-9s %-28s %s\n", marker, campaign.Status, campaign.Key, campaign.Label)
		}
	}
	view.WriteString("\n↑/↓ select • a add campaign • r retire selected active campaign • Enter or Esc back\n")
	return view.String()
}

func (m tuiModel) campaignAddView() string {
	values := []string{m.campaignDraftKey, m.campaignDraftLabel, m.campaignDraftDesc}
	labels := []string{"Campaign key", "Label", "Description"}
	inputs := []tuiInput{tuiCampaignKeyInput, tuiCampaignLabelInput, tuiCampaignDescriptionInput}
	var view strings.Builder
	view.WriteString("Add campaign\n\n")
	if m.campaignMessage != "" {
		fmt.Fprintf(&view, "%s\n\n", m.campaignMessage)
	}
	for i, label := range labels {
		marker := "  "
		if i == m.campaignAddField {
			marker = "> "
		}
		value := values[i]
		if m.input == inputs[i] {
			value += "█"
		}
		fmt.Fprintf(&view, "%s%s: %s\n", marker, label, value)
	}
	view.WriteString("\n↑/↓ select • Enter edit • s save • Esc cancel\n")
	return view.String()
}

func (m tuiModel) campaignRetireView() string {
	if len(m.campaignPolicy.Campaigns) == 0 {
		return "Campaign registry\n\nNo campaign selected.\n"
	}
	campaign := m.campaignPolicy.Campaigns[m.campaignCursor]
	return fmt.Sprintf("Retire campaign?\n\n%s\n%s\n\nThis keeps historic links valid but prevents new links.\n\nEnter confirm • Esc cancel\n", campaign.Key, campaign.Label)
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
	lines := strings.Split(m.filteredResult(), "\n")
	display := m.resultDisplayLines()
	end := min(len(display), m.resultOffset+m.resultHeight())
	for index := m.resultOffset; index < end; index++ {
		marker := "  "
		if display[index].sourceLine == m.resultCursor && display[index].firstOfLine && resultSeverity(lines[m.resultCursor]) != "" {
			marker = "> "
		}
		view.WriteString(marker + display[index].text + "\n")
	}
	if len(lines) > m.resultCursor && m.resultCursor >= 0 {
		if match := tuiFindingSource.FindStringSubmatch(lines[m.resultCursor]); len(match) == 3 {
			fmt.Fprintf(&view, "\nSelected source: %s:%s • v inspect\n", match[1], match[2])
		}
	}
	fmt.Fprintf(&view, "a all • w warnings • e errors • i info | Showing: %s\n", m.resultFilter)
	view.WriteString("↑/↓ select • PgUp/PgDn or Space page • Home/End jump • Enter or Esc back • q quit\n")
	return view.String()
}

func (m tuiModel) contentHeight() int { return max(4, m.height-11) }
func (m tuiModel) resultHeight() int  { return max(4, m.height-5) }
func (m tuiModel) previewHeight() int { return max(4, m.height-5) }
