# Releasing

A release is a pushed tag:

```bash
git tag v1.1.412.0
git push pyright-go v1.1.412.0
```

`.github/workflows/release.yml` then runs the Go tests, cross-compiles the
five targets (linux/macos × amd64/arm64, windows/amd64, `CGO_ENABLED=0`),
and produces two kinds of artifact per platform:

- **Tarballs** (`pyright-go-<version>-<os>-<arch>.tar.gz`, `.zip` on
  Windows) attached to a GitHub release: the binary, the
  `typeshed-fallback` tree, and the license. The binary finds the typeshed
  sitting next to it via its upward search, so `tar -xzf` + run is the whole
  install — this is the artifact for CI jobs that just want a pinned URL.
- **Wheels** (`pyright_go-<version>-py3-none-<platform>.whl`) published to
  PyPI as [`pyright-go`](https://pypi.org/project/pyright-go/): the binary
  and typeshed inside the `pyright_go` package with a console-script shim
  that sets `PYRIGHT_GO_ROOTDIR` to the packaged typeshed and execs the
  binary (an explicit `--rootdir` still wins). Built by
  `go/packaging/build_wheel.py`, stdlib-only and reproducible. Nothing
  compiles at install time.

Versioning: the tag is the package version, and the scheme is
`<pyright release>.<port revision>` -- the first three segments name the
pyright release whose behavior the port reproduces, the fourth counts the
port's own releases on top of it. `1.1.412.0` is the first cut of the
1.1.412 port, `1.1.412.1` fixes something in it, and `1.1.413.0` would mean
a newer pyright was ported. The release workflow injects the version into
the binary (`-X main.version=...`), so `--version` and the JSON report carry
the release version; development builds report `1.1.412-go`.

## PyPI trusted publishing: one-time setup

Publishing uses OIDC (no API tokens stored anywhere). Before the first
release, someone with a PyPI account must register the publisher:

1. On PyPI, go to **Publishing** → **Add a new pending publisher**
   (or, once the project exists, the project's **Publishing** settings) and
   enter:
   - PyPI project name: `pyright-go`
   - Owner: `reflex-dev`
   - Repository: `pyright-go`
   - Workflow name: `release.yml`
   - Environment name: `pypi`
2. In the GitHub repository settings, create an **environment** named
   `pypi` (Settings → Environments → New environment). Optionally add
   required reviewers to gate publishes.

The first tag pushed after both steps creates the PyPI project and
publishes; later tags just publish.

## Local dry run

Build a wheel for the current machine and install it into a scratch venv:

```bash
cd go
go build -trimpath -ldflags="-s -w" -o /tmp/pyright-go ./cmd/pyright-go
python3 packaging/build_wheel.py \
    --version 0.0.0 \
    --platform manylinux_2_17_x86_64.manylinux2014_x86_64 \
    --binary /tmp/pyright-go \
    --typeshed ../packages/pyright-internal/typeshed-fallback \
    --license ../LICENSE.txt \
    --out /tmp/dist
python3 -m venv /tmp/venv && /tmp/venv/bin/pip install /tmp/dist/*.whl
/tmp/venv/bin/pyright-go --version
```
