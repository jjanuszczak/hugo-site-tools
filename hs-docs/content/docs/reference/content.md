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

# Export one local Markdown post as an EPUB with Pandoc 3.0+ installed.
hs content epub content/articles/example.md . --output /tmp/example.epub
```

Source front matter may use YAML, TOML, or JSON. `content stats --write` creates `data/site-stats.json` and a draft statistics-page stub unless you choose another output path.

## Export a post as an EPUB

`hs content epub <content-file> [site-directory]` converts the selected local Markdown post with Pandoc. Front matter supplies the book metadata. The date remains in EPUB package metadata but does not appear on the title page. Copyright or rights metadata appears on the title page and in EPUB metadata.

For a cover, place `cover.png` or `cover.jpg` beside the post's `index.md` in its page bundle. The exporter uses no cover when neither file exists. A portrait image around 1600 × 2560 pixels (width × height), in RGB, suits most e-readers.

Referenced local images are packaged. Hugo shortcode tags are stripped before conversion, while text between paired shortcode tags remains. A built-in stylesheet handles basic reading layout, table borders, and superscript footnote references. Pass `--css PATH` to use a custom stylesheet.

### Replacements

Add ordered literal replacement rules to the project's `.hs.toml`. Rules are case-sensitive, affect EPUB exports only, and can target the body or title. Empty `replace` text suppresses the match. Set `format = "markdown"` for body replacements that should render Markdown such as bold text.

```toml
[[epub.replacements]]
find = "CONFIDENTIAL DRAFT"
replace = ""
scope = "body"

[[epub.replacements]]
find = "important phrase"
replace = "**important phrase**"
scope = "body"
format = "markdown"
```

### Footnotes and references

Use `[^1]` in the body and a matching `[^1]: ...` definition for a linked EPUB note. Each definition must be referenced and separated from adjacent definitions by a blank line. Reading apps may show these notes as pop-ups and omit them from the normal reading flow.

For a bibliography that is visibly listed under a heading, use a normal Markdown heading and ordered list instead:

```markdown
## References

1. IMF. “How Stablecoins Can Improve Payments and Global Finance.” *IMF Blog*, Dec. 4, 2025. [Link](https://www.imf.org/...)
2. McKinsey & Company. “The stable door opens.” *McKinsey Insights*, 2025. [Link](https://www.mckinsey.com/...)
```

Use `## References` without wrapping the heading syntax in bold markers.
