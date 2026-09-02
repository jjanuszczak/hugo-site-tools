package app

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// embeddedWebAssets keeps `go install` self-contained for `hs web`.
//
//go:embed web_static
var embeddedWebAssets embed.FS

type webOptions struct {
	ProjectDir    string
	RepoURL       string
	Ref           string
	Subdir        string
	Port          string
	RelativePaths bool
}

type webProjectTarget struct {
	Project       string
	Kind          string
	Repository    string
	RequestedRef  string
	Commit        string
	Subdir        string
	RelativePaths bool
	cleanup       func()
}

const repositoryCloneTimeout = 2 * time.Minute

func (target webProjectTarget) close() {
	if target.cleanup != nil {
		target.cleanup()
	}
}

func runWeb(args []string, out io.Writer) error {
	opts, err := parseWebOptions(args)
	if err != nil {
		return &exitError{code: 2, message: err.Error()}
	}
	if opts.RepoURL != "" {
		fmt.Fprintf(out, "Cloning %s", opts.RepoURL)
		if opts.Ref != "" {
			fmt.Fprintf(out, " @ %s", opts.Ref)
		}
		fmt.Fprintln(out, "...")
	}
	target, err := resolveWebTarget(opts)
	if err != nil {
		return err
	}
	defer target.close()

	token, err := newWebToken()
	if err != nil {
		return &exitError{code: 3, message: err.Error()}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+opts.Port)
	if err != nil {
		return &exitError{code: 3, message: "start web server: " + err.Error()}
	}
	defer listener.Close()
	handler, err := newWebServer(target, token)
	if err != nil {
		return &exitError{code: 3, message: err.Error()}
	}
	server := &http.Server{Handler: handler}
	address := "http://" + listener.Addr().String() + "/?token=" + token
	fmt.Fprintf(out, "hs web is ready: %s\n", address)
	fmt.Fprintln(out, "Press Ctrl-C to stop the local server.")
	if os.Getenv("HS_WEB_NO_BROWSER") == "" {
		if err := openWebBrowser(address); err != nil {
			fmt.Fprintf(out, "Could not open browser: %s\n", err)
		}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-signals:
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func openWebBrowser(address string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{address}
	case "linux":
		command, args = "xdg-open", []string{address}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", address}
	default:
		return fmt.Errorf("automatic browser launch is not supported on %s", runtime.GOOS)
	}
	return exec.Command(command, args...).Start()
}

func parseWebOptions(args []string) (webOptions, error) {
	opts := webOptions{Port: "0"}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--relative-paths":
			opts.RelativePaths = true
		case "--repo", "--ref", "--subdir", "--port":
			if i+1 == len(args) {
				return opts, fmt.Errorf("%s requires a value", args[i])
			}
			i++
			switch args[i-1] {
			case "--repo":
				opts.RepoURL = args[i]
			case "--ref":
				opts.Ref = args[i]
			case "--subdir":
				opts.Subdir = args[i]
			case "--port":
				opts.Port = args[i]
			}
		default:
			if strings.HasPrefix(args[i], "-") || opts.ProjectDir != "" {
				return opts, errors.New("usage: hs web [project-directory] [--relative-paths] | hs web --repo <github-url> [--ref REF] [--subdir PATH] [--port PORT] [--relative-paths]")
			}
			opts.ProjectDir = args[i]
		}
	}
	if opts.RepoURL != "" && opts.ProjectDir != "" {
		return opts, errors.New("use either a local project directory or --repo, not both")
	}
	if opts.Ref != "" && opts.RepoURL == "" {
		return opts, errors.New("--ref requires --repo")
	}
	if opts.Subdir != "" && opts.RepoURL == "" {
		return opts, errors.New("--subdir requires --repo")
	}
	if opts.RepoURL == "" && opts.ProjectDir == "" {
		var err error
		opts.ProjectDir, err = os.Getwd()
		if err != nil {
			return opts, err
		}
	}
	if _, err := net.LookupPort("tcp", opts.Port); err != nil {
		return opts, fmt.Errorf("invalid --port %q", opts.Port)
	}
	return opts, nil
}

