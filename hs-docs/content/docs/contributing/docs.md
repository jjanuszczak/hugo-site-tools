---
title: Write documentation
weight: 1
---

The documentation site lives in `hs-docs/`. Add or edit Markdown files beneath `hs-docs/content/docs/`; Hugo and Docsy generate the navigation from their section structure and page weights.

Preview the site locally:

```sh
cd hs-docs
npm install
hugo server
```

Build the production site before opening a pull request:

```sh
cd hs-docs
npm run build
```

Keep command examples runnable and aligned with the CLI. When a user-facing command changes, update the relevant page in this site as part of the same pull request.
