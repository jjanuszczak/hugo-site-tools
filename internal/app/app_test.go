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