func resolveWebTarget(opts webOptions) (webProjectTarget, error) {
	if opts.RepoURL == "" {
		project, _, err := validateHugoProject(opts.ProjectDir)
		if err != nil {
			return webProjectTarget{}, err
		}
		return webProjectTarget{Project: project, Kind: "local", RelativePaths: opts.RelativePaths}, nil
	}
	repository, err := validateGitHubRepositoryURL(opts.RepoURL)
	if err != nil {
		return webProjectTarget{}, &exitError{code: 2, message: err.Error()}
	}
	if err := validateRepoSubdir(opts.Subdir); err != nil {
		return webProjectTarget{}, &exitError{code: 2, message: err.Error()}
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return webProjectTarget{}, &exitError{code: 3, message: "Git executable not found on PATH"}
	}
	checkout, err := os.MkdirTemp("", "hs-repo-")
	if err != nil {
		return webProjectTarget{}, &exitError{code: 3, message: err.Error()}
	}
	cleanup := func() { _ = os.RemoveAll(checkout) }
	arguments := []string{"clone", "--depth", "1", "--no-tags", "--no-recurse-submodules", "--progress"}
	if opts.Ref != "" {
		arguments = append(arguments, "--branch", opts.Ref)
	}
	arguments = append(arguments, repository, checkout)
	cloneContext, cancelClone := context.WithTimeout(context.Background(), repositoryCloneTimeout)
	defer cancelClone()
	cloneCommand := exec.CommandContext(cloneContext, git, arguments...)
	cloneCommand.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LFS_SKIP_SMUDGE=1")
	output, cloneErr := cloneCommand.CombinedOutput()
	if cloneContext.Err() != nil {
		cleanup()
		return webProjectTarget{}, &exitError{code: 3, message: fmt.Sprintf("clone repository: timed out after %s; check the repository URL, ref, and network connection", repositoryCloneTimeout)}
	}
	if cloneErr != nil {
		cleanup()
		return webProjectTarget{}, &exitError{code: 3, message: "clone repository: " + oneLine(string(output), 500)}
	}
	commitOut, err := exec.Command(git, "-C", checkout, "rev-parse", "HEAD").Output()
	if err != nil {
		cleanup()
		return webProjectTarget{}, &exitError{code: 3, message: "resolve repository commit: " + err.Error()}
	}
	project := filepath.Join(checkout, filepath.FromSlash(opts.Subdir))
	project, _, err = validateHugoProject(project)
	if err != nil {
		cleanup()
		return webProjectTarget{}, err
	}
	return webProjectTarget{Project: project, Kind: "repository", Repository: repository, RequestedRef: opts.Ref, Commit: strings.TrimSpace(string(commitOut)), Subdir: opts.Subdir, RelativePaths: opts.RelativePaths, cleanup: cleanup}, nil
}

func validateGitHubRepositoryURL(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "github.com") || strings.Trim(parsed.Path, "/") == "" {
		return "", errors.New("--repo must be an https://github.com/owner/repository URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("--repo must identify exactly one GitHub repository")
	}
	parsed.RawQuery, parsed.Fragment = "", ""
	parsed.Path = "/" + strings.TrimSuffix(strings.Join(parts, "/"), ".git") + ".git"
	return parsed.String(), nil
}

func validateRepoSubdir(subdir string) error {
	if subdir == "" {
		return nil
	}
	clean := filepath.ToSlash(filepath.Clean(subdir))
	if filepath.IsAbs(subdir) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return errors.New("--subdir must be a relative path within the repository")
	}
	return nil
}

func newWebToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

type webServer struct {
	target webProjectTarget
	token  string
	assets http.Handler
	mu     sync.RWMutex
	jobs   map[string]*webJob
	nextID int
}

