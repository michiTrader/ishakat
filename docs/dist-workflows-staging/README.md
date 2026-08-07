# Staged `.github/workflows/` files — blocked on a token permission

**Why these two files live here instead of `.github/workflows/`:** the GitHub
App token backing this sandbox's git access does not carry the `workflows`
OAuth scope. GitHub rejects any push — via `git push` or the Contents API,
both tested directly — that creates or updates a file under
`.github/workflows/`, with the explicit error:

```
refusing to allow a GitHub App to create or update workflow
`.github/workflows/ci.yml` without `workflows` permission
```

This is a hard block at GitHub's API layer, not a local git configuration
issue, and it applies per-file, not per-branch or per-repo-setting — every
push attempt in this project's history that touched `.github/workflows/*`
has failed with the identical message, across multiple sessions.

## What is staged here

- **`ci.yml`** — mirrors `make check` and `make race`: build, vet, gofmt,
  test, and `-race` on every push/PR. Verified locally (Go 1.26.5):
  `go build ./...`, `go vet ./...`, `gofmt -l cmd internal` (empty),
  `go test ./...` (17/17 packages).
- **`release.yml`** — the centerpiece of Step 13bis: a five-target build
  matrix (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`,
  `android/arm64`), fires on a `v*` tag. The `android/arm64` leg:
  1. Builds with `nttld/setup-ndk` + `CGO_ENABLED=1`, matching the
     `android:` target already in this repo's `Makefile`.
  2. Confirms with `readelf -d | grep NEEDED.*libc.so` that the artifact is
     genuinely CGO-linked against Bionic, not a plain static Go binary that
     happens to have `GOOS=android` in its build tags — the exact
     distinction §3 says matters, because a `CGO_ENABLED=0` android build
     compiles cleanly, prints `--version`, and only fails on the first real
     network call.
  3. Separately builds an `android/amd64` **test-only** binary (not
     published) and runs `ishakat doctor` inside a real, booted
     `reactivecircus/android-emulator-runner` emulator, asserting doctor's
     output contains both `probando DNS...OK` and `probando HTTPS...OK`.
     `amd64` rather than `arm64` because GitHub-hosted runners only have
     hardware-accelerated virtualization for x86_64 Android images; an
     `arm64-v8a` AVD on an x86_64 host runs under software translation and
     is a well-documented way to get an emulator that never finishes
     booting in CI. The code path under test — `netfix`'s CGO/Bionic
     resolver choice and `doctor`'s DNS+HTTPS probes — depends on
     `GOOS=android` and `CGO_ENABLED=1`, not on CPU architecture, so
     `amd64` proves the same thing `arm64` would; the `arm64` release
     artifact's CGO linkage is confirmed separately by the `readelf` check.
  4. Only publishes the release (`softprops/action-gh-release`) if every
     build job **and** the emulator verification succeeded. This is what
     makes the closing criterion literal instead of aspirational: *"the
     release job must verify a real remote DNS resolution on the android
     artifact, not just that it compiled"* (§13bis) — extended here to a
     full HTTPS round trip per the same section's later closing-criterion
     wording, which `cmd/ishakat/main.go`'s `httpsProbe` (PR #65) added.

## What needs to happen to unblock this

Someone with a credential that has the `workflows` scope needs to do one of:

1. **Grant the `workflows` scope** to the GitHub App/token this sandbox
   uses, so future sessions can push directly, or
2. **Move these two files by hand**: from the repo root,
   ```
   mkdir -p .github/workflows
   git mv docs/dist-workflows-staging/ci.yml .github/workflows/ci.yml
   git mv docs/dist-workflows-staging/release.yml .github/workflows/release.yml
   git commit -m "ci: activate staged GitHub Actions workflows"
   git push
   ```
   Both files are already fully written, commented, and locally verified
   (`ci.yml`'s equivalent commands ran green — see the PR that introduced
   this directory for the exact `go test` output). No further editing
   should be needed; this is a pure `git mv` plus commit.

## Do not build anything else against this being permanent

This directory is a holding pen, not a design decision — the two files
belong in `.github/workflows/` and only live under `docs/` because that is
the one location this token is allowed to write to. Nothing in the rest of
the codebase should ever reference `docs/dist-workflows-staging/` as if it
were a real CI location.
