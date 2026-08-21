package app

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchRanksTitleAndRequiresAllTerms(t *testing.T) {
	items := []searchItem{
		{Title: "AI strategy", Content: "Executive strategy notes", URL: "/ai/"},
		{Title: "Other", Content: "AI strategy strategy strategy", URL: "/other/"},
		{Title: "AI only", Content: "No matching second word", URL: "/no/"},
	}
	got := search(items, "AI strategy", 10)
	if len(got) != 2 || got[0].Title != "AI strategy" {
		t.Fatalf("unexpected results: %#v", got)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	got, err := normalizeBaseURL("https://example.com/blog/?q=ignored#fragment")
	if err != nil || got != "https://example.com/blog" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestFetchIndexMakesURLsAbsolute(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"title":"Post","url":"/posts/one/"}]`))
	}))
	defer srv.Close()
	items, err := fetchIndex(srv.URL)
	if err != nil || len(items) != 1 || !strings.HasPrefix(items[0].URL, srv.URL) {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestDoctorRemoteAuditsExplicitURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><title>Home</title><meta name="description" content="A site"><link rel="canonical" href="/">`))
		case "/robots.txt":
			_, _ = w.Write([]byte("User-agent: *\n"))
		case "/sitemap.xml":
			_, _ = w.Write([]byte(`<?xml version="1.0"?><urlset></urlset>`))
		case "/index.json":
			_, _ = w.Write([]byte(`[{"title":"Post","url":"/post/"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	if err := run([]string{"doctor", "--remote", srv.URL, "--only", "seo"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Home page responded successfully") || strings.Contains(out.String(), "HS-SEO-00") {
		t.Fatalf("remote doctor output = %s", out.String())
	}
}

func TestRemoteTUITargetAndIndexLoading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[{"title":"Published post","url":"/post/","content":"Visible body"}]`))
	}))
	defer srv.Close()
	project, remote, err := tuiTarget([]string{"--remote", srv.URL})
	if err != nil || project != "" || remote != srv.URL {
		t.Fatalf("tuiTarget = %q, %q, %v", project, remote, err)
	}
	m := newRemoteTUIModel(remote)
	msg := m.loadRemoteContentCmd()()
	result, ok := msg.(tuiRemoteContentResult)
	if !ok || result.err != nil || len(result.items) != 1 || result.items[0].URL != srv.URL+"/post/" {
		t.Fatalf("remote TUI result = %#v", msg)
	}
}

