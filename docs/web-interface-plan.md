# `hs web` implementation plan

## Decision

Add `hs web` as a local companion to the CLI and TUI. It serves a graphical
workspace on the loopback interface and uses the JavaScript Object GUI (JOG)
runtime for the browser application.

The browser is a presentation layer. `hs` remains responsible for discovering
Hugo projects, reading content, generating paths, building into temporary
directories, and running doctor and audit checks. The web UI must consume typed
application results, never parse CLI output.

The first release is read-only and safe-operation only. It may inspect content,
run builds and audits, compare a configured public site, and inspect, validate,
or generate campaign links. It must not edit content or write project
configuration. A later editing release will extend the existing inspector and
permission model rather than replace the architecture.

## Targets

`hs web [project-directory]` analyzes an explicit local Hugo project.

`hs web --repo <github-url> [--ref <ref>] [--subdir <path>]` analyzes an
explicit GitHub repository by creating a managed temporary checkout. Repository
resolution belongs to core `hs`, so the CLI, TUI, CI, and a future hosted
version can use the same project target abstraction.

Each repository analysis records its URL, requested ref, resolved commit, and
Hugo project subdirectory. The UI displays this identity beside results.

## First-release workspace

- Dashboard: content counts, draft ratio, issue severity, recent run state,
  section distribution, publishing cadence, and SEO readiness.
- Content explorer: filters, sortable content grid, metadata inspector, source
  preview, generated path, configured public URL, and selected-content doctor.
- Release workbench: build, doctor, focused SEO/link audits, structured
  findings, source previews, JSON/SARIF download, and cancelable progress.
- Repository and remote comparison: local or repository identity, project
  configuration summary, published-site checks, and local-versus-remote URL
  comparison.
- Campaign links: inspect policy, validate links, and generate links without
  changing campaign definitions. Delivered in the initial workspace.

## Architecture

```text
JOG browser app
  -> same-origin JSON API and job events
hs web server, bound to 127.0.0.1
  -> project/repository resolver
  -> typed hs services
  -> asynchronous build/audit job runner
  -> bundled JOG and app assets
local Hugo project or temporary Git checkout
```

The server binds only to loopback, uses a per-launch session token, keeps UI
and API same-origin, validates all project-relative source paths, and limits
concurrent jobs. Builds continue to use temporary destinations. Repository
checkouts are removed when the command exits.

## JOG decision

Vendor a pinned JOG distribution with the application rather than loading it
from a CDN. The initial runtime is copied from JOG commit
`118d2be72f2368cff40bacdc1ca39b3f1dd6670a` into
`internal/app/web_static/vendor/jog/`; the release build copies the web asset
tree beside the executable. Use the core runtime for the workspace shell,
grids, panels, dialogs, binding, and responsive layouts. Bundle ChartJOG only
for dashboard charts that answer operational questions. Pin and document each
JOG update because JOG V2 remains pre-release.

## Delivery sequence

1. Add reusable typed services for content, stats, source preview, build,
   doctor, audits, remote checks, and campaign reads while preserving CLI/TUI
   behavior.
2. Add local and GitHub repository project targets, temporary checkout cleanup,
   and tests.
3. Add the loopback `hs web` server, narrow APIs, asynchronous jobs, progress,
   cancellation, and safety boundaries.
4. Build the JOG dashboard, content explorer, and release workbench.
5. Add charts, remote comparison, browser tests, documentation, and release
   verification.

## Future hosting path

Future hosting replaces the local source resolver with a repository
installation/workspace resolver, adds user authorization at the API boundary,
stores job artifacts, and runs Hugo work in isolated workers. The JOG client
contract and the typed `hs` analysis services remain unchanged.
