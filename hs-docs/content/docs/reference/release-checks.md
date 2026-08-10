---
title: Release checks
weight: 2
---

Run these checks before publishing a Hugo site:

```sh
hs build . --format json
hs urls . --format json > urls.json
hs urls . --compare urls.json
hs doctor . --strict
```

`hs urls --compare` exits with code 1 when paths changed. That makes permalink changes visible in CI. `hs doctor` can report content, build, link, and SEO findings in text, JSON, or SARIF.

To inspect a live site:

```sh
hs site set https://example.com
hs search "market entry"
hs doctor --remote https://example.com
```