func TestRemoteDoctorCrawlsSitemapAndFlagsBrokenInternalLink(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/one/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<title>Page</title><meta name="description" content="Description"><link rel="canonical" href="https://example.test/"><a href="/missing/">Missing</a>`))
		case "/sitemap.xml":
			_, _ = w.Write([]byte(`<?xml version="1.0"?><urlset><url><loc>` + srv.URL + `/one/</loc></url></urlset>`))
		case "/robots.txt", "/index.json":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	err := run([]string{"doctor", "--remote", srv.URL, "--max-pages", "5"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(out.String(), "Internal link is unavailable") {
		t.Fatalf("remote crawl err=%v output=%s", err, out.String())
	}
}

func TestRemoteTUIFallsBackToSitemap(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sitemap.xml" {
			_, _ = w.Write([]byte(`<?xml version="1.0"?><urlset><url><loc>` + srv.URL + `/guides/</loc></url></urlset>`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	msg := newRemoteTUIModel(srv.URL).loadRemoteContentCmd()()
	result := msg.(tuiRemoteContentResult)
	if result.err != nil || len(result.items) != 1 || result.items[0].Title != "/guides/" {
		t.Fatalf("sitemap fallback = %#v", result)
	}
}

func TestPostsWritesContentReportAsCSV(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "articles", "first.md"), `---
title: First, with a comma
date: 2026-01-01
categories: [Writing, Hugo]
tags:
  - Go
  - CLI
---
one two three`)
	writePost(t, filepath.Join(site, "content", "articles", "second.md"), `+++
title = "Second"
date = "2026-01-11T00:00:00Z"
category = "Notes"
tags = ["Hugo", "CSV"]
+++
four five`)
	writePost(t, filepath.Join(site, "content", "articles", "_index.md"), "---\ntitle: Section\n---\nnot a post")
	writePost(t, filepath.Join(site, "content", "notes", "third.md"), `{"title":"Third","date":"2026-01-21","category":"Updates","tags":["JSON"]}
six seven eight nine`)

	var out bytes.Buffer
	if err := run([]string{"posts", site, "--verbose"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := "type,title,date,category,tags,word_count\n" +
		"articles,\"First, with a comma\",2026-01-01,\"Writing, Hugo\",\"Go, CLI\",3\n" +
		"articles,Second,2026-01-11,Notes,\"Hugo, CSV\",2\n" +
		"notes,Third,2026-01-21,Updates,JSON,4\n" +
		"SUMMARY,Posts,,,,3\n" +
		"SUMMARY,Total words,,,,9\n" +
		"SUMMARY,Average words per post,,,,3\n" +
		"SUMMARY,Average time between posts,10 days,,,\n"
	if out.String() != want {
		t.Fatalf("report =\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestPostsDefaultsToSummary(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "one.md"), "---\ntitle: One\ndate: 2026-01-01\n---\none two three")
	writePost(t, filepath.Join(site, "content", "two.md"), "---\ntitle: Two\ndate: 2026-01-11\n---\nfour five")
	var out bytes.Buffer
	if err := run([]string{"posts", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := "Posts: 2\nTotal words: 5\nAverage words per post: 2\nAverage time between posts: 10 days\nPublishing span: 2026-01-01 to 2026-01-11\n"
	if out.String() != want {
		t.Fatalf("summary = %q, want %q", out.String(), want)
	}
}

func TestContentListSearchStatsAndWrite(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "articles", "one.md"), "---\ntitle: Fintech note\ndate: 2026-01-01\ntags: [Fintech]\ndraft: false\n---\none two three")
	writePost(t, filepath.Join(site, "content", "research", "two.md"), "---\ntitle: Research note\ndate: 2026-01-11\ntags: [Open Finance]\ndraft: true\n---\nfour five")
	var list bytes.Buffer
	if err := run([]string{"content", "list", site, "--format", "json"}, &list, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), `"url":"/articles/one/"`) || !strings.Contains(list.String(), `"draft":true`) {
		t.Fatalf("list = %s", list.String())
	}
	list.Reset()
	if err := run([]string{"content", "list", site, "--draft", "true"}, &list, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), "Research note") || strings.Contains(list.String(), "Fintech note") {
		t.Fatalf("draft list = %s", list.String())
	}
	list.Reset()
	if err := run([]string{"content", "list", site, "--draft", "false"}, &list, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), "Fintech note") || strings.Contains(list.String(), "Research note") {
		t.Fatalf("published list = %s", list.String())
	}
	var search bytes.Buffer
	if err := run([]string{"content", "search", "fintech", site, "--section", "articles"}, &search, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search.String(), "Fintech note") || strings.Contains(search.String(), "Research note") {
		t.Fatalf("search = %s", search.String())
	}
	var stats bytes.Buffer
	if err := run([]string{"content", "stats", site, "--write", "--draft", "--output", "private/site-stats"}, &stats, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stats.String(), "Posts: 2") {
		t.Fatalf("stats = %s", stats.String())
	}
	data, err := os.ReadFile(filepath.Join(site, "data", "site-stats.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"sections"`) {
		t.Fatalf("data = %s", data)
	}
	page, err := os.ReadFile(filepath.Join(site, "content", "private", "site-stats", "_index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "draft: true") || !strings.Contains(string(page), "hs_stats: true") {
		t.Fatalf("page = %s", page)
	}
}

func TestTUIContentFilterAndProjectParsing(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "published.md"), "---\ntitle: Published\ndate: 2026-01-01\ncategories: [Guides]\ndraft: false\n---\nbody")
	writePost(t, filepath.Join(site, "content", "draft.md"), "---\ntitle: Draft\ndate: 2026-02-01\ncategories: [Notes]\ndraft: true\n---\nbody")
	project, err := tuiProject([]string{site})
	if err != nil || project != site {
		t.Fatalf("tuiProject = %q, %v", project, err)
	}
	m := newTUIModel(project)
	m.loadContent()
	m.draftFilter = 1
	if items := m.filteredItems(); len(items) != 1 || items[0].Title != "Draft" {
		t.Fatalf("draft items = %#v", items)
	}
	m.draftFilter = 2
	if items := m.filteredItems(); len(items) != 1 || items[0].Title != "Published" {
		t.Fatalf("published items = %#v", items)
	}
	if view := m.contentView(); !strings.Contains(view, "Posts: 1 | Words: 1 | Avg: 1 words/post") || !strings.Contains(view, "1 words  Published") {
		t.Fatalf("content stats = %s", view)
	}
	m.draftFilter = 0
	m.query = "open finance"
	for i := range m.items {
		if m.items[i].Title == "Published" {
			m.items[i].Tags = []string{"Open Finance"}
		}
	}
	if items := m.filteredItems(); len(items) != 1 || items[0].Title != "Published" {
		t.Fatalf("search items = %#v", items)
	}
	m.query = ""
	m.section = "missing"
	if items := m.filteredItems(); len(items) != 0 {
		t.Fatalf("section items = %#v", items)
	}
	m.section = ""
	m.category = "Guides"
	if items := m.filteredItems(); len(items) != 1 || items[0].Title != "Published" {
		t.Fatalf("category items = %#v", items)
	}
	m.category = ""
	m.from, m.to = "2026-02-01", "2026-02-01"
	if items := m.filteredItems(); len(items) != 1 || items[0].Title != "Draft" {
		t.Fatalf("date items = %#v", items)
	}
}

