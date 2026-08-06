# hs

`hs` is a command-line toolkit for inspecting, validating, and operating Hugo websites. It works against a local Hugo project and, when configured, its published JSON search index.

## Install

```sh
go install github.com/johnjanuszczak/hugo-site-tools/cmd/hs@latest
```

For local development:

```sh
go build -o ./bin/hs ./cmd/hs
```

## Quick start

```sh
# Configure a deployed site that publishes /index.json.
hs site set https://example.com
hs site show
hs search "market entry"

# Inspect a local Hugo project.
hs content list .
hs content list . --draft true
hs content search fintech . --section articles --tag "Open Finance"
hs content stats .
hs tui .
hs build .
hs urls .
hs doctor .
```

The configured site URL is stored in the operating-system configuration directory, for example `~/Library/Application Support/hs/config.json` on macOS. Each search fetches `<base-url>/index.json`, so results remain current.

## Commands

### Search a published Hugo index

`hs search` requires every query word to match a title, tag, summary, description, or body. Title and tag matches rank above body-text matches.

```sh
hs search "AI strategy" --limit 5 --json
```

Your site must publish `index.json`. A typical Hugo configuration is:

```toml
[outputs]
home = ["HTML", "RSS", "JSON"]
```

### Work with local content

```sh
hs posts /path/to/hugo-site
hs posts /path/to/hugo-site --verbose
hs content list . --format json
hs content list . --draft true
hs content list . --draft false
hs content search fintech . --section articles --tag "Open Finance" --draft false
hs tui .
hs content new "A new post" . --section articles --tag Fintech --draft
hs content stats . --write --draft --output private/site-stats
```

`posts` reads YAML, TOML, and JSON front matter. `content list` includes draft state, word count, and generated path; its JSON output also includes each source path. `--draft true|false` narrows the list without requiring a search query. `content search` accepts section, tag, draft, and date-range filters. All content commands understand the project's configured `contentDir` and content-targeted Hugo module mounts. `content stats --write` creates `data/site-stats.json` and a draft generated-page stub by default.

### Use the interactive terminal UI

`hs tui [site-directory]` opens an arrow-key interface for content work and release checks. The content browser shows the active result-set statistics, per-article word counts, and filters for draft state, section, category, tag, and date range.

```sh
./bin/hs tui /path/to/hugo-site
```

| Key | Action |
| --- | --- |
| `/` | Search content title, tags, and body text. |
| `f` / `c` | Open filters / clear search and filters. |
| `Enter` | Read the selected Markdown source, including front matter. |
| `d` | Run the content doctor against the selected source file only. |
| `u` | Show the selected post's URL, built from Hugo's `baseURL` and generated path. |
| `r` | Refresh local content. |
| `Esc` | Return to the previous screen. |
| `q` | Quit, except while typing a search or filter value. |

In the filter screen, use left/right to choose known sections, categories, and tags, or Enter to type an exact value. Date filters use `YYYY-MM-DD`.

Use PgUp/PgDown to move by a page through lists and text, Home/End to jump to the first or last item, and Space as PgDown. While typing a search or filter value, Space enters a space character.

### Build for release

`hs build` runs a production Hugo build in a temporary directory and reports duration, generated pages and files, output size, and Hugo warnings. It never writes to the project's `public/` directory.

```sh
hs build .
hs build . --format json
hs build . --build-drafts --build-future
```

### Compare generated URLs

`hs urls` runs a production Hugo build in a temporary directory. It never writes to the project's `public/` directory.

```sh
hs urls . --format json > urls.json
hs urls . --compare urls.json
```

Added or removed paths return exit code 1, which makes permalink changes visible in CI.

### Audit a release

`hs doctor` builds a Hugo project in a temporary directory, then audits content and generated output.

```sh
hs doctor .
hs doctor . --only build,content,links,seo
hs doctor . --strict
hs doctor . --format json
hs doctor . --format sarif
hs doctor . --build-drafts --build-future
# Check only one content source file.
hs doctor . --only content --source content/articles/example.md
```

Warnings do not fail the command unless you pass `--strict`. `--source` requires `--only content`. Errors return exit code 1, invalid arguments return 2, and an unavailable Hugo executable or unreadable project returns 3. Remote auditing is planned but not yet implemented.

### Run focused audits

Use focused reports when you need one category of release feedback. Both commands build into a temporary production directory and support text, JSON, and SARIF output.

```sh
hs audit seo .
hs audit links . --strict
hs audit seo . --format json
```

## Development

The command entrypoint lives in `cmd/hs`; implementation and tests are kept in `internal/app`. This boundary keeps process lifecycle separate from command behavior while the domain packages evolve.

```sh
go test ./...
go vet ./...
```

See [the product roadmap and doctor specification](docs/roadmap.md) for planned work and the intended package structure.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution expectations and [SECURITY.md](SECURITY.md) for private vulnerability reporting.