type webJob struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"`
	Status     string      `json:"status"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt time.Time   `json:"finished_at,omitempty"`
	Result     interface{} `json:"result,omitempty"`
	Error      string      `json:"error,omitempty"`
}

func newWebServer(target webProjectTarget, token string) (*webServer, error) {
	assets, err := webAssetHandler()
	if err != nil {
		return nil, err
	}
	return &webServer{target: target, token: token, assets: assets, jobs: map[string]*webJob{}}, nil
}

func webAssetHandler() (http.Handler, error) {
	if configured := os.Getenv("HS_WEB_ASSETS"); configured != "" {
		if hasWebAssets(configured) {
			return http.FileServer(http.Dir(configured)), nil
		}
		return nil, errors.New("web assets not found; set HS_WEB_ASSETS to the directory containing index.html")
	}

	var candidates []string
	if working, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(working, "internal", "app", "web_static"), filepath.Join(working, "web_static"))
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), "web_static"))
	}
	for _, candidate := range candidates {
		if hasWebAssets(candidate) {
			return http.FileServer(http.Dir(candidate)), nil
		}
	}

	assets, err := fs.Sub(embeddedWebAssets, "web_static")
	if err != nil {
		return nil, fmt.Errorf("load embedded web assets: %w", err)
	}
	return http.FileServer(http.FS(assets)), nil
}

func hasWebAssets(directory string) bool {
	for _, relative := range []string{"index.html", "app.js", "web_vendor/jog/JOG.min.js", "web_vendor/chartjog/ChartJOG.Controls.js"} {
		info, err := os.Stat(filepath.Join(directory, filepath.FromSlash(relative)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

func (server *webServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if r.Header.Get("X-HS-Session") != server.token {
			writeWebError(w, http.StatusUnauthorized, "missing or invalid local session")
			return
		}
		server.serveAPI(w, r)
		return
	}
	if r.URL.Path == "/" {
		server.assets.ServeHTTP(w, r)
		return
	}
	server.assets.ServeHTTP(w, r)
}

func (server *webServer) serveAPI(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/project":
		writeWebJSON(w, http.StatusOK, map[string]string{"kind": server.target.Kind, "project": displayPath(server.target.Project, server.target.RelativePaths), "repository": server.target.Repository, "requested_ref": server.target.RequestedRef, "commit": server.target.Commit, "subdir": server.target.Subdir})
	case r.Method == http.MethodGet && r.URL.Path == "/api/content":
		server.serveWebContent(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/source":
		server.serveWebSource(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/stats":
		items, err := collectContent(server.target.Project)
		if err != nil {
			writeWebError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeWebJSON(w, http.StatusOK, makeContentStats(items))
	case r.Method == http.MethodGet && r.URL.Path == "/api/campaigns":
		policy, err := loadCampaignPolicy(server.target.Project)
		if err != nil {
			writeWebError(w, http.StatusNotFound, err.Error())
			return
		}
		writeWebJSON(w, http.StatusOK, policy)
	case r.Method == http.MethodPost && r.URL.Path == "/api/campaign-links":
		server.createWebCampaignLink(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/campaign-links/qr":
		server.serveWebCampaignQRCode(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/campaign-links/validate":
		server.validateWebCampaignLink(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/jobs":
		server.startWebJob(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/jobs/") && strings.HasSuffix(r.URL.Path, "/report"):
		server.serveWebJobReport(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/jobs/"):
		server.serveWebJob(w, r)
	default:
		writeWebError(w, http.StatusNotFound, "API endpoint not found")
	}
}

func (server *webServer) createWebCampaignLink(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ContentSource string `json:"content_source"`
		Campaign      string `json:"campaign"`
		Source        string `json:"source"`
		Medium        string `json:"medium"`
		Content       string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid campaign-link request")
		return
	}
	result, err := createCampaignLink(server.target.Project, request.ContentSource, request.Campaign, request.Source, request.Medium, request.Content)
	if err != nil {
		writeWebError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}

func (server *webServer) validateWebCampaignLink(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeWebError(w, http.StatusBadRequest, "invalid campaign-link validation request")
		return
	}
	result, err := validateCampaignURL(server.target.Project, request.URL)
	if err != nil {
		writeWebError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeWebJSON(w, http.StatusOK, result)
}

func (server *webServer) serveWebCampaignQRCode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.URL == "" {
		writeWebError(w, http.StatusBadRequest, "invalid QR-code request")
		return
	}
	if _, err := validateCampaignURL(server.target.Project, request.URL); err != nil {
		writeWebError(w, http.StatusBadRequest, err.Error())
		return
	}
	png, err := campaignQRCodePNG(request.URL)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `attachment; filename="campaign-qr.png"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(png)
}

