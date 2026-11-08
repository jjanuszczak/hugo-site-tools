package app

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

var hugoShortcode = regexp.MustCompile(`(?s)\{\{[<%].*?[>%]\}\}`)
var pandocVersionLine = regexp.MustCompile(`(?m)^pandoc ([0-9]+)\.([0-9]+)`)

const epubDefaultCSS = `body { font-family: serif; line-height: 1.55; margin: 5%; }
h1, h2, h3 { line-height: 1.2; }
img { max-width: 100%; height: auto; }
table { border-collapse: collapse; width: 100%; }
th, td { border: 1px solid #888; padding: 0.35em 0.55em; }
.rights { margin-top: 3em; font-size: 0.9em; text-align: center; }
a.footnote-ref { vertical-align: super; font-size: 0.75em; line-height: 0; text-decoration: none; }
.footnotes { font-size: 0.9em; }
pre { white-space: pre-wrap; }
blockquote { margin-left: 1.5em; color: #555; }
`

type epubOptions struct {
	source  string
	project string
	output  string
	css     string
}

type epubReplacement struct {
	Find    string `toml:"find"`
	Replace string `toml:"replace"`
	Scope   string `toml:"scope"`
	Format  string `toml:"format"`
}

type epubSettings struct {
	Replacements []epubReplacement `toml:"replacements"`
}

func runContentEPUB(args []string, out io.Writer) error {
	opts, err := parseEPUBOptions(args)
	if err != nil {
		return &exitError{code: 2, message: err.Error()}
	}
	path, err := exportPostEPUB(opts)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, path)
	return nil
}

func parseEPUBOptions(args []string) (epubOptions, error) {
	opts := epubOptions{}
	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output":
			if i+1 >= len(args) {
				return opts, errors.New("--output requires a path")
			}
			i++
			opts.output = args[i]
		case "--css":
			if i+1 >= len(args) {
				return opts, errors.New("--css requires a path")
			}
			i++
			opts.css = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return opts, fmt.Errorf("unknown EPUB option %q", args[i])
			}
			positional = append(positional, args[i])
		}
	}
	if len(positional) < 1 || len(positional) > 2 {
		return opts, errors.New("usage: hs content epub <content-file> [site-directory] [--output PATH] [--css PATH]")
	}
	opts.source = positional[0]
	if len(positional) == 2 {
		opts.project = positional[1]
	} else {
		var err error
		opts.project, err = os.Getwd()
		if err != nil {
			return opts, err
		}
	}
	opts.project, _ = filepath.Abs(opts.project)
	return opts, nil
}