func TestTUIShowsCommandResultAfterRunning(t *testing.T) {
	m := newTUIModel(t.TempDir())
	m.running = true
	updated, _ := m.Update(tuiCommandResult{output: "Build: success"})
	result := updated.(tuiModel)
	if result.running || result.result != "Build: success" {
		t.Fatalf("result = %#v", result)
	}
}

func TestTUIViewsDiagnosticSourceFromResult(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "entry.md"), "one\ntwo\n{{< x >}}\nfour")
	m := newTUIModel(site)
	m.screen = tuiResult
	updated, _ := m.Update(tuiCommandResult{output: "WARNING HS-BUILD-002 content/entry.md:3: shortcode warning"})
	result := updated.(tuiModel)
	if len(result.resultSources) != 1 || result.resultSources[0].path != "content/entry.md" || result.resultSources[0].line != 3 {
		t.Fatalf("result sources = %#v", result.resultSources)
	}
	updated, _ = result.updateResult("v")
	preview := updated.(tuiModel)
	if preview.screen != tuiPreview || preview.previewReturn != tuiResult || !strings.Contains(preview.previewText, ">    3 | {{< x >}}") {
		t.Fatalf("diagnostic preview = %#v", preview)
	}
	updated, _ = preview.updatePreview("esc")
	if updated.(tuiModel).screen != tuiResult {
		t.Fatalf("Esc did not return to result screen")
	}
}

func TestTUIFiltersResultDiagnosticsBySeverity(t *testing.T) {
	m := newTUIModel(t.TempDir())
	m.screen = tuiResult
	updated, _ := m.Update(tuiCommandResult{output: "WARNING HS-BUILD-002 content/entry.md:3: warning\n  Help: detail\nERROR HS-LINK-001: broken link\nINFO HS-BUILD-003: complete"})
	result := updated.(tuiModel)
	updated, _ = result.updateResult("e")
	result = updated.(tuiModel)
	if result.resultFilter != "error" || strings.Contains(result.filteredResult(), "warning") || !strings.Contains(result.filteredResult(), "broken link") {
		t.Fatalf("error filter = %q", result.filteredResult())
	}
	updated, _ = result.updateResult("w")
	result = updated.(tuiModel)
	if result.resultFilter != "warning" || !strings.Contains(result.filteredResult(), "Help: detail") || len(result.resultSources) != 1 {
		t.Fatalf("warning filter = %q, sources=%#v", result.filteredResult(), result.resultSources)
	}
}

func TestTUIResultBrowserMarksAndMovesSelectedFinding(t *testing.T) {
	m := newTUIModel(t.TempDir())
	m.screen = tuiResult
	updated, _ := m.Update(tuiCommandResult{output: "WARNING HS-BUILD-002: warning\nERROR HS-LINK-001: broken link\nINFO HS-BUILD-003: complete"})
	result := updated.(tuiModel)
	if !strings.Contains(result.resultView(), "> WARNING HS-BUILD-002") {
		t.Fatalf("initial result view lacks cursor:\n%s", result.resultView())
	}
	updated, _ = result.updateResult("down")
	result = updated.(tuiModel)
	if !strings.Contains(result.resultView(), "> ERROR HS-LINK-001") {
		t.Fatalf("result view did not move cursor:\n%s", result.resultView())
	}
}

