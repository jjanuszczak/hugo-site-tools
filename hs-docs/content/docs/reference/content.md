---
title: Content commands
weight: 1
---

Use content commands against a local Hugo project. They respect its configured `contentDir` and content-targeted Hugo module mounts.

```sh
# List source content, including drafts.
hs content list . --format json

# Search text and narrow the result set.
hs content search "open finance" . --section articles --tag Fintech --draft false

# Create a draft source file.
hs content new "A new post" . --section articles --tag Fintech --draft

# Generate content statistics.
hs content stats .
```

Source front matter may use YAML, TOML, or JSON. `content stats --write` creates `data/site-stats.json` and a draft statistics-page stub unless you choose another output path.
