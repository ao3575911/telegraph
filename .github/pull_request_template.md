<!-- Title: a Conventional Commit, e.g. "fix(checkpoint): reject late entries". It becomes the squash commit. -->

## What

## Why

Closes #

## Checks

- [ ] `go build ./... && go vet ./... && gofmt -l .` clean
- [ ] `go test -race ./...` passes
- [ ] CHANGELOG updated under `[Unreleased]` (user-visible changes)
- [ ] One `semver:` label and one `type:` label set
- [ ] Commits are signed