func TestTUIWrapsLongDiagnosticLinesWithoutLosingText(t *testing.T) {
	m := newTUIModel(t.TempDir())
	m.screen = tuiResult
	m.width = 42
	message := "WARNING HS-BUILD-002 content/entry.md:3: WARN The x shortcode could not retrieve remote data from https://example.test/very/long/path"
	updated, _ := m.Update(tuiCommandResult{output: message})
	view := updated.(tuiModel).resultView()
	compact := strings.ReplaceAll(view, "\n", "")
	for _, want := range []string{"WARN The x shortcode", "https://example.test/very/long/path"} {
		if !strings.Contains(compact, want) {
			t.Fatalf("wrapped result lost %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "> WARNING HS-BUILD-002") {
		t.Fatalf("wrapped result lost cursor:\n%s", view)
	}
}

func TestTUIOpensSelectedContentSource(t *testing.T) {
	site := t.TempDir()
	path := filepath.Join(site, "content", "entry.md")
	writePost(t, path, "---\ntitle: Entry\n---\nsource body")
	m := newTUIModel(site)
	m.loadContent()
	m.openContent(m.items[0])
	if m.screen != tuiPreview || !strings.Contains(m.previewText, "title: Entry") || !strings.Contains(m.previewText, "source body") {
		t.Fatalf("preview = %#v", m)
	}
}

func TestTUIStartsDoctorForSelectedContent(t *testing.T) {
	m := newTUIModel(t.TempDir())
	m.screen = tuiContent
	m.items = []contentItem{{Source: "content/entry.md"}}
	updated, command := m.updateContent("d")
	result := updated.(tuiModel)
	if command == nil || !result.running || result.screen != tuiResult || !strings.Contains(result.command, "--only content --source content/entry.md") {
		t.Fatalf("result = %#v, command = %#v", result, command)
	}
}

func TestTUIShowsURLForSelectedContent(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte(`baseURL = "https://example.test/site/"`), 0644); err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(site)
	m.screen = tuiContent
	m.items = []contentItem{{URL: "https://elsewhere.test/", GeneratedURL: "/articles/entry/"}}
	updated, _ := m.updateContent("u")
	result := updated.(tuiModel)
	if result.screen != tuiURL || result.selectedURL != "https://example.test/site/articles/entry/" {
		t.Fatalf("result = %#v", result)
	}
}

func TestTUICreatesCampaignLinkForSelectedContent(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte(`baseURL = "https://example.test/"`), 0644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(site, "content", "entry.md")
	writePost(t, path, "---\ntitle: Entry\n---")
	var out bytes.Buffer
	if err := run([]string{"campaign", "init", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"campaign", "add", "always-on", site, "--label", "Always on", "--description", "Continuous distribution"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(site)
	m.screen = tuiContent
	m.items = []contentItem{{Title: "Entry", Source: "content/entry.md", GeneratedURL: "/entry/"}}
	updated, _ := m.updateContent("l")
	result := updated.(tuiModel)
	if result.screen != tuiCampaign || result.campaignKey != "always-on" || result.campaignSource != "newsletter" {
		t.Fatalf("campaign form = %#v", result)
	}
	updated, _ = result.updateCampaign("down")
	result = updated.(tuiModel)
	updated, _ = result.updateCampaign("right")
	result = updated.(tuiModel)
	if result.campaignSource != "x" || result.campaignMedium != "social" {
		t.Fatalf("source selection = %#v", result)
	}
	updated, _ = result.updateCampaign("down")
	result = updated.(tuiModel)
	updated, _ = result.updateCampaign("down")
	result = updated.(tuiModel)
	updated, _ = result.updateCampaign("enter")
	result = updated.(tuiModel)
	for _, key := range []string{"c", "e", "o", "-", "p", "o", "s", "t", "enter"} {
		updated, _ = result.updateCampaign(key)
		result = updated.(tuiModel)
	}
	updated, _ = result.updateCampaign("up")
	result = updated.(tuiModel)
	updated, _ = result.updateCampaign("enter")
	result = updated.(tuiModel)
	if result.screen != tuiCampaignReview || !strings.Contains(result.campaignReviewView(), "Expected GA4 channel: Organic Social") {
		t.Fatalf("campaign review = %#v", result)
	}
	updated, _ = result.updateCampaignReview("enter")
	result = updated.(tuiModel)
	if result.screen != tuiURL || !strings.Contains(result.selectedURL, "utm_source=x") || !strings.Contains(result.selectedURL, "utm_medium=social") || !strings.Contains(result.selectedURL, "utm_content=ceo-post") {
		t.Fatalf("campaign URL = %#v", result)
	}
}

func TestTUICampaignRegistryAddsAndRetiresCampaign(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte(`baseURL = "https://example.test/"`), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"campaign", "init", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(site)
	m.openCampaigns()
	updated, _ := m.updateCampaigns("a")
	result := updated.(tuiModel)
	for _, field := range [][]string{{"a", "l", "w", "a", "y", "s", "-", "o", "n"}, {"A", "l", "w", "a", "y", "s", " ", "o", "n"}, {"C", "o", "n", "t", "i", "n", "u", "o", "u", "s"}} {
		updated, _ = result.updateCampaignAdd("enter")
		result = updated.(tuiModel)
		for _, key := range field {
			updated, _ = result.updateCampaignAdd(key)
			result = updated.(tuiModel)
		}
		updated, _ = result.updateCampaignAdd("enter")
		result = updated.(tuiModel)
		updated, _ = result.updateCampaignAdd("down")
		result = updated.(tuiModel)
	}
	updated, _ = result.updateCampaignAdd("s")
	result = updated.(tuiModel)
	if result.screen != tuiCampaigns || len(result.campaignPolicy.Campaigns) != 1 || result.campaignPolicy.Campaigns[0].Key != "always-on" {
		t.Fatalf("campaign registry = %#v", result)
	}
	updated, _ = result.updateCampaigns("e")
	result = updated.(tuiModel)
	if result.screen != tuiCampaignAdd || !result.campaignEditing || result.campaignDraftKey != "always-on" || result.campaignAddField != 1 {
		t.Fatalf("campaign edit form = %#v", result)
	}
	result.campaignDraftLabel = "Always-on distribution"
	result.campaignDraftDesc = "Revised distribution context"
	updated, _ = result.updateCampaignAdd("s")
	result = updated.(tuiModel)
	if result.campaignPolicy.Campaigns[0].Label != "Always-on distribution" || result.campaignPolicy.Campaigns[0].Description != "Revised distribution context" {
		t.Fatalf("edited registry = %#v", result.campaignPolicy.Campaigns[0])
	}
	updated, _ = result.updateCampaigns("r")
	result = updated.(tuiModel)
	if result.screen != tuiCampaignRetire {
		t.Fatalf("retire confirmation = %#v", result)
	}
	updated, _ = result.updateCampaignRetire("enter")
	result = updated.(tuiModel)
	if result.screen != tuiCampaigns || result.campaignPolicy.Campaigns[0].Status != "retired" {
		t.Fatalf("retired registry = %#v", result)
	}
}

func TestTUICampaignRegistryInitializesPolicyAndCanExit(t *testing.T) {
	site := t.TempDir()
	m := newTUIModel(site)
	m.openCampaigns()
	if m.screen != tuiCampaigns || m.campaignPolicy.PolicyVersion != 1 {
		t.Fatalf("campaign policy = %#v", m)
	}
	if _, err := os.Stat(filepath.Join(site, ".hs.toml")); err != nil {
		t.Fatalf("campaign init did not create policy: %v", err)
	}
	updated, _ := m.updateCampaigns("enter")
	if updated.(tuiModel).screen != tuiMenu {
		t.Fatalf("Enter did not return to menu: %#v", updated)
	}
	m.screen = tuiCampaigns
	updated, _ = m.updateCampaigns("esc")
	if updated.(tuiModel).screen != tuiMenu {
		t.Fatalf("Esc did not return to menu: %#v", updated)
	}
}

func TestTUIPageNavigation(t *testing.T) {
	m := newTUIModel(t.TempDir())
	m.height = 12
	m.screen = tuiContent
	m.items = make([]contentItem, 20)
	updated, _ := m.updateContent("pgdown")
	result := updated.(tuiModel)
	if result.contentCursor != result.contentHeight() {
		t.Fatalf("page down cursor = %d", result.contentCursor)
	}
	updated, _ = result.updateContent("end")
	result = updated.(tuiModel)
	if result.contentCursor != 19 {
		t.Fatalf("end cursor = %d", result.contentCursor)
	}
	updated, _ = result.updateContent("home")
	result = updated.(tuiModel)
	if result.contentCursor != 0 {
		t.Fatalf("home cursor = %d", result.contentCursor)
	}

	m = newTUIModel(t.TempDir())
	m.height = 12
	m.screen = tuiPreview
	m.previewText = strings.Repeat("line\n", 20)
	updated, _ = m.updatePreview("space")
	result = updated.(tuiModel)
	if result.previewOffset != result.previewHeight() {
		t.Fatalf("preview page down offset = %d", result.previewOffset)
	}
	updated, _ = result.updatePreview("end")
	result = updated.(tuiModel)
	if result.previewOffset == 0 {
		t.Fatalf("preview end offset = %d", result.previewOffset)
	}
}

func TestContentNewCreatesFrontMatter(t *testing.T) {
	site := t.TempDir()
	if err := os.MkdirAll(filepath.Join(site, "content"), 0755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"content", "new", "A New Post", site, "--section", "articles", "--tag", "Fintech", "--draft", "--date", "2026-07-22"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(site, "content", "articles", "a-new-post.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `title: "A New Post"`) || !strings.Contains(string(data), "draft: true") {
		t.Fatalf("new content = %s", data)
	}
}

func TestPostsRequiresHugoContentDirectory(t *testing.T) {
	err := run([]string{"posts", t.TempDir()}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no content directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestPostsUsesContentDirFromHugoConfig(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "writing", "entry.md"), "---\ntitle: Custom root\ndate: 2026-02-01\n---\ncustom content")
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte(`contentDir = "writing"`), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"posts", site, "--verbose"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ",Custom root,2026-02-01,,,2\n") {
		t.Fatalf("report did not use contentDir:\n%s", out.String())
	}
}

func TestPostsUsesContentModuleMounts(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "ignored.md"), "---\ntitle: Ignored\n---\nnot counted")
	writePost(t, filepath.Join(site, "shared", "from-mount.md"), "---\ntitle: Mounted\ndate: 2026-03-01\n---\none two")
	if err := os.WriteFile(filepath.Join(site, "hugo.yaml"), []byte(`module:
  mounts:
    - source: shared
      target: content/articles
    - source: assets
      target: assets
`), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"posts", site, "--verbose"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Ignored") || !strings.Contains(out.String(), "articles,Mounted,2026-03-01,,,2") {
		t.Fatalf("report did not use content mount:\n%s", out.String())
	}
}

func TestPostsReadsModuleMountsFromConfigDirectory(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "docs-source", "page.md"), "---\ntitle: Config mount\n---\nthree words here")
	config := filepath.Join(site, "config", "_default", "module.json")
	if err := os.MkdirAll(filepath.Dir(config), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"mounts":[{"source":"docs-source","target":"content/docs"}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run([]string{"posts", site, "--verbose"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "docs,Config mount,,,,3") {
		t.Fatalf("report did not use config directory mount:\n%s", out.String())
	}
}

func TestDoctorContentStrictAndJSON(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "untitled.md"), "---\ndate: not-a-date\n---\nbody")
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := run([]string{"doctor", site, "--only", "content", "--strict", "--format", "json"}, &out, &bytes.Buffer{})
	var status *exitError
	if !errors.As(err, &status) || status.code != 1 {
		t.Fatalf("err = %#v, want exit 1", err)
	}
	if !strings.Contains(out.String(), `"HS-CONTENT-002"`) || !strings.Contains(out.String(), `"HS-CONTENT-003"`) {
		t.Fatalf("unexpected report: %s", out.String())
	}
}

