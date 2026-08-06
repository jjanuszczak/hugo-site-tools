# hs contributor guide

## What this project is

`hs` is a Go command-line toolkit for inspecting, validating, and operating Hugo sites. It works in two modes:

- Against a local Hugo project for content, builds, URLs, and release checks.
- Against a configured published site's `index.json` for search.

Hugo remains the source of truth for site generation, routing, templates, and archetypes. `hs` should make common content and release workflows quicker, not become a replacement Hugo implementation.

## Repository map

- `cmd/hs/main.go` is intentionally thin. It delegates process lifecycle to `internal/app`.
- `internal/app/app.go` contains command parsing, Hugo/content discovery, reports, doctor checks, and unit tests' main seam (`Run` and `run`).
- `internal/app/tui.go` contains the Bubble Tea terminal UI. It uses the same command and content helpers as the CLI.
- `internal/app/app_test.go` is the primary test suite. It creates self-contained temporary Hugo fixtures and a fake Hugo executable where needed.
- `docs/roadmap.md` defines intended scope and future work.
- `testdata/fixtures/` is for small, anonymous fixtures only. Do not copy production-site content, exports, credentials, or configuration into it.

## Commands and verification

Build the local executable:

```sh
go build -o ./bin/hs ./cmd/hs
```

Run the test suite and static checks:

```sh
go test ./...
go vet ./...
```

In restricted environments, Go may not be allowed to write its default caches. Use temporary caches rather than changing project files:

```sh
GOCACHE=/private/tmp/hs-go-build GOMODCACHE=/private/tmp/hs-go-mod go test ./...
```

The suite includes local `httptest` coverage. If the sandbox prohibits opening a local listener, rerun the verification with the required local-network permission rather than weakening or skipping the test.

Run `gofmt` on every edited Go file and finish with `git diff --check`.

## Command design rules

- Keep `cmd/hs` thin and keep behavior testable through `internal/app.Run`/`run`.
- Preserve existing CLI behavior and output formats. The CLI remains suitable for scripts and CI.
- Return errors through the existing `exitError` pattern when an exit-code distinction matters. The established convention is: command failures return `1`, invalid arguments return `2`, and unavailable dependencies or unreadable projects return `3` where applicable.
- Prefer parsing Hugo source files and configuration directly for local content operations. Do not require a Hugo build just to list or inspect content.
- Respect configured `contentDir` and content-targeted Hugo module mounts. A feature that only assumes `content/` is incomplete.
- Treat front matter as YAML, TOML, or JSON. Keep source paths relative to the project when reporting findings.
- Do not write a project's `public/` directory. Build, URL, and doctor workflows must use temporary destinations.

## Content behavior

- `content list` reports local source content, including drafts.
- `content search` is for text matching. Filters belong on `content list` when no text query is required.
- Generated page paths and front-matter `url` overrides are distinct. The TUI's `u` action deliberately displays the configured Hugo `baseURL` plus the generated content path, not an arbitrary front-matter override.
- Generated statistics pages (`hs_stats: true`) are omitted from aggregate content statistics.

## TUI conventions

The TUI is invoked with `hs tui [project-directory]` and uses Bubble Tea. Keep it as a layer over existing domain helpers, not a parallel implementation of command behavior.

- Never block the TUI event loop for slow work. Run build, doctor, and audit actions through a `tea.Cmd` and keep the progress indicator active until the result message returns.
- Keep keyboard behavior consistent across long lists and text: arrows move one item or line, PgUp/PgDown and Space move a page, Home/End jump to extremes, Esc returns, and `q` quits outside editable input fields.
- Search and filter state must apply to the displayed items and the aggregate statistics above them.
- Any destructive content action needs a clear confirmation step. Read-only actions such as viewing source, URL, and selected-content doctor checks can run directly.
- Test state transitions without launching an interactive terminal. Call the model's update helpers directly in unit tests.

## Working safely in this repository

- Check `git status --short` before editing. This repository may contain another developer's in-progress changes. Preserve unrelated diffs.
- Do not use destructive Git commands such as `git reset --hard` or `git checkout --` to clean a worktree.
- Avoid adding dependencies unless the behavior cannot be delivered cleanly with the standard library or existing modules. If a dependency is added, run `go mod tidy` and keep `go.mod`/`go.sum` in sync.
- Update `README.md` when user-facing commands or key bindings change. Update `docs/roadmap.md` only when project status or planned scope changes.

