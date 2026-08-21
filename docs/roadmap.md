# hs product roadmap and `doctor` specification

## Product position

`hs` is a command-line toolkit for building, validating, and operating Hugo websites. It should work first against a local Hugo project, where it can prevent a bad release, and then against a configured public site, where it can verify what visitors actually receive.

The existing commands establish two useful foundations:

```sh
hs site set https://example.com
hs search "market entry"
```

`hs site set` stores the deployed-site base URL. Local commands take a project directory explicitly, defaulting to the current directory.

## Roadmap

### Phase 1: local content and release safety

Build these before adding deployment integrations. They solve day-to-day Hugo problems and have no vendor dependency.

| Command | Purpose | Priority |
| --- | --- | --- |
| `hs content list [dir]` | List content with title, date, section, tags, draft state, word count, and generated URL. Filter draft state with `--draft true|false`. | High |
| `hs content search <terms> [dir]` | Search local front matter and Markdown, with section, tag, draft, and date filters. | High |
| `hs content new <title>` | Create content from the project archetype with date, section, tag, and draft options. | High |
| `hs doctor [dir]` | Build and audit the project before release. | High |
| `hs urls [dir]` | List generated URLs and identify duplicates or changed permalink rules. | High |
| `hs build [dir]` | Standardise a production Hugo build and report pages, output size, timing, and warnings. | Delivered |

### Phase 2: content quality and discovery

| Command | Purpose | Priority |
| --- | --- | --- |
| `hs audit seo [dir]` | Identify missing titles, descriptions, canonical URLs, Open Graph images, alt text, and duplicate metadata. | Delivered |
| `hs audit links [dir]` | Check local content links and generated internal links. External checks remain opt-in. | Delivered |
| `hs search index [dir]` | Generate, validate, inspect, and query the local `index.json` search index. | Medium |
| `hs related <content-file>` | Recommend related content from taxonomy and text similarity. | Medium |
| `hs redirects [dir]` | Generate redirect rules for moved URLs in Netlify, Cloudflare, Nginx, and Apache formats. | Medium |
| `hs campaign` | Create and validate strict GA4-compatible UTM links for local Hugo content, including a TUI workflow for selected content. | Medium |

### Phase 3: deployed-site assurance and automation

| Command | Purpose | Priority |
| --- | --- | --- |
| `hs doctor --remote [URL]` | Crawl same-origin sitemap pages and verify public HTTP behaviour, links, canonical URLs, and standard Hugo outputs. | Delivered |
| `hs doctor [dir] --remote [URL]` | Compare a local production build with the deployed sitemap. | Delivered |
| `hs deploy check [dir]` | Check release readiness and detect stale or incomplete deployments. | Medium |
| CI integration | Stable exit codes, JSON and SARIF output, and GitHub Actions examples. | Medium |

## `hs doctor` PRD

### Problem

Hugo builds fast and produces static files, but it does not give developers one reliable pre-release audit. Broken links, missing front matter, absent social images, invalid output files, and incorrect URL configuration often reach production because the checks live in separate scripts or do not exist.

### Goal

Give a Hugo developer one command that builds a site as it will be deployed, identifies release-blocking defects and quality gaps, and returns output suitable for both a terminal and CI.

### Non-goals for the first release

- Editing content or configuration automatically.
- Crawling arbitrary external URLs by default.
- Rendering JavaScript or using a browser engine.
- Replacing Hugo's own build and asset pipeline.
- Guaranteeing visual layout quality across browsers.

### Users and primary jobs

| User | Job |
| --- | --- |
| Hugo developer | Catch site errors before pushing a release. |
| Maintainer | Enforce agreed content, link, and metadata standards in CI. |
| Site operator | Confirm that the public site matches the expected release. |

### Command interface

```sh
# Local project audit. The directory defaults to the current directory.
hs doctor [project-dir]

# Run only selected checks.
hs doctor [project-dir] --only build,content,links,seo

# Treat warnings as a failed command.
hs doctor [project-dir] --strict

# Machine-readable output.
hs doctor [project-dir] --format text|json|sarif

# Include draft and future content in the local production-equivalent build.
hs doctor [project-dir] --build-drafts --build-future

# Audit the configured public site.
hs doctor --remote

# Build locally, then compare the expected URL set with the public site.
hs doctor [project-dir] --remote
```

Defaults:

- `project-dir`: current working directory.
- local build: production settings, excluding drafts, future content, and expired content unless explicitly requested.
- output format: text.
- checks: `build,content,urls,links,assets,seo,outputs`.
- network access: disabled in local mode. `--remote` enables requests only to the configured site origin.

### Functional requirements

#### Project discovery and build

1. Locate `hugo.toml`, `hugo.yaml`, `hugo.json`, or the legacy `config.*` file in the supplied project directory.
2. Confirm that the Hugo executable is available and report its version.
3. Build into a newly created temporary destination, never into the project's configured `public/` directory.
4. Use the project's configuration, modules, themes, and environment. Capture Hugo stdout, stderr, exit code, and duration.
5. Create an `ERROR` finding when Hugo fails. Retain all available build output as evidence.
6. Create a `WARN` finding for recognized Hugo warning lines.