func TestDoctorContentDoesNotRequireDateForSectionIndex(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "articles", "_index.md"), "---\ntitle: Articles\n---\nSection introduction")
	findings := doctorContent(site)
	for _, finding := range findings {
		if finding.Code == "HS-CONTENT-003" {
			t.Fatalf("section index received a date finding: %#v", finding)
		}
	}
}

func TestDoctorContentCanTargetOneSource(t *testing.T) {
	site := t.TempDir()
	writePost(t, filepath.Join(site, "content", "selected.md"), "---\n---\nbody")
	writePost(t, filepath.Join(site, "content", "other.md"), "---\n---\nbody")
	findings := doctorContent(site, "content/selected.md")
	if len(findings) != 2 {
		t.Fatalf("findings = %#v", findings)
	}
	for _, finding := range findings {
		if finding.Source != "content/selected.md" {
			t.Fatalf("finding targets %q, want selected source", finding.Source)
		}
	}
}

func TestBuildReportsProductionOutput(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	installFakeHugo(t, `
printf '%s\n' 'WARN sample warning'
mkdir -p "$dest/articles/one"
printf '%s' '<html></html>' > "$dest/index.html"
printf '%s' '<html></html>' > "$dest/articles/one/index.html"
printf '%s' 'body{}' > "$dest/site.css"
`)
	var out bytes.Buffer
	if err := run([]string{"build", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Build: success", "Pages: 2", "Files: 3", "Warning: WARN sample warning"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("build report missing %q:\n%s", want, out.String())
		}
	}
	out.Reset()
	if err := run([]string{"build", site, "--format", "json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"status":"success"`, `"pages":2`, `"files":3`, `"output_bytes":`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("JSON build report missing %q:\n%s", want, out.String())
		}
	}
}

