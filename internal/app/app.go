// Package app implements the hs command without owning process lifecycle.
package app

import (
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const appName = "hs"

type config struct {
	BaseURL string `json:"base_url"`
}

// searchItem accepts the common fields emitted by Hugo's JSON search output.
// Extra fields are deliberately ignored so themes can extend their index safely.
type searchItem struct {
	Title       string   `json:"title"`
	URL         string   `json:"url"`
	Permalink   string   `json:"permalink"`
	Date        string   `json:"date"`
	PublishDate string   `json:"publishdate"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags"`
}

type result struct {
	searchItem
	Score int
}

type post struct {
	Type     string
	Title    string
	Date     time.Time
	Category string
	Tags     []string
	Words    int
}

type contentSource struct {
	path   string
	prefix string // Path below Hugo's virtual content directory.
}

type moduleMount struct {
	source string
	target string
}

type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message }

type DoctorOptions struct {
	ProjectDir     string
	RemoteURL      string
	Only           []string
	OnlySet        bool
	Source         string
	Strict         bool
	Format         string
	BuildDrafts    bool
	BuildFuture    bool
	Remote         bool
	RemoteMaxPages int
	RemoteTimeout  time.Duration
}

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

type Finding struct {
	Code          string   `json:"code"`
	Severity      Severity `json:"severity"`
	Check         string   `json:"check"`
	Message       string   `json:"message"`
	Source        string   `json:"source,omitempty"`
	Line          int      `json:"line,omitempty"`
	GeneratedPath string   `json:"generated_path,omitempty"`
	Target        string   `json:"target,omitempty"`
	Help          string   `json:"help,omitempty"`
}

type generatedPage struct {
	file    string
	urlPath string
	ids     map[string]bool
}

type urlReport struct {
	URLs    []string `json:"urls"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

type buildOptions struct {
	ProjectDir  string
	Format      string
	BuildDrafts bool
	BuildFuture bool
}

type buildReport struct {
	Status      string   `json:"status"`
	Project     string   `json:"project"`
	HugoVersion string   `json:"hugo_version"`
	Duration    string   `json:"duration"`
	Pages       int      `json:"pages"`
	Files       int      `json:"files"`
	OutputBytes int64    `json:"output_bytes"`
	Warnings    []string `json:"warnings,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type hugoBuild struct {
	Project        string
	OutputDir      string
	HugoVersion    string
	Duration       time.Duration
	Pages          map[string]generatedPage
	Files          map[string]bool
	OutputBytes    int64
	Warnings       []string
	WarningDetails []buildWarning
	BuildErr       error
	Output         string
}

// buildWarning keeps the full diagnostic Hugo emitted. Source and Line are
// populated when the diagnostic names a file position in the project.
type buildWarning struct {
	Message string
	Source  string
	Line    int
}

var (
	hugoWarningPrefix = regexp.MustCompile(`(?i)^\s*(?:\d{4}-\d{2}-\d{2}[^\s]*\s+)?WARN(?:ING)?\s*[: ]\s*`)
	hugoLocation      = regexp.MustCompile(`(?m)(?:^|\s)([^\s:]+\.(?:md|markdown|html|gohtml)):(\d+)(?::\d+)?`)
	shortcodeWarning  = regexp.MustCompile(`(?i)["']([^"']+)["']\s+shortcode`)
)

// Run executes hs and returns the process exit code. It keeps command behavior
// testable while allowing cmd/hs to remain a minimal process entrypoint.
func Run(args []string, out, errOut io.Writer) int {
	if err := run(args, out, errOut); err != nil {
		var status *exitError
		if errors.As(err, &status) {
			if status.message != "" {
				fmt.Fprintln(errOut, "error:", status.message)
			}
			return status.code
		}
		fmt.Fprintln(errOut, "error:", err)
		return 1
	}
	return 0
}

func run(args []string, out, errOut io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		printUsage(out)
		return nil
	}

	switch args[0] {
	case "site":
		return runSite(args[1:], out)
	case "search":
		return runSearch(args[1:], out)
	case "posts":
		return runPosts(args[1:], out)
	case "content":
		return runContent(args[1:], out)
	case "tui":
		return runTUI(args[1:], out)
	case "audit":
		return runAudit(args[1:], out)
	case "doctor":
		return runDoctor(args[1:], out)
	case "build":
		return runBuild(args[1:], out)
	case "urls":
		return runURLs(args[1:], out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// runAudit exposes focused reports while reusing doctor as the single audit
// engine. Local audits build into a temporary production destination.
func runAudit(args []string, out io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs audit <seo|links> [project-directory] [--remote URL] [--strict] [--format text|json|sarif] [--build-drafts] [--build-future]")
		return nil
	}
	check := args[0]
	if check != "seo" && check != "links" {
		return &exitError{code: 2, message: fmt.Sprintf("unknown audit %q; expected seo or links", check)}
	}
	doctorArgs := append([]string{}, args[1:]...)
	doctorArgs = append(doctorArgs, "--only", check)
	return runDoctor(doctorArgs, out)
}

// runBuild runs Hugo with production settings in a temporary destination and
// reports the generated output without changing the project's public directory.
func runBuild(args []string, out io.Writer) error {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs build [project-directory] [--build-drafts] [--build-future] [--format text|json]")
		return nil
	}
	opts, err := parseBuildOptions(args)
	if err != nil {
		return &exitError{code: 2, message: err.Error()}
	}
	project, _, err := validateHugoProject(opts.ProjectDir)
	if err != nil {
		return err
	}
	built, err := runHugoBuild(project, opts.BuildDrafts, opts.BuildFuture)
	if err != nil {
		return err
	}
	defer os.RemoveAll(built.OutputDir)
	if err := writeBuildReport(out, opts.Format, built); err != nil {
		return err
	}
	if built.BuildErr != nil {
		return &exitError{code: 1, message: "Hugo build failed: " + oneLine(built.Output, 500)}
	}
	return nil
}

func parseBuildOptions(args []string) (buildOptions, error) {
	opts := buildOptions{Format: "text"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--build-drafts":
			opts.BuildDrafts = true
		case "--build-future":
			opts.BuildFuture = true
		case "--format":
			if i+1 == len(args) {
				return opts, errors.New("--format requires a value")
			}
			i++
			opts.Format = args[i]
		default:
			if strings.HasPrefix(args[i], "-") || opts.ProjectDir != "" {
				return opts, errors.New("usage: hs build [project-directory] [--build-drafts] [--build-future] [--format text|json]")
			}
			opts.ProjectDir = args[i]
		}
	}
	if opts.ProjectDir == "" {
		var err error
		opts.ProjectDir, err = os.Getwd()
		if err != nil {
			return opts, err
		}
	}
	if opts.Format != "text" && opts.Format != "json" {
		return opts, fmt.Errorf("unknown build format %q", opts.Format)
	}
	return opts, nil
}

func validateHugoProject(project string) (string, []string, error) {
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", nil, &exitError{code: 2, message: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", nil, &exitError{code: 2, message: "project directory does not exist: " + abs}
	}
	configFiles, err := hugoConfigFiles(abs)
	if err != nil {
		return "", nil, &exitError{code: 3, message: err.Error()}
	}
	if len(configFiles) == 0 {
		return "", nil, &exitError{code: 3, message: "no Hugo configuration found in " + abs}
	}
	return abs, configFiles, nil
}

func runHugoBuild(project string, buildDrafts, buildFuture bool) (hugoBuild, error) {
	hugo, err := exec.LookPath("hugo")
	if err != nil {
		return hugoBuild{}, &exitError{code: 3, message: "Hugo executable not found on PATH"}
	}
	versionOut, _ := exec.Command(hugo, "version").CombinedOutput()
	temporary, err := os.MkdirTemp("", "hs-build-")
	if err != nil {
		return hugoBuild{}, &exitError{code: 3, message: err.Error()}
	}
	built := hugoBuild{Project: project, OutputDir: temporary, HugoVersion: strings.TrimSpace(string(versionOut))}
	arguments := []string{"--destination", temporary, "--environment", "production"}
	if buildDrafts {
		arguments = append(arguments, "--buildDrafts")
	}
	if buildFuture {
		arguments = append(arguments, "--buildFuture")
	}
	command := exec.Command(hugo, arguments...)
	command.Dir = project
	started := time.Now()
	output, buildErr := command.CombinedOutput()
	built.Duration = time.Since(started)
	built.Output = string(output)
	built.BuildErr = buildErr
	built.WarningDetails = parseHugoWarnings(built.Output)
	for _, warning := range built.WarningDetails {
		built.Warnings = append(built.Warnings, warning.Message)
	}
	built.OutputBytes, _ = directorySize(temporary)
	if buildErr != nil {
		return built, nil
	}
	built.Pages, built.Files, err = discoverGenerated(temporary)
	if err != nil {
		_ = os.RemoveAll(temporary)
		return hugoBuild{}, &exitError{code: 3, message: err.Error()}
	}
	return built, nil
}

