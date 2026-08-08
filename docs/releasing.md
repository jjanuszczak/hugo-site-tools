# Releasing hs

The GitHub **Release** workflow publishes a release when a `v*` tag reaches the remote repository. It runs the test suite and `go vet`, then attaches Linux and macOS archives for AMD64 and ARM64 with a `SHA256SUMS` file.

Run these commands from a clean, up-to-date `main` branch. Replace `v1.2.3` with the release version.

```sh
git switch main
git pull --ff-only
go test ./...
go vet ./...
git tag -a v1.2.3 -m "v1.2.3"
git push origin v1.2.3
```

Watch the **Release** workflow in GitHub. After it succeeds, download an archive and verify it against `SHA256SUMS` before distributing it.

If a release workflow fails after the tag is pushed, fix the issue, push the fix, then run **Release** manually in GitHub Actions with the existing tag. Do not move or recreate a published release tag.