func TestBuildWarningsKeepSuppressionsAndLocateShortcodeContent(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	writePost(t, filepath.Join(site, "content", "articles", "affected.md"), "---\ntitle: Affected\n---\nBefore\n{{< x id=\"123\" >}}\nAfter")
	installFakeHugo(t, `
printf '%s\n' 'WARN The "x" shortcode was unable to retrieve the remote data: template: layouts/_shortcodes/x.html:20:25: executing "render"'
printf '%s\n' 'WARN You can suppress this warning by adding ignoreErrors = ["error-remote-getjson"] to your site configuration.'
mkdir -p "$dest"
printf '%s' '<html></html>' > "$dest/index.html"
`)
	var out bytes.Buffer
	if err := run([]string{"audit", "links", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{
		"WARNING HS-BUILD-002 content/articles/affected.md:5: WARN The \"x\" shortcode was unable to retrieve the remote data",
		"You can suppress this warning by adding ignoreErrors",
		"Hugo reported this shortcode warning from its template",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report missing %q:\n%s", want, report)
		}
	}
	if strings.Count(report, "HS-BUILD-002") != 1 {
		t.Fatalf("suppression must remain part of one warning:\n%s", report)
	}
}

func TestBuildWarningsKeepFollowingSuppressionConfigurationAndDeduplicate(t *testing.T) {
	warnings := parseHugoWarnings("WARN The \"x\" shortcode was unable to retrieve remote data\nWARN You can suppress this warning by adding the following to your site configuration:\nignoreErrors = ['error-remote-getjson']\n")
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "ignoreErrors = ['error-remote-getjson']") {
		t.Fatalf("warnings = %#v", warnings)
	}
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	writePost(t, filepath.Join(site, "content", "entry.md"), "{{< x >}}")
	findings := buildWarningFindings(site, []buildWarning{warnings[0], warnings[0], warnings[0]})
	if len(findings) != 1 || findings[0].Source != "content/entry.md" || findings[0].Line != 1 {
		t.Fatalf("findings = %#v", findings)
	}
}

