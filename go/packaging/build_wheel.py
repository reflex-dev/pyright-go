#!/usr/bin/env python3
"""Build a platform wheel for pyright-go.

The wheel carries the compiled binary and the typeshed-fallback tree inside
the `pyright_go` package, plus a tiny console-script shim that points the
binary at the packaged typeshed (via PYRIGHT_GO_ROOTDIR, which an explicit
--rootdir still overrides) and execs it. Nothing is compiled at install time.

Stdlib only, so CI needs nothing but Python. Wheels are byte-reproducible
for a given set of inputs: fixed timestamps, sorted member order.

Usage:
  build_wheel.py --version 0.1.0 --platform manylinux_2_17_x86_64.manylinux2014_x86_64 \
      --binary path/to/pyright-go --typeshed path/to/typeshed-fallback \
      --license path/to/LICENSE.txt --out dist/
"""

import argparse
import base64
import hashlib
import io
import os
import sys
import zipfile

SHIM = '''\
"""pyright-go: the pyright type checker, transliterated to Go.

This package ships a native binary; this module only locates it, points it
at the packaged typeshed, and hands over.
"""

import os
import sys


def main():
    package_dir = os.path.dirname(os.path.abspath(__file__))
    binary = os.path.join(
        package_dir, "bin", "pyright-go.exe" if os.name == "nt" else "pyright-go"
    )

    # An explicit --rootdir on the command line still wins; the binary
    # prefers the flag over this variable.
    os.environ.setdefault("PYRIGHT_GO_ROOTDIR", package_dir)

    argv = [binary] + sys.argv[1:]
    if os.name == "nt":
        import subprocess

        sys.exit(subprocess.run(argv).returncode)
    os.execv(binary, argv)


if __name__ == "__main__":
    main()
'''

# Fixed timestamp: zip's epoch. Reproducibility beats plausible mtimes.
ZIP_DATE = (1980, 1, 1, 0, 0, 0)


def metadata(version: str, description: str) -> str:
    return (
        "Metadata-Version: 2.1\n"
        "Name: pyright-go\n"
        f"Version: {version}\n"
        "Summary: The pyright static type checker for Python, transliterated to Go\n"
        "Home-page: https://github.com/reflex-dev/pyright-go\n"
        "License: MIT\n"
        "Classifier: Development Status :: 4 - Beta\n"
        "Classifier: Intended Audience :: Developers\n"
        "Classifier: License :: OSI Approved :: MIT License\n"
        "Classifier: Programming Language :: Python :: 3\n"
        "Classifier: Topic :: Software Development :: Quality Assurance\n"
        "Requires-Python: >=3.8\n"
        "Description-Content-Type: text/markdown\n"
        "\n" + description
    )


DESCRIPTION = """\
# pyright-go

A transliteration of [pyright](https://github.com/microsoft/pyright)
**1.1.412** from TypeScript to Go, verified to produce identical diagnostics
— and several times faster.

```bash
pyright-go --threads 8 --cachedir .pyright-cache
```

This package ships a self-contained native binary with typeshed bundled; it
accepts pyright's command line, produces pyright's output, and returns
pyright's exit codes. `--threads` parallelizes checking; `--cachedir` adds a
run-to-run cache that answers unchanged projects in about a second.

Sources, verification methodology and benchmarks:
<https://github.com/reflex-dev/pyright-go>.
"""

WHEEL_TEMPLATE = (
    "Wheel-Version: 1.0\n"
    "Generator: pyright-go-build\n"
    "Root-Is-Purelib: false\n"
    "Tag: py3-none-{platform}\n"
)

ENTRY_POINTS = "[console_scripts]\npyright-go = pyright_go:main\n"


def record_hash(data: bytes) -> str:
    digest = hashlib.sha256(data).digest()
    return "sha256=" + base64.urlsafe_b64encode(digest).rstrip(b"=").decode()


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--version", required=True)
    ap.add_argument("--platform", required=True, help="wheel platform tag")
    ap.add_argument("--binary", required=True)
    ap.add_argument("--typeshed", required=True)
    ap.add_argument("--license", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    # Path inside the archive -> file content. Collected first, written in
    # sorted order for reproducibility.
    members: dict[str, bytes] = {}
    executables: set[str] = set()

    binary_name = "pyright-go.exe" if args.platform.startswith("win") else "pyright-go"
    binary_path = f"pyright_go/bin/{binary_name}"
    with open(args.binary, "rb") as f:
        members[binary_path] = f.read()
    executables.add(binary_path)

    members["pyright_go/__init__.py"] = SHIM.encode()

    for root, dirs, files in os.walk(args.typeshed):
        dirs.sort()
        for name in sorted(files):
            src = os.path.join(root, name)
            rel = os.path.relpath(src, args.typeshed)
            arc = "pyright_go/typeshed-fallback/" + rel.replace(os.sep, "/")
            with open(src, "rb") as f:
                members[arc] = f.read()

    dist_info = f"pyright_go-{args.version}.dist-info"
    members[f"{dist_info}/METADATA"] = metadata(args.version, DESCRIPTION).encode()
    members[f"{dist_info}/WHEEL"] = WHEEL_TEMPLATE.format(platform=args.platform).encode()
    members[f"{dist_info}/entry_points.txt"] = ENTRY_POINTS.encode()
    with open(args.license, "rb") as f:
        members[f"{dist_info}/licenses/LICENSE.txt"] = f.read()

    record_lines = []
    for arc in sorted(members):
        data = members[arc]
        record_lines.append(f"{arc},{record_hash(data)},{len(data)}")
    record_lines.append(f"{dist_info}/RECORD,,")
    record = "\n".join(record_lines) + "\n"

    os.makedirs(args.out, exist_ok=True)
    wheel_name = f"pyright_go-{args.version}-py3-none-{args.platform}.whl"
    wheel_path = os.path.join(args.out, wheel_name)

    record_arc = f"{dist_info}/RECORD"
    with zipfile.ZipFile(wheel_path, "w", zipfile.ZIP_DEFLATED) as zf:
        for arc in sorted(members) + [record_arc]:
            data = record.encode() if arc == record_arc else members[arc]
            info = zipfile.ZipInfo(arc, date_time=ZIP_DATE)
            mode = 0o755 if arc in executables else 0o644
            # S_IFREG | mode, shifted into the zip's external attributes --
            # without the file-type bits some extractors (pip included) do
            # not restore the execute bit.
            info.external_attr = (0o100000 | mode) << 16
            info.compress_type = zipfile.ZIP_DEFLATED
            zf.writestr(info, data)

    size = os.path.getsize(wheel_path)
    print(f"{wheel_path} ({size / 1024 / 1024:.1f} MB, {len(members)} files)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