// parseHugoWarnings retains warning continuations. Hugo emits its suggested
// suppression setting as a separate WARN line, which used to become a second,
// contextless finding in hs.
func parseHugoWarnings(output string) []buildWarning {
	var warnings []buildWarning
	var current *buildWarning
	finish := func() {
		if current == nil {
			return
		}
		current.Message = strings.TrimSpace(current.Message)
		if current.Message != "" {
			if match := hugoLocation.FindStringSubmatch(current.Message); len(match) == 3 {
				current.Source = filepath.ToSlash(match[1])
				current.Line, _ = strconv.Atoi(match[2])
			}
			warnings = append(warnings, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(output, "\n") {
		message := hugoWarningPrefix.ReplaceAllString(line, "")
		isWarning := message != line
		isSuppression := strings.Contains(strings.ToLower(message), "you can suppress this warning")
		if isWarning {
			if current != nil && isSuppression {
				current.Message += "\n" + strings.TrimSpace(message)
				continue
			}
			finish()
			current = &buildWarning{Message: strings.TrimSpace(line)}
			continue
		}
		if current != nil && isSuppression {
			current.Message += "\n" + strings.TrimSpace(message)
			continue
		}
		if current != nil && strings.Contains(strings.ToLower(current.Message), "you can suppress this warning") {
			if strings.TrimSpace(line) != "" {
				current.Message += "\n" + strings.TrimSpace(line)
				continue
			}
		}
		finish()
	}
	finish()
	return warnings
}

func directorySize(root string) (int64, error) {
	var size int64
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
}

func writeBuildReport(out io.Writer, format string, built hugoBuild) error {
	report := buildReport{Project: built.Project, HugoVersion: built.HugoVersion, Duration: built.Duration.Round(time.Millisecond).String(), Pages: len(built.Pages), Files: len(built.Files), OutputBytes: built.OutputBytes, Warnings: built.Warnings, Status: "success"}
	if built.BuildErr != nil {
		report.Status = "failed"
		report.Error = strings.TrimSpace(built.Output)
	}
	if format == "json" {
		return json.NewEncoder(out).Encode(report)
	}
	if _, err := fmt.Fprintf(out, "Build: %s\nProject: %s\nHugo: %s\nDuration: %s\nPages: %d\nFiles: %d\nOutput size: %s\n", report.Status, report.Project, report.HugoVersion, report.Duration, report.Pages, report.Files, humanBytes(report.OutputBytes)); err != nil {
		return err
	}
	for _, warning := range report.Warnings {
		if _, err := fmt.Fprintf(out, "Warning: %s\n", warning); err != nil {
			return err
		}
	}
	if report.Error != "" {
		_, err := fmt.Fprintf(out, "Error: %s\n", report.Error)
		return err
	}
	return nil
}

func humanBytes(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	return fmt.Sprintf("%.1f KB", float64(size)/1024)
}

// runURLs builds a Hugo project into a temporary directory and prints the
// canonical paths represented by its generated HTML files. It never changes
// the project's configured public directory.
func runURLs(args []string, out io.Writer) error {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs urls [project-directory] [--format text|json] [--compare snapshot.json]")
		return nil
	}
	project, format, compare, err := parseURLOptions(args)
	if err != nil {
		return &exitError{code: 2, message: err.Error()}
	}
	project, _, err = validateHugoProject(project)
	if err != nil {
		return err
	}
	built, err := runHugoBuild(project, false, false)
	if err != nil {
		return err
	}
	defer os.RemoveAll(built.OutputDir)
	if built.BuildErr != nil {
		return &exitError{code: 1, message: "Hugo build failed: " + oneLine(built.Output, 500)}
	}
	report := urlReport{URLs: generatedURLs(built.Pages)}
	if compare != "" {
		previous, err := readURLSnapshot(compare)
		if err != nil {
			return &exitError{code: 2, message: err.Error()}
		}
		report.Added, report.Removed = compareURLs(previous, report.URLs)
	}
	if err := writeURLReport(out, format, report, compare != ""); err != nil {
		return err
	}
	if len(report.Added) > 0 || len(report.Removed) > 0 {
		return &exitError{code: 1}
	}
	return nil
}

func parseURLOptions(args []string) (project, format, compare string, err error) {
	format = "text"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format", "--compare":
			if i+1 == len(args) {
				return "", "", "", fmt.Errorf("%s requires a value", args[i])
			}
			i++
			if args[i-1] == "--format" {
				format = args[i]
			} else {
				compare = args[i]
			}
		default:
			if strings.HasPrefix(args[i], "-") || project != "" {
				return "", "", "", errors.New("usage: hs urls [project-directory] [--format text|json] [--compare snapshot.json]")
			}
			project = args[i]
		}
	}
	if project == "" {
		project, err = os.Getwd()
		if err != nil {
			return "", "", "", err
		}
	}
	if format != "text" && format != "json" {
		return "", "", "", fmt.Errorf("unknown urls format %q", format)
	}
	return project, format, compare, nil
}

func generatedURLs(pages map[string]generatedPage) []string {
	urls := make([]string, 0, len(pages))
	for urlPath := range pages {
		urls = append(urls, urlPath)
	}
	sort.Strings(urls)
	return urls
}

func readURLSnapshot(filename string) ([]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read URL snapshot: %w", err)
	}
	var report urlReport
	if err := json.Unmarshal(data, &report); err == nil && report.URLs != nil {
		sort.Strings(report.URLs)
		return report.URLs, nil
	}
	var urls []string
	if err := json.Unmarshal(data, &urls); err != nil {
		return nil, fmt.Errorf("read URL snapshot: expected a JSON URL report or array: %w", err)
	}
	sort.Strings(urls)
	return urls, nil
}

func compareURLs(previous, current []string) (added, removed []string) {
	oldSet, currentSet := make(map[string]bool, len(previous)), make(map[string]bool, len(current))
	for _, value := range previous {
		oldSet[value] = true
	}
	for _, value := range current {
		currentSet[value] = true
	}
	for _, value := range current {
		if !oldSet[value] {
			added = append(added, value)
		}
	}
	for _, value := range previous {
		if !currentSet[value] {
			removed = append(removed, value)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func writeURLReport(out io.Writer, format string, report urlReport, compared bool) error {
	if format == "json" {
		return json.NewEncoder(out).Encode(report)
	}
	for _, urlPath := range report.URLs {
		if _, err := fmt.Fprintln(out, urlPath); err != nil {
			return err
		}
	}
	if compared && len(report.Added) == 0 && len(report.Removed) == 0 {
		_, err := fmt.Fprintln(out, "No URL changes.")
		return err
	}
	for _, urlPath := range report.Added {
		if _, err := fmt.Fprintf(out, "+ %s\n", urlPath); err != nil {
			return err
		}
	}
	for _, urlPath := range report.Removed {
		if _, err := fmt.Fprintf(out, "- %s\n", urlPath); err != nil {
			return err
		}
	}
	return nil
}

// runPosts reports every content page in a local Hugo site. The command reads
// source files, not the generated site, so drafts and unpublished posts are
// included deliberately.
func runPosts(args []string, out io.Writer) error {
	if len(args) > 0 && isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs posts [site-directory] [--verbose]")
		return nil
	}
	verbose := false
	siteDir := ""
	for _, arg := range args {
		switch arg {
		case "--verbose":
			verbose = true
		default:
			if strings.HasPrefix(arg, "-") || siteDir != "" {
				return errors.New("usage: hs posts [site-directory] [--verbose]")
			}
			siteDir = arg
		}
	}
	if siteDir == "" {
		var err error
		siteDir, err = os.Getwd()
		if err != nil {
			return err
		}
	}

	posts, err := collectPosts(siteDir)
	if err != nil {
		return err
	}
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].Date.Equal(posts[j].Date) {
			return posts[i].Title < posts[j].Title
		}
		return posts[i].Date.Before(posts[j].Date)
	})

	summary := summarizePosts(posts)
	if !verbose {
		return writePostSummary(out, summary)
	}
	return writePostsCSV(out, posts, summary)
}

type postSummary struct {
	Count, TotalWords, DatedPosts int
	AverageWords                  int
	AverageInterval               string
	Oldest, Newest                time.Time
}