type webContentItem struct {
	Title        string    `json:"title"`
	Date         time.Time `json:"date,omitempty"`
	Section      string    `json:"section"`
	Categories   []string  `json:"categories,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	Draft        bool      `json:"draft"`
	Words        int       `json:"word_count"`
	URL          string    `json:"url"`
	GeneratedURL string    `json:"generated_url"`
	Source       string    `json:"source"`
}

func (server *webServer) serveWebContent(w http.ResponseWriter, r *http.Request) {
	items, err := collectContent(server.target.Project)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return
	}
	query, section, tag, draft := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query"))), r.URL.Query().Get("section"), r.URL.Query().Get("tag"), r.URL.Query().Get("draft")
	response := make([]webContentItem, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{item.Title, strings.Join(item.Tags, " "), item.Body}, " "))
		if (query != "" && !containsAll(haystack, query)) || (section != "" && !strings.EqualFold(section, item.Section)) || (tag != "" && !hasString(item.Tags, tag)) || (draft != "" && draft != fmt.Sprint(item.Draft)) {
			continue
		}
		response = append(response, webContentItem{Title: item.Title, Date: item.Date, Section: item.Section, Categories: item.Categories, Tags: item.Tags, Draft: item.Draft, Words: item.Words, URL: item.URL, GeneratedURL: item.GeneratedURL, Source: item.Source})
	}
	writeWebJSON(w, http.StatusOK, response)
}

func (server *webServer) serveWebSource(w http.ResponseWriter, r *http.Request) {
	source := filepath.ToSlash(r.URL.Query().Get("path"))
	items, err := collectContent(server.target.Project)
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return
	}
	allowed := false
	for _, item := range items {
		if item.Source == source {
			allowed = true
			break
		}
	}
	if !allowed {
		writeWebError(w, http.StatusNotFound, "content source not found")
		return
	}
	data, err := os.ReadFile(filepath.Join(server.target.Project, filepath.FromSlash(source)))
	if err != nil {
		writeWebError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeWebJSON(w, http.StatusOK, map[string]string{"source": source, "text": string(data)})
}

func (server *webServer) startWebJob(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || (request.Kind != "build" && request.Kind != "doctor" && request.Kind != "seo" && request.Kind != "links") {
		writeWebError(w, http.StatusBadRequest, "kind must be build, doctor, seo, or links")
		return
	}
	server.mu.Lock()
	server.nextID++
	job := &webJob{ID: fmt.Sprintf("job-%d", server.nextID), Kind: request.Kind, Status: "running", StartedAt: time.Now().UTC()}
	server.jobs[job.ID] = job
	server.mu.Unlock()
	go server.runWebJob(job.ID)
	writeWebJSON(w, http.StatusAccepted, job)
}

func (server *webServer) runWebJob(id string) {
	server.mu.RLock()
	kind := server.jobs[id].Kind
	server.mu.RUnlock()
	var result interface{}
	var runErr error
	if kind == "build" {
		built, err := runHugoBuild(server.target.Project, false, false)
		if err != nil {
			runErr = err
		} else {
			defer os.RemoveAll(built.OutputDir)
			report := buildReport{Project: built.Project, HugoVersion: built.HugoVersion, Duration: built.Duration.Round(time.Millisecond).String(), Pages: len(built.Pages), Files: len(built.Files), OutputBytes: built.OutputBytes, Warnings: built.Warnings, Status: "success"}
			if built.BuildErr != nil {
				report.Status, report.Error = "failed", strings.TrimSpace(built.Output)
			}
			result = report
		}
	} else {
		only := []string{"build", "content", "urls", "links", "assets", "seo", "outputs"}
		if kind == "seo" || kind == "links" {
			only = []string{kind}
		}
		findings, err := doctor(DoctorOptions{ProjectDir: server.target.Project, Only: only, OnlySet: true})
		if err != nil {
			runErr = err
		} else {
			result = map[string]interface{}{"findings": findings}
		}
	}
	server.mu.Lock()
	job := server.jobs[id]
	job.FinishedAt = time.Now().UTC()
	if runErr != nil {
		job.Status, job.Error = "failed", runErr.Error()
	} else {
		job.Status, job.Result = "completed", result
	}
	server.mu.Unlock()
}

func (server *webServer) serveWebJob(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	server.mu.RLock()
	job, ok := server.jobs[id]
	if ok {
		copy := *job
		job = &copy
	}
	server.mu.RUnlock()
	if !ok {
		writeWebError(w, http.StatusNotFound, "job not found")
		return
	}
	writeWebJSON(w, http.StatusOK, job)
}

func (server *webServer) serveWebJobReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/jobs/"), "/report")
	server.mu.RLock()
	job, ok := server.jobs[id]
	if ok {
		copy := *job
		job = &copy
	}
	server.mu.RUnlock()
	if !ok {
		writeWebError(w, http.StatusNotFound, "job not found")
		return
	}
	if job.Status != "completed" {
		writeWebError(w, http.StatusConflict, "job is not completed")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "sarif" {
		writeWebError(w, http.StatusBadRequest, "format must be json or sarif")
		return
	}
	filename := "hs-" + job.Kind + ".json"
	body := job.Result
	if format == "sarif" {
		filename = "hs-" + job.Kind + ".sarif.json"
		body = webSARIFReport(job)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_ = json.NewEncoder(w).Encode(body)
}

func webSARIFReport(job *webJob) map[string]interface{} {
	results := []map[string]interface{}{}
	if payload, ok := job.Result.(map[string]interface{}); ok {
		if findings, ok := payload["findings"].([]Finding); ok {
			for _, finding := range findings {
				result := map[string]interface{}{
					"ruleId":  finding.Code,
					"level":   string(finding.Severity),
					"message": map[string]string{"text": finding.Message},
				}
				if finding.Source != "" {
					result["locations"] = []map[string]interface{}{{"physicalLocation": map[string]interface{}{
						"artifactLocation": map[string]string{"uri": finding.Source},
						"region":           map[string]int{"startLine": finding.Line},
					}}}
				}
				results = append(results, result)
			}
		}
	}
	return map[string]interface{}{
		"version": "2.1.0",
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"runs":    []map[string]interface{}{{"tool": map[string]interface{}{"driver": map[string]interface{}{"name": "hs"}}, "results": results}},
	}
}

func writeWebJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeWebError(w http.ResponseWriter, status int, message string) {
	writeWebJSON(w, status, map[string]string{"error": message})
}
