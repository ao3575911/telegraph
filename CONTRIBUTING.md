# Contributing

1. Open an issue first for anything beyond a small fix.
2. Keep the module stdlib-only: `go.mod` has no `require`.
3. Before opening a PR, run:

   ```sh
   gofmt -l .          # must print nothing
   go vet ./...
   go test -race ./...
   bash docs/demo/simulate.sh
   ```

4. Add a test for every behaviour change and a CHANGELOG entry under
   `[Unreleased]` for anything user-visible.

## Commits and pull requests

- `main` changes only through pull requests, squash-merged once the `test`
  check passes. Commits must be signed (GitHub shows them as Verified).
- PR titles follow [Conventional Commits](https://www.conventionalcommits.org/):
  `feat(area): …`, `fix(area): …`, `docs: …`, `ci: …`, `chore: …`, `test: …`.
  The title becomes the squash commit message.
- Label each PR and issue with one `type:`, one `semver:` and, where it
  applies, `priority:` and `area:` labels (see `.github/labels.yml`).

## Releases

Versions follow [Semantic Versioning](https://semver.org/). To release,
move the `[Unreleased]` notes to a new version heading in `CHANGELOG.md`,
merge, then push a signed annotated tag:

```sh
git tag -s vX.Y.Z -m "telegraph vX.Y.Z" && git push origin vX.Y.Z
```

The `release` workflow builds binaries for Linux, macOS and Windows and
publishes a GitHub Release with that CHANGELOG section as its notes.
