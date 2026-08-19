# Agent development workflow

Use this workflow when changing code, tests, CI, release behavior, or user documentation.

## Orient

1. Read `CONTEXT.md` before naming or changing domain concepts.
2. Read the relevant issue under `.scratch/` when a ticket number or feature is in scope.
3. Read `docs/linux.md` before changing user-visible CLI behavior.

Orientation is complete when the relevant domain terms, issue acceptance criteria, and user workflow are identified.

## Implement and verify

The pinned release toolchain is Go 1.26.5. Format changed Go files with `gofmt` and run:

```sh
git diff --check
go mod verify
go vet ./...
CGO_ENABLED=0 go test ./... -count=1
```

Use a focused `go test` command while iterating, then run the full commands before handoff. A change is verified only when tests covering the changed seam and the full suite pass.

For release behavior, additionally run:

```sh
scripts/build-release.sh v0.0.0-agent-check dist
(cd dist && sha256sum --check checksums.txt)
```

## GitHub operations

Use authenticated `gh` for GitHub API, Actions, and Release operations. This workspace has HTTPS Git authentication; push with credentials supplied by `gh` rather than SSH:

```sh
git -c credential.helper= \
  -c credential.helper='!gh auth git-credential' \
  push https://github.com/txchen/git-remote-cloak.git BRANCH:BRANCH
```

After pushing, monitor the exact workflow run to completion. A push is complete only when the relevant Linux verification or release job succeeds.

## Release invariants

- Binary release versions and Ciphertext Repository format versions are independent.
- Release tags are annotated `vMAJOR.MINOR.PATCH` tags.
- `checksums.txt` entries are relative to the artifact directory.
- Published binaries report the tag, source commit, Go version, platform, CGo status, and format capabilities.
- A published tag or Release is immutable. Diagnose and fix workflow failures before publication; do not replace a successfully published release asset.