func exportPostEPUB(opts epubOptions) (string, error) {
	pandoc, err := exec.LookPath("pandoc")
	if err != nil {
		return "", &exitError{code: 3, message: "Pandoc is required to export EPUB books. Install Pandoc 3.0 or later and ensure `pandoc` is on PATH."}
	}
	versionOutput, err := exec.Command(pandoc, "--version").Output()
	version := pandocVersionLine.FindStringSubmatch(string(versionOutput))
	if err != nil || len(version) != 3 {
		return "", &exitError{code: 3, message: "Could not read the Pandoc version. Install Pandoc 3.0 or later and ensure `pandoc` is on PATH."}
	}
	major, _ := strconv.Atoi(version[1])
	minor, _ := strconv.Atoi(version[2])
	if major < 3 || major == 3 && minor < 0 {
		return "", &exitError{code: 3, message: fmt.Sprintf("Pandoc 3.0 or later is required for EPUB export; found %s.%s", version[1], version[2])}
	}
	items, err := collectContent(opts.project)
	if err != nil {
		return "", &exitError{code: 3, message: err.Error()}
	}
	requested := filepath.Clean(filepath.FromSlash(opts.source))
	if filepath.IsAbs(requested) {
		rel, relErr := filepath.Rel(opts.project, requested)
		if relErr != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("content file must be inside the Hugo project: %s", opts.source)
		}
		requested = rel
	}
	var item *contentItem
	for i := range items {
		if filepath.Clean(filepath.FromSlash(items[i].Source)) == requested {
			item = &items[i]
			break
		}
	}
	if item == nil {
		return "", fmt.Errorf("markdown content file was not found: %s", opts.source)
	}
	sourcePath := filepath.Join(opts.project, filepath.FromSlash(item.Source))
	if ext := strings.ToLower(filepath.Ext(sourcePath)); ext != ".md" && ext != ".markdown" {
		return "", errors.New("epub export supports Markdown content files only")
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", err
	}
	meta, body := parseFrontMatter(string(data))
	title := strings.TrimSpace(meta.string("title"))
	if title == "" {
		return "", fmt.Errorf("%s has no title in its front matter", item.Source)
	}
	settings, err := loadEPUBSettings(opts.project)
	if err != nil {
		return "", err
	}
	title = applyEPUBReplacements(title, settings.Replacements, "title")
	if strings.TrimSpace(title) == "" {
		return "", errors.New("epub title is empty after applying replacement rules")
	}
	body = hugoShortcode.ReplaceAllString(body, "")
	body = applyEPUBReplacements(body, settings.Replacements, "body")

	output := opts.output
	if output == "" {
		output = filepath.Join(filepath.Dir(sourcePath), slugify(title)+".epub")
	}
	if !filepath.IsAbs(output) {
		output = filepath.Join(opts.project, output)
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return "", err
	}
	publicDir, _ := filepath.Abs(filepath.Join(opts.project, "public"))
	if output == publicDir || strings.HasPrefix(output, publicDir+string(filepath.Separator)) {
		return "", errors.New("epub output cannot be written inside the Hugo public/ directory")
	}
	if _, err = os.Stat(output); err == nil {
		return "", fmt.Errorf("output already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return "", err
	}

	cover, err := selectEPUBCover(sourcePath)
	if err != nil {
		return "", err
	}
	css := opts.css
	if css != "" {
		if !filepath.IsAbs(css) {
			css = filepath.Join(opts.project, css)
		}
		if _, err := os.Stat(css); err != nil {
			return "", fmt.Errorf("could not read EPUB stylesheet %s: %w", css, err)
		}
	} else {
		f, err := os.CreateTemp("", "hs-epub-*.css")
		if err != nil {
			return "", err
		}
		defer os.Remove(f.Name())
		if _, err := f.WriteString(epubDefaultCSS); err != nil {
			_ = f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		css = f.Name()
	}

	args := []string{"--from", "markdown", "--to", "epub3", "--standalone", "--css", css}
	if cover != "" {
		args = append(args, "--epub-cover-image", cover)
	}
	dateMetadata, cleanupDateMetadata, err := createEPUBDateMetadata(meta.string("date", "publishdate", "publishDate"))
	if err != nil {
		return "", err
	}
	if cleanupDateMetadata != nil {
		defer cleanupDateMetadata()
		args = append(args, "--epub-metadata", dateMetadata)
	}
	resourcePaths := []string{filepath.Dir(sourcePath), opts.project}
	if sources, sourceErr := contentSources(opts.project); sourceErr == nil {
		for _, s := range sources {
			resourcePaths = append(resourcePaths, s.path)
		}
	}
	args = append(args, "--resource-path", strings.Join(uniqueStrings(resourcePaths), string(os.PathListSeparator)), "-o", "-")
	metadata, err := epubMetadataBlock(title, meta)
	if err != nil {
		return "", fmt.Errorf("could not prepare EPUB metadata: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(output), ".hs-epub-*.epub")
	if err != nil {
		return "", err
	}
	tempPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tempPath)
	defer os.Remove(tempPath)
	args[len(args)-1] = tempPath
	cmd := exec.Command(pandoc, args...)
	cmd.Dir = filepath.Dir(sourcePath)
	cmd.Stdin = strings.NewReader(metadata + body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("pandoc EPUB conversion failed: %s", message)
	}
	if err := os.Rename(tempPath, output); err != nil {
		return "", fmt.Errorf("could not write EPUB %s: %w", output, err)
	}
	return output, nil
}

func loadEPUBSettings(project string) (epubSettings, error) {
	path := filepath.Join(project, ".hs.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return epubSettings{}, nil
	}
	if err != nil {
		return epubSettings{}, err
	}
	var config struct {
		EPUB epubSettings `toml:"epub"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return epubSettings{}, fmt.Errorf("invalid .hs.toml: %w", err)
	}
	for i := range config.EPUB.Replacements {
		rule := &config.EPUB.Replacements[i]
		if strings.TrimSpace(rule.Find) == "" {
			return epubSettings{}, fmt.Errorf(".hs.toml [epub.replacements] rule %d has an empty find value", i+1)
		}
		if rule.Scope == "" {
			rule.Scope = "body"
		}
		if rule.Scope != "body" && rule.Scope != "title" {
			return epubSettings{}, fmt.Errorf(".hs.toml [epub.replacements] rule %d has invalid scope %q; use body or title", i+1, rule.Scope)
		}
		if rule.Format == "" {
			rule.Format = "text"
		}
		if rule.Format != "text" && rule.Format != "markdown" {
			return epubSettings{}, fmt.Errorf(".hs.toml [epub.replacements] rule %d has invalid format %q; use text or markdown", i+1, rule.Format)
		}
		if rule.Scope == "title" && rule.Format == "markdown" {
			return epubSettings{}, fmt.Errorf(".hs.toml [epub.replacements] rule %d cannot use Markdown format in the title", i+1)
		}
	}
	return config.EPUB, nil
}

func applyEPUBReplacements(value string, rules []epubReplacement, scope string) string {
	for _, rule := range rules {
		if rule.Scope != scope {
			continue
		}
		replacement := rule.Replace
		if rule.Format == "text" && scope == "body" {
			replacement = escapeMarkdownText(replacement)
		}
		value = strings.ReplaceAll(value, rule.Find, replacement)
	}
	return value
}

func escapeMarkdownText(value string) string {
	var b strings.Builder
	for _, r := range value {
		if strings.ContainsRune("\\`*_{}[]()#+-.!>|~=", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func epubMetadataBlock(title string, meta frontMatter) (string, error) {
	var b strings.Builder
	b.WriteString("---\n")
	writeString := func(key string, value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s: %s\n", key, encoded)
		return nil
	}
	writeList := func(key string, values []string) error {
		var clean []string
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				clean = append(clean, value)
			}
		}
		if len(clean) == 0 {
			return nil
		}
		if len(clean) == 1 {
			return writeString(key, clean[0])
		}
		fmt.Fprintf(&b, "%s:\n", key)
		for _, value := range clean {
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			fmt.Fprintf(&b, "  - %s\n", encoded)
		}
		return nil
	}
	for _, field := range []struct{ key, value string }{
		{"title", title},
		{"subtitle", meta.string("subtitle")},
		{"lang", meta.string("language", "lang")},
		{"description", meta.string("description", "summary")},
		{"rights", meta.string("rights", "copyright")},
		{"publisher", meta.string("publisher")},
		{"identifier", meta.string("identifier", "isbn")},
		{"belongs-to-collection", meta.string("belongs-to-collection", "series")},
		{"group-position", meta.string("group-position", "seriesposition")},
	} {
		if err := writeString(field.key, field.value); err != nil {
			return "", err
		}
	}
	for _, field := range []struct {
		key     string
		aliases []string
	}{
		{"author", []string{"author", "authors"}},
		{"contributor", []string{"contributor", "contributors"}},
		{"subject", []string{"subject", "categories"}},
		{"keywords", []string{"keywords", "tags"}},
	} {
		if err := writeList(field.key, firstMetaStrings(meta, field.aliases...)); err != nil {
			return "", err
		}
	}
	b.WriteString("---\n\n")
	return b.String(), nil
}

func createEPUBDateMetadata(date string) (string, func(), error) {
	if strings.TrimSpace(date) == "" {
		return "", nil, nil
	}
	f, err := os.CreateTemp("", "hs-epub-metadata-*.xml")
	if err != nil {
		return "", nil, err
	}
	var escaped bytes.Buffer
	if err := xml.EscapeText(&escaped, []byte(strings.TrimSpace(date))); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, err
	}
	_, writeErr := fmt.Fprintf(f, `<dc:date xmlns:dc="http://purl.org/dc/elements/1.1/">%s</dc:date>`, escaped.String())
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(f.Name())
		return "", nil, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(f.Name())
		return "", nil, closeErr
	}
	path := f.Name()
	return path, func() { _ = os.Remove(path) }, nil
}

func firstMetaStrings(meta frontMatter, keys ...string) []string {
	for _, key := range keys {
		if values := meta.strings(key); len(values) > 0 {
			return values
		}
	}
	return nil
}

func selectEPUBCover(source string) (string, error) {
	if strings.ToLower(filepath.Base(source)) != "index.md" {
		return "", nil
	}
	dir := filepath.Dir(source)
	for _, name := range []string{"cover.png", "cover.jpg"} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = filepath.Clean(value)
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