type contentItem struct {
	Title        string    `json:"title"`
	Date         time.Time `json:"date,omitempty"`
	Section      string    `json:"section"`
	Categories   []string  `json:"categories,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	Draft        bool      `json:"draft"`
	Words        int       `json:"word_count"`
	URL          string    `json:"url"`
	GeneratedURL string    `json:"-"`
	Source       string    `json:"source"`
	Body         string    `json:"-"`
	StatsPage    bool      `json:"-"`
}

func runContent(args []string, out io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs content list [site-directory] [--draft true|false] [--format text|json]\n       hs content search <terms> [site-directory] [--section NAME] [--tag TAG] [--draft true|false] [--from DATE] [--to DATE]\n       hs content new <title> [site-directory] [--section NAME] [--tag TAG] [--draft] [--date DATE]\n       hs content stats [site-directory] [--write] [--draft] [--output PATH]")
		return nil
	}
	switch args[0] {
	case "list":
		return runContentList(args[1:], out)
	case "search":
		return runContentSearch(args[1:], out)
	case "new":
		return runContentNew(args[1:], out)
	case "stats":
		return runContentStats(args[1:], out)
	default:
		return fmt.Errorf("unknown content command %q", args[0])
	}
}

func resolveContentProject(args []string) (string, []string, error) {
	project := ""
	var remaining []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			remaining = append(remaining, arg)
			continue
		}
		if project != "" {
			remaining = append(remaining, arg)
			continue
		}
		project = arg
	}
	if project == "" {
		var err error
		project, err = os.Getwd()
		if err != nil {
			return "", nil, err
		}
	}
	return project, remaining, nil
}

func runContentList(args []string, out io.Writer) error {
	format := "text"
	draft := ""
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format":
			if i+1 == len(args) {
				return errors.New("--format requires text or json")
			}
			i++
			format = args[i]
		case "--draft":
			if i+1 == len(args) {
				return errors.New("--draft requires true or false")
			}
			i++
			draft = args[i]
			if draft != "true" && draft != "false" {
				return errors.New("--draft requires true or false")
			}
		default:
			positional = append(positional, args[i])
		}
	}
	if format != "text" && format != "json" {
		return errors.New("--format requires text or json")
	}
	project, extra, err := resolveContentProject(positional)
	if err != nil {
		return err
	}
	if len(extra) != 0 {
		return errors.New("usage: hs content list [site-directory] [--draft true|false] [--format text|json]")
	}
	items, err := collectContent(project)
	if err != nil {
		return err
	}
	if draft != "" {
		filtered := items[:0]
		for _, item := range items {
			if strconv.FormatBool(item.Draft) == draft {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	if format == "json" {
		return json.NewEncoder(out).Encode(items)
	}
	for _, item := range items {
		date := ""
		if !item.Date.IsZero() {
			date = item.Date.Format("2006-01-02")
		}
		fmt.Fprintf(out, "%s\t%s\t%t\t%d\t%s\t%s\n", date, item.Section, item.Draft, item.Words, item.Title, item.URL)
	}
	return nil
}

func runContentSearch(args []string, out io.Writer) error {
	filters := map[string]string{}
	var positional []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "--") {
			key := strings.TrimPrefix(args[i], "--")
			if key != "section" && key != "tag" && key != "draft" && key != "from" && key != "to" {
				return fmt.Errorf("unknown content search option %q", args[i])
			}
			if i+1 == len(args) {
				return fmt.Errorf("%s requires a value", args[i])
			}
			i++
			filters[key] = args[i]
		} else {
			positional = append(positional, args[i])
		}
	}
	project := ""
	if len(positional) > 0 && directoryExists(positional[len(positional)-1]) {
		project = positional[len(positional)-1]
		positional = positional[:len(positional)-1]
	}
	if project == "" {
		var err error
		project, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	query := strings.ToLower(strings.TrimSpace(strings.Join(positional, " ")))
	if query == "" {
		return errors.New("usage: hs content search <terms> [site-directory] [--section NAME] [--tag TAG] [--draft true|false] [--from DATE] [--to DATE]")
	}
	items, err := collectContent(project)
	if err != nil {
		return err
	}
	from, to := parseDate(filters["from"]), parseDate(filters["to"])
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.Title, strings.Join(item.Tags, " "), item.Body}, " "))
		if !containsAll(haystack, query) || (filters["section"] != "" && !strings.EqualFold(filters["section"], item.Section)) || (filters["tag"] != "" && !hasString(item.Tags, filters["tag"])) || (filters["draft"] != "" && strconv.FormatBool(item.Draft) != filters["draft"]) || (!from.IsZero() && (item.Date.IsZero() || item.Date.Before(from))) || (!to.IsZero() && (item.Date.IsZero() || item.Date.After(to))) {
			continue
		}
		date := ""
		if !item.Date.IsZero() {
			date = item.Date.Format("2006-01-02")
		}
		fmt.Fprintf(out, "%s\t%s\t%t\t%d\t%s\t%s\n", date, item.Section, item.Draft, item.Words, item.Title, item.URL)
	}
	return nil
}

func runContentStats(args []string, out io.Writer) error {
	write, draft := false, false
	output := "site-stats"
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--write":
			write = true
		case "--draft":
			draft = true
		case "--output":
			if i+1 == len(args) {
				return errors.New("--output requires a path")
			}
			i++
			output = args[i]
		default:
			positional = append(positional, args[i])
		}
	}
	project, extra, err := resolveContentProject(positional)
	if err != nil {
		return err
	}
	if len(extra) != 0 || strings.HasPrefix(output, "/") || strings.Contains(output, "..") {
		return errors.New("usage: hs content stats [site-directory] [--write] [--draft] [--output PATH]")
	}
	items, err := collectContent(project)
	if err != nil {
		return err
	}
	stats := makeContentStats(items)
	if !write {
		return writeContentStatsSummary(out, stats)
	}
	if err := writeContentStats(project, output, draft, stats); err != nil {
		return err
	}
	return writeContentStatsSummary(out, stats)
}

func runContentNew(args []string, out io.Writer) error {
	section, date := "", time.Now().Format("2006-01-02")
	tags := []string{}
	draft := false
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--section", "--tag", "--date":
			if i+1 == len(args) {
				return fmt.Errorf("%s requires a value", args[i])
			}
			i++
			if args[i-1] == "--section" {
				section = args[i]
			} else if args[i-1] == "--tag" {
				tags = append(tags, args[i])
			} else {
				date = args[i]
			}
		case "--draft":
			draft = true
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) == 0 {
		return errors.New("usage: hs content new <title> [site-directory] [--section NAME] [--tag TAG] [--draft] [--date DATE]")
	}
	project := ""
	title := positional[0]
	if len(positional) > 1 {
		project = positional[1]
	}
	if len(positional) > 2 {
		return errors.New("usage: hs content new <title> [site-directory] [--section NAME] [--tag TAG] [--draft] [--date DATE]")
	}
	if project == "" {
		var err error
		project, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if parseDate(date).IsZero() {
		return errors.New("--date must be a valid date")
	}
	sources, err := contentSources(project)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return errors.New("no writable Hugo content source found")
	}
	filename := slugify(title) + ".md"
	relative := filepath.Join(section, filename)
	target := filepath.Join(sources[0].path, relative)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("content file already exists: %s", target)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	var body strings.Builder
	body.WriteString("---\n")
	fmt.Fprintf(&body, "title: %q\ndate: %s\ndraft: %t\n", title, date, draft)
	if len(tags) > 0 {
		body.WriteString("tags:\n")
		for _, tag := range tags {
			fmt.Fprintf(&body, "  - %q\n", tag)
		}
	}
	body.WriteString("---\n\n")
	if err := os.WriteFile(target, []byte(body.String()), 0644); err != nil {
		return err
	}
	fmt.Fprintln(out, target)
	return nil
}

func collectContent(siteDir string) ([]contentItem, error) {
	sources, err := contentSources(siteDir)
	if err != nil {
		return nil, err
	}
	var items []contentItem
	seen := map[string]bool{}
	for _, source := range sources {
		err := filepath.WalkDir(source.path, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(file))
			if ext != ".md" && ext != ".markdown" && ext != ".html" {
				return nil
			}
			rel, err := filepath.Rel(source.path, file)
			if err != nil {
				return err
			}
			virtual := filepath.ToSlash(filepath.Join(source.prefix, rel))
			if seen[virtual] {
				return nil
			}
			seen[virtual] = true
			data, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			meta, body := parseFrontMatter(string(data))
			relative, _ := filepath.Rel(siteDir, file)
			items = append(items, contentItem{Title: meta.string("title"), Date: parseDate(meta.string("date", "publishdate", "publishDate")), Section: contentSection(virtual), Categories: meta.strings("categories", "category"), Tags: meta.strings("tags", "tag"), Draft: strings.EqualFold(meta.string("draft"), "true"), Words: wordCount(body), URL: contentURL(virtual, meta), GeneratedURL: generatedContentURL(virtual), Source: filepath.ToSlash(relative), Body: body, StatsPage: strings.EqualFold(meta.string("hs_stats"), "true")})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Date.Equal(items[j].Date) {
			return items[i].Title < items[j].Title
		}
		return items[i].Date.After(items[j].Date)
	})
	return items, nil
}

func contentSection(virtual string) string {
	parts := strings.Split(virtual, "/")
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}
func contentURL(virtual string, meta frontMatter) string {
	if configured := meta.string("url"); configured != "" {
		return configured
	}
	return generatedContentURL(virtual)
}

func generatedContentURL(virtual string) string {
	virtual = strings.TrimSuffix(virtual, filepath.Ext(virtual))
	parts := strings.Split(virtual, "/")
	if len(parts) > 0 && parts[len(parts)-1] == "_index" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 0 && parts[len(parts)-1] == "index" {
		parts = parts[:len(parts)-1]
	}
	result := "/" + strings.Join(parts, "/")
	return strings.TrimRight(result, "/") + "/"
}
func containsAll(haystack, query string) bool {
	for _, word := range strings.Fields(query) {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}
func hasString(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}
func directoryExists(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }
func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			b.WriteRune(char)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

type contentStats struct {
	GeneratedAt     time.Time                    `json:"generated_at"`
	Posts           int                          `json:"posts"`
	TotalWords      int                          `json:"total_words"`
	AverageWords    int                          `json:"average_words_per_post"`
	AverageInterval string                       `json:"average_time_between_posts,omitempty"`
	FirstPublished  string                       `json:"first_published,omitempty"`
	LatestPublished string                       `json:"latest_published,omitempty"`
	Sections        map[string]contentStatBucket `json:"sections"`
	Tags            map[string]int               `json:"tags"`
	Years           map[string]contentStatBucket `json:"years"`
	Recent          []contentItem                `json:"recent"`
}
type contentStatBucket struct {
	Posts int `json:"posts"`
	Words int `json:"words"`
}

func makeContentStats(items []contentItem) contentStats {
	stats := contentStats{GeneratedAt: time.Now().UTC(), Sections: map[string]contentStatBucket{}, Tags: map[string]int{}, Years: map[string]contentStatBucket{}}
	posts := make([]post, 0, len(items))
	included := make([]contentItem, 0, len(items))
	for _, item := range items {
		if item.StatsPage {
			continue
		}
		included = append(included, item)
		stats.Posts++
		stats.TotalWords += item.Words
		posts = append(posts, post{Date: item.Date, Words: item.Words})
		section := stats.Sections[item.Section]
		section.Posts++
		section.Words += item.Words
		stats.Sections[item.Section] = section
		for _, tag := range item.Tags {
			stats.Tags[tag]++
		}
		if !item.Date.IsZero() {
			year := item.Date.Format("2006")
			bucket := stats.Years[year]
			bucket.Posts++
			bucket.Words += item.Words
			stats.Years[year] = bucket
		}
	}
	if stats.Posts > 0 {
		stats.AverageWords = stats.TotalWords / stats.Posts
	}
	summary := summarizePosts(posts)
	stats.AverageInterval = summary.AverageInterval
	if !summary.Oldest.IsZero() {
		stats.FirstPublished = summary.Oldest.Format("2006-01-02")
		stats.LatestPublished = summary.Newest.Format("2006-01-02")
	}
	if len(included) > 10 {
		stats.Recent = append([]contentItem(nil), included[:10]...)
	} else {
		stats.Recent = append([]contentItem(nil), included...)
	}
	for i := range stats.Recent {
		stats.Recent[i].Body = ""
	}
	return stats
}

func writeContentStatsSummary(out io.Writer, stats contentStats) error {
	if _, err := fmt.Fprintf(out, "Posts: %d\nTotal words: %d\nAverage words per post: %d\n", stats.Posts, stats.TotalWords, stats.AverageWords); err != nil {
		return err
	}
	if stats.AverageInterval != "" {
		if _, err := fmt.Fprintf(out, "Average time between posts: %s\n", stats.AverageInterval); err != nil {
			return err
		}
	}
	if stats.FirstPublished != "" {
		_, err := fmt.Fprintf(out, "Publishing span: %s to %s\n", stats.FirstPublished, stats.LatestPublished)
		return err
	}
	return nil
}

func writeContentStats(project, output string, draft bool, stats contentStats) error {
	dataPath := filepath.Join(project, "data", "site-stats.json")
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(dataPath, append(data, '\n'), 0644); err != nil {
		return err
	}
	sources, err := contentSources(project)
	if err != nil {
		return err
	}
	pagePath := filepath.Join(sources[0].path, filepath.FromSlash(output), "_index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0755); err != nil {
		return err
	}
	page := fmt.Sprintf("---\ntitle: \"Site statistics\"\ndraft: %t\nhs_stats: true\nlayout: \"site-stats\"\n---\n\nThis page is powered by `data/site-stats.json`. Add a `layouts/_default/site-stats.html` layout to render the dashboard.\n", draft)
	return os.WriteFile(pagePath, []byte(page), 0644)
}

func summarizePosts(posts []post) postSummary {
	summary := postSummary{Count: len(posts)}
	var total time.Duration
	for _, post := range posts {
		summary.TotalWords += post.Words
		if post.Date.IsZero() {
			continue
		}
		summary.DatedPosts++
		if summary.Oldest.IsZero() || post.Date.Before(summary.Oldest) {
			summary.Oldest = post.Date
		}
		if summary.Newest.IsZero() || post.Date.After(summary.Newest) {
			summary.Newest = post.Date
		}
	}
	if summary.Count > 0 {
		summary.AverageWords = summary.TotalWords / summary.Count
	}
	last := time.Time{}
	for _, post := range posts {
		if !post.Date.IsZero() {
			if !last.IsZero() {
				total += post.Date.Sub(last)
			}
			last = post.Date
		}
	}
	if summary.DatedPosts > 1 {
		summary.AverageInterval = formatDuration(total / time.Duration(summary.DatedPosts-1))
	}
	return summary
}

func writePostSummary(out io.Writer, summary postSummary) error {
	if _, err := fmt.Fprintf(out, "Posts: %d\nTotal words: %d\nAverage words per post: %d\n", summary.Count, summary.TotalWords, summary.AverageWords); err != nil {
		return err
	}
	if summary.AverageInterval != "" {
		if _, err := fmt.Fprintf(out, "Average time between posts: %s\n", summary.AverageInterval); err != nil {
			return err
		}
	}
	if !summary.Oldest.IsZero() {
		if _, err := fmt.Fprintf(out, "Publishing span: %s to %s\n", summary.Oldest.Format("2006-01-02"), summary.Newest.Format("2006-01-02")); err != nil {
			return err
		}
	}
	return nil
}

func writePostsCSV(out io.Writer, posts []post, summary postSummary) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"type", "title", "date", "category", "tags", "word_count"}); err != nil {
		return err
	}
	for _, post := range posts {
		date := ""
		if !post.Date.IsZero() {
			date = post.Date.Format("2006-01-02")
		}
		if err := w.Write([]string{post.Type, post.Title, date, post.Category, strings.Join(post.Tags, ", "), strconv.Itoa(post.Words)}); err != nil {
			return err
		}
	}
	rows := [][]string{{"SUMMARY", "Posts", "", "", "", strconv.Itoa(summary.Count)}, {"SUMMARY", "Total words", "", "", "", strconv.Itoa(summary.TotalWords)}, {"SUMMARY", "Average words per post", "", "", "", strconv.Itoa(summary.AverageWords)}}
	if summary.AverageInterval != "" {
		rows = append(rows, []string{"SUMMARY", "Average time between posts", summary.AverageInterval, "", "", ""})
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

var doctorChecks = map[string]bool{"build": true, "content": true, "urls": true, "links": true, "assets": true, "seo": true, "outputs": true}

func runDoctor(args []string, out io.Writer) error {
	opts, err := parseDoctorOptions(args)
	if err != nil {
		return &exitError{code: 2, message: err.Error()}
	}
	if opts.Remote {
		var findings []Finding
		var localURLs []string
		if opts.ProjectDir != "" {
			var localFindings []Finding
			localFindings, localURLs, err = doctorWithURLs(opts)
			if err != nil {
				return err
			}
			findings = append(findings, localFindings...)
		}
		remoteFindings, err := doctorRemote(opts)
		if err != nil {
			return &exitError{code: 4, message: err.Error()}
		}
		findings = append(findings, remoteFindings...)
		if len(localURLs) > 0 {
			remoteURLs, remoteErr := publishedSitemapURLs(opts.RemoteURL, opts.RemoteMaxPages, opts.RemoteTimeout)
			if remoteErr != nil {
				findings = append(findings, Finding{Code: "HS-REMOTE-020", Severity: SeverityWarning, Check: "remote", Message: "Could not compare the local build with the published sitemap: " + remoteErr.Error(), Target: opts.RemoteURL + "/sitemap.xml"})
			} else {
				findings = append(findings, comparePublishedURLs(localURLs, remoteURLs)...)
			}
		}
		findings = sortFindings(findings)
		if err := writeDoctorReport(out, opts.Format, findings); err != nil {
			return err
		}
		for _, f := range findings {
			if f.Severity == SeverityError || (opts.Strict && f.Severity == SeverityWarning) {
				return &exitError{code: 1}
			}
		}
		return nil
	}
	findings, err := doctor(opts)
	if err != nil {
		return err
	}
	if err := writeDoctorReport(out, opts.Format, findings); err != nil {
		return err
	}
	for _, f := range findings {
		if f.Severity == SeverityError || (opts.Strict && f.Severity == SeverityWarning) {
			return &exitError{code: 1}
		}
	}
	return nil
}

func parseDoctorOptions(args []string) (DoctorOptions, error) {
	opts := DoctorOptions{Format: "text", RemoteMaxPages: 100, RemoteTimeout: 20 * time.Second}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--strict":
			opts.Strict = true
		case "--build-drafts":
			opts.BuildDrafts = true
		case "--build-future":
			opts.BuildFuture = true
		case "--max-pages", "--timeout":
			if i+1 == len(args) {
				return opts, fmt.Errorf("%s requires a value", arg)
			}
			i++
			value, err := strconv.Atoi(args[i])
			if err != nil || value < 1 {
				return opts, fmt.Errorf("%s requires a positive number", arg)
			}
			if arg == "--max-pages" {
				opts.RemoteMaxPages = value
			} else {
				opts.RemoteTimeout = time.Duration(value) * time.Second
			}
		case "--remote":
			opts.Remote = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				base, err := normalizeBaseURL(args[i])
				if err != nil {
					return opts, err
				}
				opts.RemoteURL = base
			}
		case "--only", "--format", "--source":
			if i+1 == len(args) {
				return opts, fmt.Errorf("%s requires a value", arg)
			}
			i++
			if arg == "--only" {
				opts.OnlySet = true
				opts.Only = strings.Split(args[i], ",")
				for _, check := range opts.Only {
					if !doctorChecks[check] {
						return opts, fmt.Errorf("unknown doctor check %q", check)
					}
				}
			} else if arg == "--format" {
				opts.Format = args[i]
			} else {
				opts.Source = filepath.ToSlash(args[i])
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown doctor option %q", arg)
			}
			if opts.ProjectDir != "" {
				return opts, errors.New("usage: hs doctor [project-dir] [--remote URL] [--only checks] [--source content-file] [--strict] [--format text|json|sarif]")
			}
			opts.ProjectDir = arg
		}
	}
	if opts.Remote && opts.RemoteURL == "" {
		cfg, err := loadConfig()
		if err != nil {
			return opts, err
		}
		opts.RemoteURL = cfg.BaseURL
	}
	if opts.ProjectDir == "" && !opts.Remote {
		var err error
		opts.ProjectDir, err = os.Getwd()
		if err != nil {
			return opts, err
		}
	}
	if opts.Format != "text" && opts.Format != "json" && opts.Format != "sarif" {
		return opts, fmt.Errorf("unknown doctor format %q", opts.Format)
	}
	if len(opts.Only) == 0 {
		opts.Only = []string{"build", "content", "urls", "links", "assets", "seo", "outputs"}
	}
	if opts.Remote && opts.Source != "" {
		return opts, errors.New("--source is available only for local doctor checks")
	}
	if opts.Source != "" && (len(opts.Only) != 1 || opts.Only[0] != "content") {
		return opts, errors.New("--source requires --only content")
	}
	return opts, nil
}

// doctorRemote checks the public HTTP surface without sending credentials,
// cookies, or form data. It deliberately does not infer unpublished content.
func doctorRemote(opts DoctorOptions) ([]Finding, error) {
	base := opts.RemoteURL
	if base == "" {
		return nil, errors.New("remote site URL is required; pass --remote URL or configure one with hs site set")
	}
	client := &http.Client{Timeout: opts.RemoteTimeout}
	page, err := fetchRemotePage(client, base+"/")
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", base, err)
	}
	findings := []Finding{}
	if page.status < 200 || page.status > 299 {
		findings = append(findings, Finding{Code: "HS-REMOTE-001", Severity: SeverityError, Check: "remote", Message: fmt.Sprintf("Home page returned HTTP %d.", page.status), Target: base})
		return findings, nil
	}
	findings = append(findings, Finding{Code: "HS-REMOTE-INFO", Severity: SeverityInfo, Check: "remote", Message: "Home page responded successfully.", Target: base})
	findings = append(findings, remotePageFindings(base, page, true)...)
	for _, assetSpec := range []struct{ path, name string }{{"/robots.txt", "robots.txt"}, {"/sitemap.xml", "sitemap.xml"}, {"/index.json", "index.json"}} {
		asset, assetErr := fetchRemotePage(client, base+assetSpec.path)
		if assetErr != nil || asset.status < 200 || asset.status > 299 {
			findings = append(findings, Finding{Code: "HS-REMOTE-010", Severity: SeverityWarning, Check: "remote", Message: fmt.Sprintf("%s is not available.", assetSpec.name), Target: base + assetSpec.path})
			continue
		}
		if assetSpec.name == "sitemap.xml" && !validXML(asset.body) {
			findings = append(findings, Finding{Code: "HS-OUTPUT-001", Severity: SeverityError, Check: "outputs", Message: "Published sitemap.xml is malformed.", Target: base + assetSpec.path})
		} else if assetSpec.name == "index.json" && !json.Valid(asset.body) {
			findings = append(findings, Finding{Code: "HS-OUTPUT-002", Severity: SeverityError, Check: "outputs", Message: "Published index.json is malformed.", Target: base + assetSpec.path})
		} else {
			findings = append(findings, Finding{Code: "HS-REMOTE-011", Severity: SeverityInfo, Check: "remote", Message: assetSpec.name + " is available.", Target: base + assetSpec.path})
		}
	}
	urls, err := publishedSitemapURLs(base, opts.RemoteMaxPages, opts.RemoteTimeout)
	if err != nil {
		findings = append(findings, Finding{Code: "HS-REMOTE-012", Severity: SeverityWarning, Check: "remote", Message: "Could not crawl the published sitemap: " + err.Error(), Target: base + "/sitemap.xml"})
		return sortFindings(findings), nil
	}
	known := map[string]bool{}
	for _, target := range urls {
		known[target] = true
	}
	linkCache := map[string]bool{}
	for _, target := range urls {
		page, pageErr := fetchRemotePage(client, target)
		if pageErr != nil || page.status < 200 || page.status > 299 {
			findings = append(findings, Finding{Code: "HS-REMOTE-003", Severity: SeverityError, Check: "remote", Message: "Sitemap page is unavailable.", Target: target})
			continue
		}
		findings = append(findings, remotePageFindings(base, page, false)...)
		for _, link := range remoteInternalLinks(page) {
			if !sameOrigin(base, link) {
				continue
			}
			if linkCache[link] {
				continue
			}
			linkCache[link] = true
			if len(linkCache) > opts.RemoteMaxPages*5 {
				break
			}
			if !known[link] {
				linked, linkErr := fetchRemotePage(client, link)
				if linkErr != nil || linked.status < 200 || linked.status > 299 {
					findings = append(findings, Finding{Code: "HS-LINK-001", Severity: SeverityError, Check: "links", Message: "Internal link is unavailable.", Target: link})
				}
			}
		}
	}
	return sortFindings(findings), nil
}

type remotePage struct {
	status                              int
	contentType, requestedURL, finalURL string
	body                                []byte
}

func fetchRemotePage(client *http.Client, target string) (remotePage, error) {
	resp, err := client.Get(target)
	if err != nil {
		return remotePage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return remotePage{}, err
	}
	return remotePage{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), requestedURL: target, finalURL: resp.Request.URL.String(), body: body}, nil
}

func remotePageFindings(base string, page remotePage, home bool) []Finding {
	target := page.finalURL
	if target == "" {
		target = base
	}
	if !strings.Contains(strings.ToLower(page.contentType), "text/html") {
		return []Finding{{Code: "HS-REMOTE-002", Severity: SeverityWarning, Check: "remote", Message: "Page did not return an HTML content type.", Target: target}}
	}
	root, err := html.Parse(strings.NewReader(string(page.body)))
	if err != nil {
		return nil
	}
	title, description, canonical := remoteHTMLMetadata(root)
	var findings []Finding
	if page.requestedURL != "" && page.finalURL != "" && page.requestedURL != page.finalURL {
		findings = append(findings, Finding{Code: "HS-REMOTE-004", Severity: SeverityWarning, Check: "remote", Message: "Page redirected to a different URL.", Target: page.requestedURL})
	}
	if title == "" {
		findings = append(findings, Finding{Code: "HS-SEO-001", Severity: SeverityWarning, Check: "seo", Message: "Page has no non-empty title.", Target: target})
	}
	if description == "" {
		findings = append(findings, Finding{Code: "HS-SEO-002", Severity: SeverityWarning, Check: "seo", Message: "Page has no non-empty meta description.", Target: target})
	}
	if canonical == "" {
		findings = append(findings, Finding{Code: "HS-SEO-003", Severity: SeverityWarning, Check: "seo", Message: "Page has no canonical link.", Target: target})
	} else if !sameOrigin(base, absoluteURL(target, canonical)) {
		findings = append(findings, Finding{Code: "HS-SEO-004", Severity: SeverityWarning, Check: "seo", Message: "Page canonical points to a different origin.", Target: target})
	}
	_ = home
	return findings
}

func remoteInternalLinks(page remotePage) []string {
	root, err := html.Parse(strings.NewReader(string(page.body)))
	if err != nil {
		return nil
	}
	var links []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if strings.EqualFold(attr.Key, "href") {
					ref := absoluteURL(page.finalURL, attr.Val)
					parsed, err := url.Parse(ref)
					if err == nil && parsed.Scheme != "" && parsed.Host != "" {
						parsed.Fragment = ""
						links = append(links, parsed.String())
					}
					break
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return links
}

func sameOrigin(base, target string) bool {
	a, aErr := url.Parse(base)
	b, bErr := url.Parse(target)
	return aErr == nil && bErr == nil && a.Scheme == b.Scheme && a.Host == b.Host
}

func publishedSitemapURLs(base string, maxPages int, timeout time.Duration) ([]string, error) {
	client := &http.Client{Timeout: timeout}
	seenSitemaps, seenPages := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(sitemap string) error {
		if seenSitemaps[sitemap] {
			return nil
		}
		seenSitemaps[sitemap] = true
		page, err := fetchRemotePage(client, sitemap)
		if err != nil {
			return err
		}
		if page.status < 200 || page.status > 299 {
			return fmt.Errorf("%s returned HTTP %d", sitemap, page.status)
		}
		kind, locations := sitemapDocument(page.body)
		if kind == "" {
			return errors.New("sitemap.xml is malformed")
		}
		for _, location := range locations {
			target := absoluteURL(sitemap, location)
			if !sameOrigin(base, target) {
				continue
			}
			if kind == "sitemapindex" {
				if err := visit(target); err != nil {
					return err
				}
				continue
			}
			if len(seenPages) >= maxPages {
				return nil
			}
			seenPages[target] = true
		}
		return nil
	}
	if err := visit(strings.TrimRight(base, "/") + "/sitemap.xml"); err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(seenPages))
	for target := range seenPages {
		urls = append(urls, target)
	}
	sort.Strings(urls)
	return urls, nil
}

func sitemapDocument(data []byte) (string, []string) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	kind, inLoc := "", false
	var locations []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return kind, locations
		}
		if err != nil {
			return "", nil
		}
		switch value := token.(type) {
		case xml.StartElement:
			if kind == "" {
				kind = value.Name.Local
			}
			if value.Name.Local == "loc" {
				inLoc = true
			}
		case xml.CharData:
			if inLoc {
				locations = append(locations, strings.TrimSpace(string(value)))
			}
		case xml.EndElement:
			if value.Name.Local == "loc" {
				inLoc = false
			}
		}
	}
}

func comparePublishedURLs(localURLs, remoteURLs []string) []Finding {
	local, remote := map[string]bool{}, map[string]bool{}
	for _, target := range localURLs {
		local[target] = true
	}
	for _, target := range remoteURLs {
		parsed, err := url.Parse(target)
		if err == nil {
			remote[parsed.Path] = true
		}
	}
	var findings []Finding
	for target := range local {
		if !remote[target] {
			findings = append(findings, Finding{Code: "HS-REMOTE-021", Severity: SeverityWarning, Check: "remote", Message: "Local generated URL is absent from the published sitemap.", Target: target})
		}
	}
	for target := range remote {
		if !local[target] {
			findings = append(findings, Finding{Code: "HS-REMOTE-022", Severity: SeverityWarning, Check: "remote", Message: "Published sitemap URL is absent from the local build.", Target: target})
		}
	}
	return findings
}

func containsCheck(checks []string, wanted string) bool {
	for _, check := range checks {
		if check == wanted {
			return true
		}
	}
	return false
}

func remoteHTMLMetadata(root *html.Node) (title, description, canonical string) {
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			attrs := map[string]string{}
			for _, attr := range n.Attr {
				attrs[strings.ToLower(attr.Key)] = strings.TrimSpace(attr.Val)
			}
			switch strings.ToLower(n.Data) {
			case "title":
				if title == "" {
					title = strings.TrimSpace(nodeText(n))
				}
			case "meta":
				if strings.EqualFold(attrs["name"], "description") {
					description = attrs["content"]
				}
			case "link":
				if strings.EqualFold(attrs["rel"], "canonical") {
					canonical = attrs["href"]
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			b.WriteString(current.Data)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

func doctor(opts DoctorOptions) ([]Finding, error) {
	findings, _, err := doctorWithURLs(opts)
	return findings, err
}

func doctorWithURLs(opts DoctorOptions) ([]Finding, []string, error) {
	project, configFiles, err := validateHugoProject(opts.ProjectDir)
	if err != nil {
		return nil, nil, err
	}
	selected := make(map[string]bool)
	for _, check := range opts.Only {
		selected[check] = true
	}
	var findings []Finding
	if selected["content"] {
		findings = append(findings, doctorContent(project, opts.Source)...)
	}
	if !selected["build"] && !selected["urls"] && !selected["links"] && !selected["assets"] && !selected["seo"] && !selected["outputs"] {
		return sortFindings(findings), nil, nil
	}
	built, err := runHugoBuild(project, opts.BuildDrafts, opts.BuildFuture)
	if err != nil {
		return sortFindings(findings), nil, err
	}
	defer os.RemoveAll(built.OutputDir)
	findings = append(findings, Finding{Code: "HS-BUILD-INFO", Severity: SeverityInfo, Check: "build", Message: built.HugoVersion})
	findings = append(findings, buildWarningFindings(project, built.WarningDetails)...)
	if built.BuildErr != nil {
		findings = append(findings, Finding{Code: "HS-BUILD-001", Severity: SeverityError, Check: "build", Message: fmt.Sprintf("Hugo build failed after %s: %s", built.Duration.Round(time.Millisecond), strings.TrimSpace(built.Output)), Help: "Fix the Hugo build error and run doctor again."})
		return sortFindings(findings), nil, nil
	}
	if selected["build"] {
		findings = append(findings, Finding{Code: "HS-BUILD-003", Severity: SeverityInfo, Check: "build", Message: fmt.Sprintf("Hugo build completed in %s.", built.Duration.Round(time.Millisecond))})
	}
	if selected["urls"] {
		findings = append(findings, doctorURLs(built.Pages)...)
	}
	if selected["links"] || selected["assets"] || selected["seo"] {
		findings = append(findings, doctorHTML(built.Pages, built.Files, doctorBaseURL(configFiles), selected)...)
	}
	if selected["outputs"] {
		findings = append(findings, doctorOutputs(built.OutputDir, built.Pages)...)
	}
	return sortFindings(findings), generatedURLs(built.Pages), nil
}

// buildWarningFindings maps Hugo diagnostics back to content where possible.
// Hugo often reports a shortcode template rather than the Markdown page that
// invoked it, so identify every local invocation instead of hiding the useful
// post and line information behind the template warning.
func buildWarningFindings(project string, warnings []buildWarning) []Finding {
	var findings []Finding
	seen := map[string]bool{}
	appendFinding := func(finding Finding) {
		key := finding.Source + ":" + strconv.Itoa(finding.Line) + "\n" + finding.Message
		if !seen[key] {
			seen[key] = true
			findings = append(findings, finding)
		}
	}
	for _, warning := range warnings {
		base := Finding{Code: "HS-BUILD-002", Severity: SeverityWarning, Check: "build", Message: warning.Message}
		if source, line, ok := projectDiagnosticLocation(project, warning); ok {
			base.Source, base.Line = source, line
			appendFinding(base)
			continue
		}
		candidates := shortcodeSources(project, warning.Message)
		if len(candidates) == 0 {
			appendFinding(base)
			continue
		}
		for _, candidate := range candidates {
			finding := base
			finding.Source, finding.Line = candidate.Source, candidate.Line
			finding.Help = "Hugo reported this shortcode warning from its template; this is a content invocation that can trigger it."
			appendFinding(finding)
		}
	}
	return findings
}

func projectDiagnosticLocation(project string, warning buildWarning) (string, int, bool) {
	if warning.Source == "" || filepath.IsAbs(warning.Source) {
		return "", 0, false
	}
	source := filepath.ToSlash(warning.Source)
	info, err := os.Stat(filepath.Join(project, filepath.FromSlash(source)))
	if err != nil || info.IsDir() {
		return "", 0, false
	}
	return source, warning.Line, true
}

func shortcodeSources(project, message string) []Finding {
	match := shortcodeWarning.FindStringSubmatch(message)
	if len(match) != 2 {
		return nil
	}
	name := regexp.QuoteMeta(match[1])
	shortcode := regexp.MustCompile(`(?m)\{\{[<%]\s*` + name + `(?:\s|[>%])`)
	sources, err := contentSources(project)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var matches []Finding
	for _, source := range sources {
		_ = filepath.WalkDir(source.path, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(file))
			if ext != ".md" && ext != ".markdown" && ext != ".html" {
				return nil
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return nil
			}
			relative, err := filepath.Rel(project, file)
			if err != nil {
				return nil
			}
			relative = filepath.ToSlash(relative)
			for line, text := range strings.Split(string(data), "\n") {
				if shortcode.MatchString(text) && !seen[relative+":"+strconv.Itoa(line+1)] {
					seen[relative+":"+strconv.Itoa(line+1)] = true
					matches = append(matches, Finding{Source: relative, Line: line + 1})
				}
			}
			return nil
		})
	}
	return matches
}

func doctorContent(project string, selectedSource ...string) []Finding {
	sources, err := contentSources(project)
	if err != nil {
		return []Finding{{Code: "HS-CONTENT-001", Severity: SeverityError, Check: "content", Message: err.Error()}}
	}
	sourceFilter := ""
	if len(selectedSource) > 0 {
		sourceFilter = filepath.ToSlash(selectedSource[0])
	}
	found := sourceFilter == ""
	var findings []Finding
	for _, source := range sources {
		_ = filepath.WalkDir(source.path, func(file string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(file))
			if ext != ".md" && ext != ".markdown" && ext != ".html" {
				return nil
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return nil
			}
			meta, _ := parseFrontMatter(string(data))
			relative, _ := filepath.Rel(project, file)
			relative = filepath.ToSlash(relative)
			if sourceFilter != "" && relative != sourceFilter {
				return nil
			}
			found = true
			if meta.string("title") == "" {
				findings = append(findings, Finding{Code: "HS-CONTENT-002", Severity: SeverityWarning, Check: "content", Message: "Content file has no title.", Source: relative, Line: frontMatterLine(string(data), "title"), Help: "Add a title to the front matter."})
			}
			date := meta.string("date", "publishdate", "publishDate")
			if entry.Name() != "_index.md" && entry.Name() != "_index.markdown" && (date == "" || parseDate(date).IsZero()) {
				findings = append(findings, Finding{Code: "HS-CONTENT-003", Severity: SeverityWarning, Check: "content", Message: "Content file has a missing or invalid date.", Source: relative, Line: frontMatterLine(string(data), "date"), Help: "Add a valid date to the front matter."})
			}
			return nil
		})
	}
	if !found {
		return []Finding{{Code: "HS-CONTENT-004", Severity: SeverityError, Check: "content", Message: fmt.Sprintf("Content source was not found: %s", sourceFilter)}}
	}
	return findings
}

func frontMatterLine(source, key string) int {
	for index, line := range strings.Split(source, "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), strings.ToLower(key)+":") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), strings.ToLower(key)+" ") {
			return index + 1
		}
	}
	return 0
}

func discoverGenerated(root string) (map[string]generatedPage, map[string]bool, error) {
	pages, files := map[string]generatedPage{}, map[string]bool{}
	err := filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		files["/"+rel] = true
		if strings.EqualFold(filepath.Ext(rel), ".html") {
			urlPath := generatedURL(rel)
			pages[urlPath] = generatedPage{file: file, urlPath: urlPath, ids: map[string]bool{}}
		}
		return nil
	})
	return pages, files, err
}

func generatedURL(relative string) string {
	if relative == "index.html" {
		return "/"
	}
	if strings.HasSuffix(relative, "/index.html") {
		return "/" + strings.TrimSuffix(relative, "index.html")
	}
	return "/" + relative
}

func doctorURLs(pages map[string]generatedPage) []Finding {
	if len(pages) == 0 {
		return []Finding{{Code: "HS-URL-001", Severity: SeverityWarning, Check: "urls", Message: "Hugo generated no HTML pages."}}
	}
	return nil
}

func doctorHTML(pages map[string]generatedPage, files map[string]bool, baseURL string, selected map[string]bool) []Finding {
	var findings []Finding
	documents := make(map[string]*html.Node, len(pages))
	for pagePath, page := range pages {
		data, err := os.ReadFile(page.file)
		if err != nil {
			continue
		}
		doc, err := html.Parse(strings.NewReader(string(data)))
		if err != nil {
			findings = append(findings, Finding{Code: "HS-HTML-001", Severity: SeverityError, Check: "links", Message: "Generated HTML could not be parsed.", GeneratedPath: pagePath})
			continue
		}
		walkHTML(doc, func(node *html.Node) {
			if node.Type == html.ElementNode {
				if id := htmlAttributes(node)["id"]; id != "" {
					page.ids[id] = true
				}
			}
		})
		documents[pagePath] = doc
	}
	taxonomyDescriptionCount := map[string]int{}
	for pagePath, doc := range documents {
		if !isTaxonomyArchive(pagePath) || isPaginatedPage(pagePath) {
			continue
		}
		if description := htmlMetaDescription(doc); description != "" {
			taxonomyDescriptionCount[description]++
		}
	}
	for pagePath := range pages {
		doc, ok := documents[pagePath]
		if !ok {
			continue
		}
		var title, description, canonical string
		walkHTML(doc, func(node *html.Node) {
			if node.Type != html.ElementNode {
				return
			}
			name := strings.ToLower(node.Data)
			if name == "title" {
				title = strings.TrimSpace(htmlText(node))
			}
			attrs := htmlAttributes(node)
			if name == "meta" && strings.EqualFold(attrs["name"], "description") {
				description = strings.TrimSpace(attrs["content"])
			}
			if name == "link" && strings.EqualFold(attrs["rel"], "canonical") {
				canonical = attrs["href"]
			}
			if selected["assets"] && name == "img" && strings.TrimSpace(attrs["alt"]) == "" && attrs["role"] != "presentation" && strings.ToLower(attrs["aria-hidden"]) != "true" {
				findings = append(findings, Finding{Code: "HS-ASSET-002", Severity: SeverityWarning, Check: "assets", Message: "Image has empty alternative text.", GeneratedPath: pagePath, Help: "Add useful alt text or mark decorative images with role=presentation."})
			}
			for _, attribute := range htmlReferenceAttributes(name) {
				reference := attrs[attribute]
				if reference == "" {
					continue
				}
				kind := "links"
				code := "HS-LINK-001"
				if name != "a" {
					kind, code = "assets", "HS-ASSET-001"
				}
				if !selected[kind] || !isLocalReference(reference, baseURL) {
					continue
				}
				target, fragment := resolveReference(pagePath, reference)
				if target == "" {
					continue
				}
				if name == "a" {
					if _, ok := pages[target]; !ok && !files[target] && !files[path.Join(target, "index.html")] {
						findings = append(findings, Finding{Code: code, Severity: SeverityError, Check: kind, Message: "Internal link has no generated target.", GeneratedPath: pagePath, Target: reference, Help: "Update the link or add a redirect."})
						continue
					}
				} else if !files[target] && !files[path.Join(target, "index.html")] {
					findings = append(findings, Finding{Code: code, Severity: SeverityError, Check: kind, Message: "Local asset reference has no generated file.", GeneratedPath: pagePath, Target: reference, Help: "Update the reference or generate the asset."})
					continue
				}
				if fragment != "" {
					if targetPage, ok := pages[target]; ok && !targetPage.ids[fragment] {
						findings = append(findings, Finding{Code: "HS-LINK-002", Severity: SeverityError, Check: "links", Message: "Internal link fragment has no target.", GeneratedPath: pagePath, Target: reference})
					}
				}
			}
		})
		if selected["seo"] && !isPaginatedPage(pagePath) {
			if title == "" {
				findings = append(findings, Finding{Code: "HS-SEO-001", Severity: SeverityWarning, Check: "seo", Message: "Page has no non-empty title.", GeneratedPath: pagePath})
			}
			if description == "" {
				findings = append(findings, Finding{Code: "HS-SEO-002", Severity: SeverityWarning, Check: "seo", Message: "Page has no non-empty meta description.", GeneratedPath: pagePath})
			}
			if baseURL != "" && canonical == "" {
				findings = append(findings, Finding{Code: "HS-SEO-003", Severity: SeverityWarning, Check: "seo", Message: "Page has no canonical URL.", GeneratedPath: pagePath})
			}
			if isTaxonomyArchive(pagePath) && taxonomyDescriptionCount[description] > 1 {
				findings = append(findings, Finding{Code: "HS-SEO-004", Severity: SeverityInfo, Check: "seo", Message: "Taxonomy archive uses a shared rendered meta description.", GeneratedPath: pagePath, Help: "Add a page-specific description only for an important taxonomy hub."})
			}
		}
	}
	return findings
}

func walkHTML(node *html.Node, visit func(*html.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}
func htmlText(node *html.Node) string {
	var b strings.Builder
	walkHTML(node, func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
	})
	return b.String()
}
func htmlAttributes(node *html.Node) map[string]string {
	result := map[string]string{}
	for _, attr := range node.Attr {
		result[strings.ToLower(attr.Key)] = attr.Val
	}
	return result
}
func htmlMetaDescription(doc *html.Node) string {
	description := ""
	walkHTML(doc, func(node *html.Node) {
		if node.Type == html.ElementNode && strings.EqualFold(node.Data, "meta") {
			attrs := htmlAttributes(node)
			if strings.EqualFold(attrs["name"], "description") {
				description = strings.TrimSpace(attrs["content"])
			}
		}
	})
	return description
}
func isTaxonomyArchive(pagePath string) bool {
	parts := strings.Split(strings.Trim(pagePath, "/"), "/")
	return len(parts) >= 2 && (parts[0] == "tags" || parts[0] == "categories")
}
func isPaginatedPage(pagePath string) bool {
	parts := strings.Split(strings.Trim(pagePath, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "page" {
		return false
	}
	_, err := strconv.Atoi(parts[len(parts)-1])
	return err == nil
}
func htmlReferenceAttributes(name string) []string {
	switch name {
	case "a", "link":
		return []string{"href"}
	case "img", "script", "source", "iframe":
		return []string{"src"}
	}
	return nil
}

func isLocalReference(reference, baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return false
	}
	if parsed.Scheme == "mailto" || parsed.Scheme == "tel" || parsed.Scheme == "data" || parsed.Scheme == "javascript" || strings.HasPrefix(reference, "//") {
		return false
	}
	if parsed.IsAbs() {
		base, err := url.Parse(baseURL)
		return err == nil && baseURL != "" && parsed.Scheme == base.Scheme && parsed.Host == base.Host
	}
	return true
}

func resolveReference(from, reference string) (string, string) {
	reference = strings.ReplaceAll(reference, "\\", "/")
	parsed, err := url.Parse(reference)
	if err != nil {
		return "", ""
	}
	fragment := parsed.Fragment
	target := parsed.Path
	if target == "" {
		return from, fragment
	}
	if !strings.HasPrefix(target, "/") {
		target = path.Join(path.Dir(from), target)
	}
	if !strings.HasPrefix(target, "/") {
		target = "/" + target
	}
	if strings.HasSuffix(parsed.Path, "/") && !strings.HasSuffix(target, "/") {
		target += "/"
	}
	return target, fragment
}

func doctorOutputs(root string, pages map[string]generatedPage) []Finding {
	var findings []Finding
	_ = filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		name := strings.ToLower(entry.Name())
		if name == "sitemap.xml" || strings.HasSuffix(name, ".rss") || strings.HasSuffix(name, ".atom") || strings.HasSuffix(name, ".xml") {
			data, readErr := os.ReadFile(file)
			if readErr == nil {
				if !validXML(data) {
					findings = append(findings, Finding{Code: "HS-OUTPUT-001", Severity: SeverityError, Check: "outputs", Message: "Generated XML is malformed.", GeneratedPath: generatedPath(root, file)})
				} else if name == "sitemap.xml" {
					for _, location := range sitemapLocations(data) {
						parsed, err := url.Parse(location)
						if err != nil || parsed.Path == "" {
							continue
						}
						pagePath := parsed.Path
						if _, ok := pages[pagePath]; !ok {
							findings = append(findings, Finding{Code: "HS-OUTPUT-005", Severity: SeverityError, Check: "outputs", Message: "Sitemap URL has no generated page.", GeneratedPath: generatedPath(root, file), Target: location})
						}
					}
				}
			}
		}
		if name == "index.json" {
			data, readErr := os.ReadFile(file)
			if readErr != nil {
				return nil
			}
			var items []map[string]interface{}
			if json.Unmarshal(data, &items) != nil {
				findings = append(findings, Finding{Code: "HS-OUTPUT-002", Severity: SeverityError, Check: "outputs", Message: "Generated index.json is malformed.", GeneratedPath: generatedPath(root, file)})
			} else {
				for _, item := range items {
					title, _ := item["title"].(string)
					urlValue, _ := item["url"].(string)
					permalink, _ := item["permalink"].(string)
					if title == "" || (urlValue == "" && permalink == "") {
						findings = append(findings, Finding{Code: "HS-OUTPUT-003", Severity: SeverityWarning, Check: "outputs", Message: "Search index document has no title or URL.", GeneratedPath: generatedPath(root, file)})
					}
				}
			}
		}
		if name == "robots.txt" {
			data, _ := os.ReadFile(file)
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "sitemap:") {
					_, ref, _ := strings.Cut(line, ":")
					parsed, parseErr := url.Parse(strings.TrimSpace(ref))
					if parseErr == nil && parsed.Path != "" {
						candidate := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(parsed.Path, "/")))
						if _, statErr := os.Stat(candidate); statErr != nil {
							findings = append(findings, Finding{Code: "HS-OUTPUT-004", Severity: SeverityWarning, Check: "outputs", Message: "robots.txt references a sitemap that was not generated.", GeneratedPath: generatedPath(root, file), Target: strings.TrimSpace(ref)})
						}
					}
				}
			}
		}
		return nil
	})
	return findings
}

func validXML(data []byte) bool {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}
func sitemapLocations(data []byte) []string {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var locations []string
	inLoc := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return locations
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "loc" {
				inLoc = true
			}
		case xml.CharData:
			if inLoc {
				locations = append(locations, strings.TrimSpace(string(value)))
			}
		case xml.EndElement:
			if value.Name.Local == "loc" {
				inLoc = false
			}
		}
	}
}
func generatedPath(root, file string) string {
	rel, _ := filepath.Rel(root, file)
	return filepath.ToSlash(rel)
}

func doctorBaseURL(files []string) string {
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		ext := strings.ToLower(filepath.Ext(file))
		if ext == ".json" {
			var values map[string]interface{}
			if json.Unmarshal(data, &values) == nil {
				for key, value := range values {
					if strings.EqualFold(key, "baseURL") {
						if text, ok := value.(string); ok {
							return text
						}
					}
				}
			}
		} else {
			for _, line := range strings.Split(string(data), "\n") {
				key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
				if !ok {
					key, value, ok = strings.Cut(strings.TrimSpace(line), ":")
				}
				if ok && strings.EqualFold(strings.TrimSpace(key), "baseURL") {
					return cleanValue(value)
				}
			}
		}
	}
	return ""
}

func sortFindings(findings []Finding) []Finding {
	rank := map[Severity]int{SeverityError: 0, SeverityWarning: 1, SeverityInfo: 2}
	sort.SliceStable(findings, func(i, j int) bool {
		if rank[findings[i].Severity] != rank[findings[j].Severity] {
			return rank[findings[i].Severity] < rank[findings[j].Severity]
		}
		if findings[i].Source != findings[j].Source {
			return findings[i].Source < findings[j].Source
		}
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Code < findings[j].Code
	})
	return findings
}

func writeDoctorReport(out io.Writer, format string, findings []Finding) error {
	switch format {
	case "json":
		return json.NewEncoder(out).Encode(struct {
			Findings []Finding `json:"findings"`
		}{findings})
	case "sarif":
		results := make([]map[string]interface{}, 0, len(findings))
		for _, finding := range findings {
			result := map[string]interface{}{"ruleId": finding.Code, "level": string(finding.Severity), "message": map[string]string{"text": finding.Message}}
			if finding.Source != "" {
				result["locations"] = []map[string]interface{}{{"physicalLocation": map[string]interface{}{"artifactLocation": map[string]string{"uri": finding.Source}, "region": map[string]int{"startLine": finding.Line}}}}
			}
			results = append(results, result)
		}
		return json.NewEncoder(out).Encode(map[string]interface{}{"version": "2.1.0", "$schema": "https://json.schemastore.org/sarif-2.1.0.json", "runs": []map[string]interface{}{{"tool": map[string]interface{}{"driver": map[string]interface{}{"name": "hs"}}, "results": results}}})
	default:
		if len(findings) == 0 {
			_, err := fmt.Fprintln(out, "No findings.")
			return err
		}
		for _, finding := range findings {
			location := finding.GeneratedPath
			if finding.Source != "" {
				location = finding.Source
				if finding.Line > 0 {
					location += fmt.Sprintf(":%d", finding.Line)
				}
			}
			var err error
			if location != "" {
				err = writeFinding(out, "%s %s %s: %s\n", strings.ToUpper(string(finding.Severity)), finding.Code, location, finding.Message)
			} else {
				err = writeFinding(out, "%s %s: %s\n", strings.ToUpper(string(finding.Severity)), finding.Code, finding.Message)
			}
			if err != nil {
				return err
			}
			if finding.Help != "" {
				if _, err := fmt.Fprintf(out, "  Help: %s\n", finding.Help); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func writeFinding(out io.Writer, format string, args ...interface{}) error {
	_, err := fmt.Fprintf(out, format, args...)
	return err
}

func collectPosts(siteDir string) ([]post, error) {
	var posts []post
	sources, err := contentSources(siteDir)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, source := range sources {
		if err := collectPostsFromSource(source, &posts, seen); err != nil {
			return nil, err
		}
	}
	return posts, nil
}

func collectPostsFromSource(source contentSource, posts *[]post, seen map[string]bool) error {
	info, err := os.Stat(source.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // A missing optional mount contributes no content.
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}
	return filepath.WalkDir(source.path, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		name := entry.Name()
		if strings.HasPrefix(name, "_") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		meta, body := parseFrontMatter(string(data))
		rel, err := filepath.Rel(source.path, path)
		if err != nil {
			return err
		}
		virtualPath := filepath.ToSlash(filepath.Join(source.prefix, rel))
		if seen[virtualPath] {
			return nil
		}
		seen[virtualPath] = true
		parts := strings.Split(virtualPath, "/")
		postType := ""
		if len(parts) > 1 {
			postType = parts[0]
		}
		*posts = append(*posts, post{
			Type: postType, Title: meta.string("title"), Date: parseDate(meta.string("date", "publishdate", "publishDate")),
			Category: strings.Join(meta.strings("category", "categories"), ", "), Tags: meta.strings("tags", "tag"), Words: wordCount(body),
		})
		return nil
	})
}

func contentSources(siteDir string) ([]contentSource, error) {
	contentDir, mounts, err := readContentConfig(siteDir)
	if err != nil {
		return nil, err
	}
	var sources []contentSource
	for _, mount := range mounts {
		target := strings.Trim(filepath.ToSlash(filepath.Clean(mount.target)), "/")
		if target != "content" && !strings.HasPrefix(target, "content/") {
			continue
		}
		source := mount.source
		if !filepath.IsAbs(source) {
			source = filepath.Join(siteDir, source)
		}
		sources = append(sources, contentSource{path: filepath.Clean(source), prefix: strings.TrimPrefix(target, "content/")})
	}
	if len(sources) > 0 {
		return sources, nil
	}
	if contentDir == "" {
		contentDir = "content"
	}
	if !filepath.IsAbs(contentDir) {
		contentDir = filepath.Join(siteDir, contentDir)
	}
	if _, err := os.Stat(contentDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("no content directory found at %s", contentDir)
		}
		return nil, err
	}
	return []contentSource{{path: contentDir}}, nil
}

// readContentConfig reads the project-level configuration that determines the
// content component. Hugo's full configuration merge is environment-aware; hs
// uses the root configuration and config/_default, which are the settings Hugo
// applies in every environment.
func readContentConfig(siteDir string) (string, []moduleMount, error) {
	files, err := hugoConfigFiles(siteDir)
	if err != nil {
		return "", nil, err
	}
	contentDir := ""
	var mounts []moduleMount
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", nil, err
		}
		dir, fileMounts := parseHugoConfig(string(data), strings.ToLower(filepath.Ext(path)), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
		if dir != "" {
			contentDir = dir
		}
		if len(fileMounts) > 0 {
			mounts = fileMounts
		}
	}
	return contentDir, mounts, nil
}

func hugoConfigFiles(siteDir string) ([]string, error) {
	var files []string
	for _, name := range []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", "config.yaml", "config.yml", "config.json"} {
		path := filepath.Join(siteDir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files = append(files, path)
			break
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	defaultConfig := filepath.Join(siteDir, "config", "_default")
	if info, err := os.Stat(defaultConfig); err == nil && info.IsDir() {
		err = filepath.WalkDir(defaultConfig, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && isConfigExtension(filepath.Ext(path)) {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func isConfigExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".toml", ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func parseHugoConfig(source, ext, name string) (string, []moduleMount) {
	if ext == ".json" {
		return parseJSONConfig(source, name)
	}
	if ext == ".toml" {
		return parseTOMLConfig(source, name)
	}
	return parseYAMLConfig(source, name)
}

func parseJSONConfig(source, name string) (string, []moduleMount) {
	var value map[string]interface{}
	if json.Unmarshal([]byte(source), &value) != nil {
		return "", nil
	}
	if strings.EqualFold(name, "module") {
		value = map[string]interface{}{"module": value}
	}
	return configFromMap(value)
}

func configFromMap(value map[string]interface{}) (string, []moduleMount) {
	contentDir := ""
	for key, raw := range value {
		if strings.EqualFold(key, "contentDir") {
			if dir, ok := raw.(string); ok {
				contentDir = dir
			}
		}
	}
	var module map[string]interface{}
	for key, raw := range value {
		if strings.EqualFold(key, "module") {
			module, _ = raw.(map[string]interface{})
		}
	}
	if module == nil {
		return contentDir, nil
	}
	var mounts []moduleMount
	for key, raw := range module {
		if !strings.EqualFold(key, "mounts") {
			continue
		}
		items, _ := raw.([]interface{})
		for _, item := range items {
			fields, _ := item.(map[string]interface{})
			source, sourceOK := configString(fields, "source")
			target, targetOK := configString(fields, "target")
			if sourceOK && targetOK {
				mounts = append(mounts, moduleMount{source: source, target: target})
			}
		}
	}
	return contentDir, mounts
}

func configString(fields map[string]interface{}, key string) (string, bool) {
	for field, value := range fields {
		if strings.EqualFold(field, key) {
			text, ok := value.(string)
			return text, ok
		}
	}
	return "", false
}

func parseTOMLConfig(source, name string) (string, []moduleMount) {
	section := ""
	if strings.EqualFold(name, "module") {
		section = "module"
	}
	contentDir := ""
	var mounts []moduleMount
	current := -1
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
			section = strings.TrimSpace(line[2 : len(line)-2])
			if strings.EqualFold(section, "module.mounts") || (strings.EqualFold(name, "module") && strings.EqualFold(section, "mounts")) {
				mounts = append(mounts, moduleMount{})
				current = len(mounts) - 1
			}
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key, raw = strings.TrimSpace(key), cleanValue(raw)
		if section == "" && strings.EqualFold(key, "contentDir") {
			contentDir = raw
		}
		if (strings.EqualFold(section, "module.mounts") || (strings.EqualFold(name, "module") && strings.EqualFold(section, "mounts"))) && current >= 0 {
			if strings.EqualFold(key, "source") {
				mounts[current].source = raw
			}
			if strings.EqualFold(key, "target") {
				mounts[current].target = raw
			}
		}
	}
	return contentDir, validMounts(mounts)
}

func parseYAMLConfig(source, name string) (string, []moduleMount) {
	if strings.EqualFold(name, "module") {
		source = "module:\n  " + strings.ReplaceAll(source, "\n", "\n  ")
	}
	contentDir := ""
	var mounts []moduleMount
	inModule, inMounts, moduleIndent, mountsIndent, current := false, false, -1, -1, -1
	for _, rawLine := range strings.Split(source, "\n") {
		indent := len(rawLine) - len(strings.TrimLeft(rawLine, " \t"))
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		if line == "" {
			continue
		}
		if indent == 0 && strings.HasPrefix(strings.ToLower(line), "contentdir:") {
			_, value, _ := strings.Cut(line, ":")
			contentDir = cleanValue(value)
		}
		if strings.HasPrefix(strings.ToLower(line), "module:") {
			inModule, inMounts, moduleIndent = true, false, indent
			continue
		}
		if inModule && indent <= moduleIndent {
			inModule, inMounts = false, false
		}
		if inModule && strings.HasPrefix(strings.ToLower(line), "mounts:") {
			inMounts, mountsIndent = true, indent
			continue
		}
		if inMounts && indent <= mountsIndent {
			inMounts = false
		}
		if !inMounts {
			continue
		}
		if strings.HasPrefix(line, "-") {
			mounts = append(mounts, moduleMount{})
			current = len(mounts) - 1
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		}
		if current < 0 {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "source") {
			mounts[current].source = cleanValue(value)
		}
		if strings.EqualFold(strings.TrimSpace(key), "target") {
			mounts[current].target = cleanValue(value)
		}
	}
	return contentDir, validMounts(mounts)
}

func validMounts(mounts []moduleMount) []moduleMount {
	result := mounts[:0]
	for _, mount := range mounts {
		if mount.source != "" && mount.target != "" {
			result = append(result, mount)
		}
	}
	return result
}

type frontMatter map[string]interface{}

func (m frontMatter) string(keys ...string) string {
	for _, key := range keys {
		if value, ok := m[strings.ToLower(key)]; ok {
			switch v := value.(type) {
			case string:
				return v
			case []string:
				return strings.Join(v, ", ")
			}
		}
	}
	return ""
}

func (m frontMatter) strings(keys ...string) []string {
	for _, key := range keys {
		if value, ok := m[strings.ToLower(key)]; ok {
			switch v := value.(type) {
			case string:
				if v != "" {
					return []string{v}
				}
			case []string:
				return v
			}
		}
	}
	return nil
}

func parseFrontMatter(source string) (frontMatter, string) {
	source = strings.TrimPrefix(source, "\ufeff")
	if strings.HasPrefix(strings.TrimSpace(source), "{") {
		decoder := json.NewDecoder(strings.NewReader(source))
		var values map[string]interface{}
		if err := decoder.Decode(&values); err == nil {
			meta := frontMatter{}
			for key, value := range values {
				switch v := value.(type) {
				case string:
					meta[strings.ToLower(key)] = v
				case []interface{}:
					var stringsValue []string
					for _, item := range v {
						if text, ok := item.(string); ok {
							stringsValue = append(stringsValue, text)
						}
					}
					meta[strings.ToLower(key)] = stringsValue
				}
			}
			return meta, source[decoder.InputOffset():]
		}
	}
	lines := strings.Split(source, "\n")
	if len(lines) < 2 {
		return frontMatter{}, source
	}
	delimiter := strings.TrimSpace(lines[0])
	if delimiter != "---" && delimiter != "+++" {
		return frontMatter{}, source
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == delimiter {
			return parseFrontMatterFields(lines[1:i], delimiter == "+++"), strings.Join(lines[i+1:], "\n")
		}
	}
	return frontMatter{}, source
}

func parseFrontMatterFields(lines []string, toml bool) frontMatter {
	m := frontMatter{}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		separator := ":"
		if toml {
			separator = "="
		}
		key, raw, ok := strings.Cut(line, separator)
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		raw = strings.TrimSpace(raw)
		if raw == "" && !toml { // YAML block lists, for example: tags:\n  - Hugo
			var values []string
			for i+1 < len(lines) {
				next := strings.TrimSpace(lines[i+1])
				if !strings.HasPrefix(next, "-") {
					break
				}
				i++
				values = append(values, cleanValue(strings.TrimSpace(strings.TrimPrefix(next, "-"))))
			}
			if len(values) > 0 {
				m[key] = values
			}
			continue
		}
		m[key] = parseValue(raw)
	}
	return m
}

func parseValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		var values []string
		for _, value := range strings.Split(strings.TrimSpace(raw[1:len(raw)-1]), ",") {
			if value = cleanValue(value); value != "" {
				values = append(values, value)
			}
		}
		return values
	}
	return cleanValue(raw)
}

func cleanValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

func parseDate(value string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date
		}
	}
	return time.Time{}
}

func wordCount(body string) int { return len(strings.Fields(body)) }

func formatDuration(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	if hours == 0 {
		return fmt.Sprintf("%d days", days)
	}
	return fmt.Sprintf("%d days %d hours", days, hours)
}

func runSite(args []string, out io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs site set <base-url>\n       hs site show")
		return nil
	}

	switch args[0] {
	case "set":
		if len(args) != 2 {
			return errors.New("usage: hs site set <base-url>")
		}
		base, err := normalizeBaseURL(args[1])
		if err != nil {
			return err
		}
		if err := saveConfig(config{BaseURL: base}); err != nil {
			return err
		}
		fmt.Fprintln(out, base)
		return nil
	case "show":
		if len(args) != 1 {
			return errors.New("usage: hs site show")
		}
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, cfg.BaseURL)
		return nil
	default:
		return fmt.Errorf("unknown site command %q", args[0])
	}
}

func runSearch(args []string, out io.Writer) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(out, "Usage: hs search <terms...> [--limit N] [--json]")
		return nil
	}

	limit := 10
	jsonOutput := false
	var terms []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit":
			if i+1 == len(args) {
				return errors.New("--limit requires a positive number")
			}
			i++
			if _, err := fmt.Sscan(args[i], &limit); err != nil || limit < 1 {
				return errors.New("--limit requires a positive number")
			}
		case "--json":
			jsonOutput = true
		default:
			terms = append(terms, args[i])
		}
	}
	query := strings.TrimSpace(strings.Join(terms, " "))
	if query == "" {
		return errors.New("usage: hs search <terms...> [--limit N] [--json]")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	items, err := fetchIndex(cfg.BaseURL)
	if err != nil {
		return err
	}
	results := search(items, query, limit)
	if jsonOutput {
		return json.NewEncoder(out).Encode(results)
	}
	for _, item := range results {
		date := item.Date
		if date == "" {
			date = item.PublishDate
		}
		fmt.Fprintf(out, "%s\n%s\n%s\n\n", item.Title, item.URL, oneLine(firstNonEmpty(item.Summary, item.Description, item.Content), 180))
		if date != "" {
			fmt.Fprintf(out, "Published: %s\n\n", date)
		}
	}
	if len(results) == 0 {
		fmt.Fprintln(out, "No matching content.")
	}
	return nil
}

func fetchIndex(base string) ([]searchItem, error) {
	endpoint := strings.TrimRight(base, "/") + "/index.json"
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("fetch %s: server returned %s", endpoint, resp.Status)
	}
	var items []searchItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	for i := range items {
		items[i].URL = absoluteURL(base, firstNonEmpty(items[i].Permalink, items[i].URL))
	}
	return items, nil
}

func search(items []searchItem, query string, limit int) []result {
	words := strings.Fields(strings.ToLower(query))
	var results []result
	for _, item := range items {
		fields := []struct {
			value  string
			weight int
		}{
			{item.Title, 12}, {strings.Join(item.Tags, " "), 8}, {item.Summary, 4},
			{item.Description, 4}, {item.Content, 1},
		}
		score := 0
		for _, word := range words {
			matched := false
			for _, field := range fields {
				count := strings.Count(strings.ToLower(field.value), word)
				if count > 0 {
					matched = true
					score += count * field.weight
				}
			}
			if !matched {
				score = 0
				break
			}
		}
		if score > 0 {
			results = append(results, result{searchItem: item, Score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Title < results[j].Title
	})
	if len(results) > limit {
		return results[:limit]
	}
	return results
}

func normalizeBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("base URL must be an absolute http(s) URL")
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

func absoluteURL(base, raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	baseURL, _ := url.Parse(base)
	return baseURL.ResolveReference(u).String()
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appName, "config.json"), nil
}

func loadConfig() (config, error) {
	path, err := configPath()
	if err != nil {
		return config{}, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return config{}, errors.New("no site configured; run: hs site set <base-url>")
	}
	if err != nil {
		return config{}, err
	}
	defer f.Close()
	var cfg config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("read configuration: %w", err)
	}
	if cfg.BaseURL == "" {
		return config{}, errors.New("no site configured; run: hs site set <base-url>")
	}
	return cfg, nil
}

func saveConfig(cfg config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(cfg)
}

func isHelp(arg string) bool { return arg == "help" || arg == "--help" || arg == "-h" }
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max-1] + "…"
	}
	return s
}
func printUsage(out io.Writer) {
	fmt.Fprintln(out, "hs searches and audits Hugo sites.\n\nUsage:\n  hs site set <base-url>\n  hs site show\n  hs search <terms...> [--limit N] [--json]\n  hs posts [site-directory] [--verbose]\n  hs content <list|search|new|stats> ...\n  hs tui [project-directory] | hs tui --remote <base-url>\n  hs build [project-directory] [--build-drafts] [--build-future] [--format text|json]\n  hs urls [project-directory] [--format text|json] [--compare snapshot.json]\n  hs audit <seo|links> [project-directory] [--remote URL] [--strict] [--format text|json|sarif]\n  hs doctor [project-directory] [--remote URL] [--max-pages N] [--timeout SECONDS] [--only checks] [--source content-file] [--strict] [--format text|json|sarif]")
}
