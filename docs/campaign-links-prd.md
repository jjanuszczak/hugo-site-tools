# Campaign link generator PRD

## Decision

Add a GA4-first campaign-link generator to `hs`. It will generate and validate
strictly governed UTM links for the selected content in a local Hugo project.
The command line supports repeatable and automated workflows. The TUI lets an
editor create a link from the currently selected content item and manage the
approved campaign registry.

UTM parameters are the v1 transport format. They are widely supported by web
analytics tools, map directly to GA4 campaign dimensions, and do not require
credentials or an analytics API connection. GA4's Default Channel Group remains
the reporting target for the built-in policy.

## Problem

When an editor shares Hugo content in a newsletter, an X post, or LinkedIn,
they need a reliable way to attribute visits and key events to the campaign,
channel, source, and creative. Hand-built links create inconsistent casing,
incompatible media labels, missing fields, broken URL encoding, and campaign
names that fragment GA4 reporting.

## Goals

- Generate correctly encoded UTM URLs for local Hugo content.
- Map approved source and medium pairs into GA4 Default Channel Groups.
- Make the TUI content browser the fastest route to a link for a selected page.
- Store strict, reviewable campaign policy alongside the Hugo project.
- Prevent accidental PII, arbitrary destinations, and unapproved taxonomy.
- Keep the underlying generator independent of GA4 APIs so later analytics
  integrations can reuse it.

## Non-goals for v1

- GA4 property access, reporting, campaign-data imports, or conversions.
- URL shortening, QR code generation, redirect hosting, or click tracking.
- Manual tagging of Google Ads destinations. Google Ads auto-tagging is the
  supported path and the tool must warn against a Google Ads manual-UTM flow.
- Remote TUI support. A remote index cannot prove a source item belongs to the
  local project or safely modify its campaign policy.
- Editing a destination page's front matter or content.

## Users and jobs

| User | Job |
| --- | --- |
| Site editor | Create a governed link for the Hugo page currently selected in the TUI. |
| Distribution owner | Define an approved campaign once, then create links for email and social placements without inventing labels. |
| Analyst | Receive consistent GA4 source, medium, campaign, and creative values. |
| Maintainer | Review the campaign taxonomy in version control and reject invalid links in CI. |

## GA4 policy

The generator always writes `utm_source`, `utm_medium`, and `utm_campaign`.
It may write `utm_content` and `utm_id` when supplied. It does not emit empty
parameters. Values are lowercase ASCII kebab-case except a configured campaign
ID, which may use underscores when an external reporting system requires them.

Initial built-in source and medium pairs:

| Source | Organic medium | Paid medium | Expected GA4 channel |
| --- | --- | --- | --- |
| `newsletter` | `email` | n/a | Email |
| `x` | `social` | `paid_social` | Organic Social or Paid Social |
| `linkedin` | `social` | `paid_social` | Organic Social or Paid Social |
| `messenger` | `social` | `paid_social` | Organic Social or Paid Social |

`cpc` is available for paid search where the source is an approved search
platform. It is not offered for an organic social link. `paid_social` is the
preferred explicit medium for paid social. GA4 determines paid social using a
paid medium and a recognized social source. GA4's source lists and channel
rules can change, so `hs` will record the policy version it implements and
allow a project to pin approved values.

The generator must reject:

- a source/medium pair absent from the project policy;
- campaign, source, medium, or content values containing uppercase letters,
  whitespace, non-slug punctuation, or known PII patterns;
- `utm_source=google` with any manually generated paid medium;
- duplicate UTM keys in the destination URL;
- a destination outside the configured Hugo `baseURL` origin and path prefix;
- a content item whose generated URL cannot be determined.

The validator should provide a precise remediation rather than silently
normalizing a submitted link. The interactive creation flow may normalize a
new form entry before confirmation and show the resulting value.

## Campaign identity and lifecycle

A campaign key is a stable reporting identifier. It uses lowercase kebab-case,
has no required date or quarter, and supports perpetual initiatives such as
`sea-fintech-thought-leadership`.

Campaigns have a display label, optional `utm_id`, description, and status:

- `active` campaigns may be used to create links.
- `retired` campaigns remain valid when validating historic links but cannot be
  selected for a new link.
- An active campaign key cannot be renamed. Create a new campaign instead,
  then retire the old one when appropriate.

This protects time-series reporting while allowing an editor to use normal,
human-readable names.

## Configuration

Configuration lives in the local project's `.hs.toml`; no global campaign
dictionary is used. A repository therefore carries its own reporting contract.

```toml
[campaign]
strict = true
policy_version = 1
allowed_mediums = ["email", "social", "paid_social", "cpc"]

[[campaign.sources]]
key = "newsletter"
allowed_mediums = ["email"]

[[campaign.sources]]
key = "x"
allowed_mediums = ["social", "paid_social"]

[[campaign.sources]]
key = "linkedin"
allowed_mediums = ["social", "paid_social"]

[[campaigns]]
key = "sea-fintech-thought-leadership"
label = "SEA fintech thought leadership"
status = "active"
description = "Ongoing distribution of long-form advisory content."
```

