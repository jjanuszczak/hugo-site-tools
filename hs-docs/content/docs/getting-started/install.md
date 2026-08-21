---
title: Install hs
weight: 1
description: Install the hs command-line toolkit.
---

Install the latest released version with Go:

```sh
go install github.com/jjanuszczak/hugo-site-tools/cmd/hs@latest
```

For local repository development, build the executable instead:

```sh
./scripts/build.sh
./bin/hs --help
```

`hs` expects Hugo to be installed when you run build, URL comparison, or doctor commands. Content listing and source search read your Hugo project directly.