#### Content checks

1. Parse front matter in Markdown and HTML content files.
2. Flag missing `title` and invalid or unparseable date values.
3. Flag duplicate canonical paths, aliases, and slugs where Hugo does not already fail the build.
4. Flag a draft, future, or expired page present in a default production build.
5. Report the source file and front-matter line when that location can be determined.

#### URL and link checks

1. Construct the set of generated HTML paths from the temporary build output.
2. Parse generated HTML using an HTML parser, not regular expressions.
3. Validate internal `a[href]`, `img[src]`, `script[src]`, `link[href]`, `source[src]`, and `iframe[src]` references.
4. Ignore fragments when resolving a path, then validate that a non-empty local fragment target exists when the target HTML document is available.
5. Treat mailto, tel, data, JavaScript, protocol-relative external URLs, and different-origin HTTP URLs as out of scope in local mode.
6. Support `--check-external` in a later release. It must be opt-in, rate-limited, and have a timeout.

#### Asset checks

1. Flag local asset references with no generated file.
2. Flag empty `img` alternative text, except images marked decorative with `role="presentation"`, `aria-hidden="true"`, or an explicit project exclusion.
3. Flag image URLs referenced by configured Open Graph metadata when their generated asset is absent.

#### SEO checks

1. Flag pages with no non-empty `<title>`.
2. Flag pages with no non-empty `meta[name="description"]`.
3. Flag duplicate title and duplicate description values. The severity is configurable.
4. Flag missing or invalid canonical URLs when the project config defines a base URL.
5. Flag missing `og:title`, `og:description`, and `og:image` only when the project declares an Open Graph policy file or the checks are explicitly enabled. Do not impose theme-specific fields by default.

#### Output checks

1. Validate XML syntax for `sitemap.xml` and RSS/Atom feeds that exist in the build.
2. Validate JSON syntax for `index.json` when it exists.
3. Flag an index document that has no title or URL/permalink.
4. Validate that each URL in the sitemap resolves to a generated page for same-origin URLs.
5. Inspect `robots.txt` when it exists and report sitemap references that do not point to a generated sitemap.

#### Remote checks

1. Require a configured base URL from `hs site set`.
2. Request the root page, `robots.txt`, `sitemap.xml`, `index.json`, and RSS only when the local build or standard paths indicate they should exist.
3. Check final status code, redirect chain, HTTPS, canonical host, canonical link, and content type.
4. Crawl same-origin sitemap URLs with a configurable cap. Do not crawl pages discovered only through arbitrary links in the first release.
5. In combined local and remote mode, compare local generated canonical paths with public sitemap paths and report missing and unexpected pages.
6. Do not send credentials, cookies, analytics identifiers, or form data.

### Finding model and exit codes

Every finding contains:

```json
{
  "code": "HS-LINK-001",
  "severity": "error",
  "check": "links",
  "message": "Internal link has no generated target.",
  "source": "content/articles/payments.md",
  "line": 42,
  "generated_path": "articles/payments/index.html",
  "target": "/articles/old-post/",
  "help": "Update the link or add a redirect."
}
```

Severity levels:

| Severity | Meaning | Default exit impact |
| --- | --- | --- |
| `error` | A build failure, broken generated reference, invalid output, or release-blocking configuration defect. | Non-zero |
| `warning` | A quality or policy gap that does not break the generated site. | Zero, unless `--strict` |
| `info` | A useful fact or non-blocking recommendation. | Zero |

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | No errors. Warnings are permitted unless `--strict` is set. |
| `1` | Findings exceed the selected severity threshold. |
| `2` | Invalid command arguments or configuration. |
| `3` | Hugo was not available, could not be executed, or the project could not be inspected. |
| `4` | Remote mode could not reach or validate the configured site. |

### Configuration

Project-specific policy belongs in `.hs.toml` at the project root. It is optional in the first release.

```toml
[doctor]
required_front_matter = ["title", "date"]
ignore_paths = ["/search/", "/privacy/"]
warning_as_error = ["HS-SEO-001"]

[doctor.seo]
require_open_graph = true
allow_empty_alt_for = ["/icons/"]

[doctor.remote]
max_sitemap_urls = 500
timeout_seconds = 15
```

Command-line options override project policy. Project policy overrides built-in defaults.

## Implementation specification

### Design approach

Implement `doctor` as a package that does not depend on command-line output. The command layer parses flags, builds a `DoctorOptions` value, invokes a runner, and sends structured findings to a formatter. This keeps the audit testable and lets CI consume JSON or SARIF without parsing terminal text.

Suggested package layout:

```text
cmd/hs/                 CLI entrypoint and command registration
internal/config/        hs configuration and .hs.toml policy
internal/doctor/        runner, finding model, check registry
internal/doctor/build/  Hugo invocation and temporary build lifecycle
internal/doctor/site/   generated output discovery and HTML/XML/JSON parsing
internal/doctor/checks/ build, content, urls, links, assets, seo, outputs, remote
internal/report/        text, JSON, and SARIF formatters
```