func TestURLsBuildsListsAndComparesSnapshot(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	installFakeHugo(t, `
mkdir -p "$dest/articles/one"
printf '%s' '<html></html>' > "$dest/index.html"
printf '%s' '<html></html>' > "$dest/articles/one/index.html"
`)
	var out bytes.Buffer
	if err := run([]string{"urls", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if out.String() != "/\n/articles/one/\n" {
		t.Fatalf("URLs = %q", out.String())
	}

	snapshot := filepath.Join(site, "urls.json")
	if err := os.WriteFile(snapshot, []byte(`{"urls":["/","/old/"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := run([]string{"urls", site, "--format", "json", "--compare", snapshot}, &out, &bytes.Buffer{})
	var status *exitError
	if !errors.As(err, &status) || status.code != 1 {
		t.Fatalf("err = %#v, want exit 1", err)
	}
	if !strings.Contains(out.String(), `"added":["/articles/one/"]`) || !strings.Contains(out.String(), `"removed":["/old/"]`) {
		t.Fatalf("comparison = %s", out.String())
	}
}

func TestCampaignCreatesValidStrictGA4Link(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/site/'"), 0644); err != nil {
		t.Fatal(err)
	}
	post := filepath.Join(site, "content", "articles", "payments.md")
	writePost(t, post, "---\ntitle: Payments\n---\nContent")
	var out bytes.Buffer
	if err := run([]string{"campaign", "init", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"campaign", "add", "sea-fintech-thought-leadership", site, "--label", "SEA fintech thought leadership", "--description", "Ongoing distribution"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run([]string{"campaign", "link", post, site, "--campaign", "sea-fintech-thought-leadership", "--source", "linkedin", "--medium", "social", "--content", "ceo-post"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := "https://example.test/site/articles/payments/?utm_campaign=sea-fintech-thought-leadership&utm_content=ceo-post&utm_medium=social&utm_source=linkedin\n"
	if out.String() != want {
		t.Fatalf("link = %q, want %q", out.String(), want)
	}
	out.Reset()
	if err := run([]string{"campaign", "validate", strings.TrimSpace(want), site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Valid campaign link") {
		t.Fatalf("validation = %q", out.String())
	}
}

func TestCampaignRejectsInvalidPairAndAllowsRetiredValidation(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	post := filepath.Join(site, "content", "entry.md")
	writePost(t, post, "---\ntitle: Entry\n---")
	var out bytes.Buffer
	for _, args := range [][]string{
		{"campaign", "init", site},
		{"campaign", "add", "always-on", site, "--label", "Always on", "--description", "Continuous distribution"},
	} {
		if err := run(args, &out, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		out.Reset()
	}
	err := run([]string{"campaign", "link", post, site, "--campaign", "always-on", "--source", "linkedin", "--medium", "email"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("invalid pairing error = %v", err)
	}
	if err := run([]string{"campaign", "retire", "always-on", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"campaign", "link", post, site, "--campaign", "always-on", "--source", "linkedin", "--medium", "social"}, &out, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("retired creation error = %v", err)
	}
	if err := run([]string{"campaign", "validate", "https://example.test/entry/?utm_source=linkedin&utm_medium=social&utm_campaign=always-on", site}, &out, &bytes.Buffer{}); err != nil {
		t.Fatalf("retired validation: %v", err)
	}
}

func TestDoctorFindsGeneratedBrokenLink(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	installFakeHugo(t, `
mkdir -p "$dest"
printf '%s' '<!doctype html><html><head><title>Home</title><meta name="description" content="desc"><link rel="canonical" href="https://example.test/"></head><body><a href="/missing/">broken</a></body></html>' > "$dest/index.html"
`)
	var out bytes.Buffer
	err := run([]string{"doctor", site, "--only", "build,links,seo", "--format", "json"}, &out, &bytes.Buffer{})
	var status *exitError
	if !errors.As(err, &status) || status.code != 1 {
		t.Fatalf("err = %#v, want exit 1", err)
	}
	if !strings.Contains(out.String(), `"HS-LINK-001"`) || !strings.Contains(out.String(), `"target":"/missing/"`) {
		t.Fatalf("unexpected report: %s", out.String())
	}
}

func TestAuditSEOAndLinksReuseDoctorChecks(t *testing.T) {
	site := t.TempDir()
	if err := os.WriteFile(filepath.Join(site, "hugo.toml"), []byte("baseURL = 'https://example.test/'"), 0644); err != nil {
		t.Fatal(err)
	}
	installFakeHugo(t, `
mkdir -p "$dest"
printf '%s' '<!doctype html><html><head><title>Home</title><link rel="canonical" href="https://example.test/"></head><body><a href="/missing/">broken</a></body></html>' > "$dest/index.html"
`)
	var out bytes.Buffer
	if err := run([]string{"audit", "seo", site, "--format", "json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"HS-SEO-002"`) {
		t.Fatalf("SEO audit did not report missing description: %s", out.String())
	}
	out.Reset()
	err := run([]string{"audit", "links", site, "--format", "json"}, &out, &bytes.Buffer{})
	var status *exitError
	if !errors.As(err, &status) || status.code != 1 {
		t.Fatalf("err = %#v, want exit 1", err)
	}
	if !strings.Contains(out.String(), `"HS-LINK-001"`) {
		t.Fatalf("links audit did not report broken link: %s", out.String())
	}
}

func TestDoctorSEOIgnoresPaginatedPagesAndReportsSharedTaxonomyFallbackAsInfo(t *testing.T) {
	output := t.TempDir()
	writeGeneratedHTML(t, output, "tags/fintech/index.html", `<html><head><title>Fintech</title><meta name="description" content="Site fallback"><link rel="canonical" href="https://example.test/tags/fintech/"></head></html>`)
	writeGeneratedHTML(t, output, "tags/open-finance/index.html", `<html><head><title>Open finance</title><meta name="description" content="Site fallback"><link rel="canonical" href="https://example.test/tags/open-finance/"></head></html>`)
	writeGeneratedHTML(t, output, "tags/fintech/page/1/index.html", `<html><head></head></html>`)
	writeGeneratedHTML(t, output, "articles/page/1/index.html", `<html><head></head></html>`)
	pages, files, err := discoverGenerated(output)
	if err != nil {
		t.Fatal(err)
	}
	findings := doctorHTML(pages, files, "https://example.test/", map[string]bool{"seo": true})
	shared := 0
	for _, finding := range findings {
		if finding.GeneratedPath == "/tags/fintech/page/1/" || finding.GeneratedPath == "/articles/page/1/" {
			t.Fatalf("paginated page should be ignored, got %#v", finding)
		}
		if finding.Code == "HS-SEO-004" {
			shared++
		}
	}
	if shared != 2 {
		t.Fatalf("shared fallback findings = %d, want 2; findings=%#v", shared, findings)
	}
}

func writeGeneratedHTML(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func installFakeHugo(t *testing.T, buildScript string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "hugo")
	contents := "#!/bin/sh\nif [ \"$1\" = \"version\" ]; then echo 'hugo v0.test'; exit 0; fi\nwhile [ $# -gt 0 ]; do\n  if [ \"$1\" = \"--destination\" ]; then dest=$2; shift 2; continue; fi\n  shift\ndone\n" + buildScript
	if err := os.WriteFile(script, []byte(contents), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writePost(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
