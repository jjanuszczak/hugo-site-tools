---
title: Quick start
weight: 2
description: Inspect a local Hugo site in a few commands.
---

From the root of a Hugo project:

```sh
hs content list .
hs content search fintech . --section articles
hs content stats .
hs build .
hs doctor . --strict
```

`content list` includes drafts. Add `--draft false` when you only want publishable content. `build` and `doctor` use a temporary output directory, so they never change the project's `public/` directory.

For an interactive view, run:

```sh
hs tui .
```