The process entrypoint now lives in `cmd/hs` and the existing implementation in `internal/app`. Split `internal/app` into this structure before substantial new `doctor` work. Keep the existing `site` and `search` public behaviour stable.

### Core types

```go
type DoctorOptions struct {
    ProjectDir    string
    Remote        bool
    Only          []string
    Strict        bool
    Format        string
    BuildDrafts   bool
    BuildFuture   bool
    CheckExternal bool
}

type Finding struct {
    Code          string      `json:"code"`
    Severity      Severity    `json:"severity"`
    Check         string      `json:"check"`
    Message       string      `json:"message"`
    Source        string      `json:"source,omitempty"`
    Line          int         `json:"line,omitempty"`
    GeneratedPath string      `json:"generated_path,omitempty"`
    Target        string      `json:"target,omitempty"`
    Help          string      `json:"help,omitempty"`
}

type Check interface {
    Name() string
    Run(context.Context, *RunContext) ([]Finding, error)
}
```

`RunContext` holds the absolute project path, parsed policy, Hugo version, temporary output path, discovered generated files, parsed URL map, and optional remote client. Checks may read shared facts but must not mutate project files.

### Execution flow

1. Resolve and validate the project directory.
2. Load project Hugo configuration and optional `.hs.toml` policy.
3. Resolve selected checks and validate their dependencies.
4. Run the build check, which creates the temporary output directory and runs Hugo.
5. If the build failed, return its findings. Skip checks requiring generated output, but still run safe source-only checks.
6. Discover generated files and build an in-memory map from canonical paths to output files.
7. Run independent local checks concurrently with a bounded worker group.
8. Run remote checks only after local output discovery and only when `--remote` is present.
9. Sort findings deterministically by severity, source, line, and code.
10. Render the selected output format and return the documented exit code.

### Hugo invocation

Use `os/exec` with the project directory as the working directory. Invoke the executable as:

```sh
hugo --destination <temporary-directory> --environment production
```

Add `--buildDrafts` and `--buildFuture` only when requested. Do not pass shell strings. Capture bounded stdout and stderr buffers so a pathological build cannot exhaust memory. Always remove the temporary directory after checks complete, including error paths.

Respect Hugo's configured base URL by default. For local link analysis, generated paths are the source of truth, not a development server URL.

### HTML and URL handling

- Use `golang.org/x/net/html` or an equivalent standards-aware parser for HTML.
- Resolve generated references with `net/url` and normalise paths with `path`, never `filepath`.
- Preserve Hugo's trailing slash semantics. Map both `/article/` and `/article/index.html` to the same generated page for validation.
- Decode HTML entities before comparison.
- Detect escaped root-relative paths and base-relative paths.
- Avoid false positives for Hugo page resources that intentionally compile to fingerprinted assets by validating the generated destination, not the source filename.

### Remote client

Use a dedicated `http.Client` with a timeout, redirect limit, same-origin enforcement, a descriptive `User-Agent` such as `hs/<version>`, and no cookie jar. Reject a configured URL without an `http` or `https` scheme. Limit sitemap processing to the configured cap and use bounded concurrency.

### Tests and fixtures

Tests must not require the public internet or a globally installed Hugo binary.

1. Unit-test parsing, URL normalisation, finding sorting, configuration precedence, and each check using fixture output directories.
2. Use `httptest.Server` for all remote-check tests.
3. Use small fixture projects for configuration discovery and front matter cases.
4. Abstract the Hugo process runner so tests can provide a fake build result. Add a separate integration suite, skipped unless Hugo is installed, for real builds.
5. Cover exit codes, text output snapshots, JSON schema, and SARIF validity.
6. Add regression fixtures for trailing slashes, base URL subpaths, multilingual output, taxonomy pages, aliases, page bundles, and fingerprinted assets.

### Acceptance criteria for the first release

- `hs doctor .` succeeds against a valid minimal Hugo project without writing inside the project.
- A Hugo build failure returns exit code `1` with the captured build error.
- A generated internal link to a nonexistent page returns `HS-LINK-001` with source and target.
- Missing title, missing generated image, malformed `index.json`, and malformed sitemap each produce stable findings.
- `--only` runs only requested checks and validates unknown names as argument errors.
- `--strict` converts warning-only audits to exit code `1`.
- `--format json` produces valid, documented JSON. `--format sarif` validates against the SARIF schema used by common CI tools.
- `hs doctor --remote` never contacts a domain other than the configured site's origin unless a future explicit external-check option is provided.
- All findings are deterministic for the same project, policy, and Hugo version.

### Delivery sequence

1. Refactor the CLI into packages without changing current commands.
2. Add the finding model, formatter, project discovery, policy loading, and exit codes.
3. Add temporary Hugo builds and the `build` check.
4. Add output discovery, then `urls`, `links`, and `outputs` checks.
5. Add `content`, `assets`, and baseline `seo` checks.
6. Add JSON and SARIF output, fixtures, and CI documentation.
7. Add remote checks and local-versus-remote comparison.

The local release audit is the first shippable milestone. Remote comparison should not delay it.
