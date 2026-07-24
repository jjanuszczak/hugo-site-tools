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
hs content search fintech . --section articles --tag "Open Finance"
hs content stats .
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
hs content new "A new post" . --section articles --tag Fintech --draft
hs content stats . --write --draft --output private/site-stats
```

`posts` reads YAML, TOML, and JSON front matter. `content` commands understand the project's configured `contentDir` and content-targeted Hugo module mounts. `content stats --write` creates `data/site-stats.json` and a draft generated-page stub by default.

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
```

Warnings do not fail the command unless you pass `--strict`. Errors return exit code 1, invalid arguments return 2, and an unavailable Hugo executable or unreadable project returns 3. Remote auditing is planned but not yet implemented.

## Development

The command entrypoint lives in `cmd/hs`; implementation and tests are kept in `internal/app`. This boundary keeps process lifecycle separate from command behavior while the domain packages evolve.

```sh
go test ./...
go vet ./...
```

See [the product roadmap and doctor specification](docs/roadmap.md) for planned work and the intended package structure.

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution expectations and [SECURITY.md](SECURITY.md) for private vulnerability reporting.