The command may create a missing `.hs.toml` only through an explicit `campaign
init` action. It must preserve unrelated `.hs.toml` configuration when adding
or retiring a campaign. If a configuration cannot be parsed safely, it returns
an actionable error and makes no change.

## Command interface

```sh
# Create the initial strict dictionary in a local project.
hs campaign init [project-dir]

# View the project-owned approved values.
hs campaign list [project-dir] [--format text|json]

# Define a campaign before using it.
hs campaign add <key> [project-dir] \
  --label <label> --description <description> [--id <campaign-id>]
hs campaign edit <key> [project-dir] \
  --label <label> --description <description>
hs campaign retire <key> [project-dir]

# Generate a link for a local source item.
hs campaign link <content-file> [project-dir] \
  --campaign <key> --source <key> --medium <medium> \
  [--content <creative-or-placement>] [--format text|json]

# Validate a generated or externally supplied link against project policy.
hs campaign validate <url> [project-dir] [--format text|json]
```

`link` resolves the content file using the same Hugo configuration, content
directories, module mounts, and generated-path rules used by existing content
and URL features. It combines the configured `baseURL` with the page's
generated URL. The base URL is required. Query parameters already present on a
valid page URL are retained, fragments are retained after the UTM query, and
existing UTM parameters are rejected instead of overwritten.

Text output contains only the resulting link, making it safe for shell use.
JSON includes the URL, source path, generated path, UTM fields, expected GA4
channel, and policy version.

Argument errors and policy validation failures return exit code `2`. Unreadable
projects, Hugo configuration, or content sources return `3`. A valid command
returns `0`; validation does not reserve a non-zero result for warnings in v1.

## TUI behaviour

The local content screen gains an `l` action, labelled `Create campaign link`.
It is unavailable in remote mode.

1. The editor selects a content item and presses `l`.
2. `hs` resolves and displays the selected page's base URL plus generated path.
   It stops with an explanation if the project has no usable `baseURL`.
3. The editor selects an active campaign, approved source, and permitted
   medium from lists. The medium list filters as soon as the source is chosen.
4. The editor may enter optional `utm_content`, described as a creative or
   placement identifier, for example `ceo-post` or `email-hero`.
5. A review screen shows the full URL, all UTM fields, and expected GA4 channel.
   Enter confirms; Esc returns without changing configuration or copying a URL.
6. The result screen displays the link in a selectable, copyable terminal view.

The main TUI menu gains `Campaigns`. This opens a registry screen listing active
and retired campaigns and allows: add campaign, edit the label and description
of an existing campaign, and retire a campaign with a clear confirmation. The
campaign key is immutable. Adding or editing a campaign uses the same strict
validation as the CLI. Retiring never deletes configuration or historic link
validity.

TUI work must use state transitions and existing domain helpers. It must not
block the Bubble Tea event loop or implement a second URL-generation path.

## Domain design

Keep the command layer thin. Introduce an application-level campaign service
with pure, unit-testable functions for:

- loading and validating policy;
- resolving a source content item to its canonical generated page URL;
- validating keys and detecting PII-like inputs;
- composing a URL with correctly escaped UTM values;
- calculating the expected GA4 Default Channel Group from the pinned policy;
- formatting text and JSON responses.

The service owns no analytics credentials, network calls, redirect behaviour,
clipboard writes, or persistent database. The CLI and TUI call the same
service. Config mutations go through a narrow repository layer that can be
tested against temporary Hugo fixtures.

## Acceptance criteria

1. A configured project can generate a link for a selected local content item
   using every approved source/medium pair, preserving existing non-UTM query
   values and URL fragments.
2. A link always includes non-empty source, medium, and campaign values; all
   values adhere to the project slug policy and are URL encoded exactly once.
3. Invalid pairings, unknown or retired campaigns, PII-like values, manual
   Google Ads links, off-site destinations, duplicate UTM keys, and missing
   base URLs fail with useful errors and make no configuration change.
4. TUI `l` creates links from the current local item, filters media by source,
   previews the exact result, and does not appear in remote mode.
5. TUI Campaigns can add and retire campaigns with confirmation. It cannot
   rename an active campaign key or delete historic records.
6. `campaign validate` accepts historic links for retired campaigns but rejects
   malformed or policy-incompatible values.
7. Text and JSON output are stable and tested; all argument and dependency
   failures follow the established exit-code convention.
8. Tests cover YAML, TOML, and JSON Hugo configuration, custom `contentDir`,
   content-targeted module mounts, query strings, fragments, encoding, and TUI
   state transitions without an interactive terminal.
9. README documents the new commands and TUI key binding when the feature is
   implemented.

## Risks and follow-up decisions

GA4 maintains its Default Channel Group and can revise source lists and rules.
The product must label its channel indication as an expected classification,
not a guarantee. A future version may import a current GA4 source catalogue,
connect a GA4 property for validation, add branded short-link providers, or
generate QR codes. None of those capabilities belong in this release.
