#!/usr/bin/env python3
"""Build a platform wheel that ships the compiled enola binary.

There is no Python code in the wheel. The binary goes into
`enola_cli-<ver>.data/scripts/`, which pip installs straight into the
environment's script directory with the executable bit set, so `enola` on PATH is
the Go binary itself and not a Python launcher in front of it. That matters here
more than it would elsewhere: enola installs a git hook, and internal/updatecheck
is written to start and finish in milliseconds. An entry-point shim would put an
interpreter start plus an exec ahead of every invocation and quietly undo it.

Consequences worth knowing:

  - Nothing is importable. `import enola_cli` fails by design, and tools that
    check wheels for a top-level package will say the wheel has no modules.
  - Root-Is-Purelib is false, so the wheel is platform specific and pip picks
    one per platform from the five we publish.

Only stdlib is used, so this runs on whatever Python a release runner happens to
have, with no bootstrap step and nothing to resolve at release time.

Usage:

    build_wheel.py --binary path/to/enola --version 0.4.12 \
        --platform-tag macosx_12_0_arm64 --outdir dist
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import re
import sys
import zipfile
from pathlib import Path

# PyPI project name. `enola` was taken, so the project is `enola-cli` while the
# installed command stays `enola`. The distribution name inside the wheel is the
# normalised form of the project name, which is where the underscore comes from.
PROJECT_NAME = "enola-cli"
DIST_NAME = "enola_cli"

SUMMARY = "Architectural regression testing for AI-assisted development"
HOMEPAGE = "https://github.com/enola-labs/enola"

# Platform tags are checked for SHAPE, not membership of a fixed list. The exact
# numbers are not a preference to be written down here: both are measured, the
# macOS floor from LC_BUILD_VERSION and the linux floor from the binary's glibc
# symbol versions, and hardcoding them would just be a second place to forget to
# update. What this catches is a malformed or invented tag, which installs
# nowhere and whose first symptom is a user reporting "no matching distribution"
# long after the release went out.
PLATFORM_TAG_RE = re.compile(
    r"^(?:"
    r"macosx_\d{1,2}_\d{1,2}_(?:arm64|x86_64)"
    r"|manylinux_2_\d{1,2}_(?:aarch64|x86_64)"
    # The legacy alias for manylinux_2_17. Not redundant: pip only learned the
    # PEP 600 manylinux_<major>_<minor> spelling in 20.3, and RHEL and Rocky 8
    # ship pip 20.2.4, so a wheel tagged only manylinux_2_17 is invisible to
    # stock pip on exactly the distros the low glibc floor exists to reach.
    r"|manylinux2014_(?:aarch64|x86_64)"
    r"|musllinux_1_\d{1,2}_(?:aarch64|x86_64)"
    r"|win_amd64|win_arm64"
    r")$"
)

# Tags are release tags with the leading v stripped, and they have to survive
# PEP 440 unchanged. `v0.5.0-rc1` would not, and the failure PyPI gives for it
# arrives at the very end of a release, so reject it here where it is cheap.
VERSION_RE = re.compile(r"^(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$")

# The zip epoch. Fixed so that the same binary produces the same wheel bytes;
# this repository has a determinism job and packaging should not be the one
# thing that opts out of it. 1980-01-01 is the earliest a zip can represent.
ZIP_EPOCH = (1980, 1, 1, 0, 0, 0)

# Pinned rather than left to the default, for the same reason as the epoch.
COMPRESS_LEVEL = 6

CLASSIFIERS = [
    "Development Status :: 4 - Beta",
    "Environment :: Console",
    "Intended Audience :: Developers",
    "Programming Language :: Go",
    "Topic :: Software Development :: Quality Assurance",
    "Topic :: Software Development :: Testing",
]


def sha256_b64(data: bytes) -> str:
    """RECORD digest format: urlsafe base64 of the sha256, without padding."""
    digest = hashlib.sha256(data).digest()
    return base64.urlsafe_b64encode(digest).decode("ascii").rstrip("=")


class WheelWriter:
    """Collects entries, tracks RECORD, and writes a deterministic zip."""

    def __init__(self, path: Path) -> None:
        self._zip = zipfile.ZipFile(
            path, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=COMPRESS_LEVEL
        )
        self._record: list[str] = []

    def add(self, arcname: str, data: bytes, *, executable: bool = False) -> None:
        info = zipfile.ZipInfo(arcname, date_time=ZIP_EPOCH)
        # create_system 3 (Unix) is forced even when this runs on Windows, so the
        # mode bits below are honoured and the wheel bytes do not depend on which
        # runner built them.
        info.create_system = 3
        mode = 0o755 if executable else 0o644
        info.external_attr = (0o100000 | mode) << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        self._zip.writestr(info, data)
        self._record.append(f"{arcname},sha256={sha256_b64(data)},{len(data)}")

    def close(self, record_name: str) -> None:
        # RECORD lists itself with no digest and no size, because it cannot
        # contain its own hash.
        lines = self._record + [f"{record_name},,", ""]
        record = "\n".join(lines).encode("utf-8")
        info = zipfile.ZipInfo(record_name, date_time=ZIP_EPOCH)
        info.create_system = 3
        info.external_attr = (0o100000 | 0o644) << 16
        info.compress_type = zipfile.ZIP_DEFLATED
        self._zip.writestr(info, record)
        self._zip.close()


def build_metadata(version: str, description: str) -> bytes:
    """METADATA, in the order the field name suggests but with the body last.

    Metadata-Version 2.4 is what allows License-Expression (an SPDX string)
    instead of the deprecated `License ::` classifier. Declaring both is invalid,
    which is why no License classifier appears in CLASSIFIERS above.
    """
    headers = [
        "Metadata-Version: 2.4",
        f"Name: {PROJECT_NAME}",
        f"Version: {version}",
        f"Summary: {SUMMARY}",
        "Description-Content-Type: text/markdown",
        "License-Expression: Apache-2.0",
        # Paths are as named in the source tree; the files themselves live under
        # <dist-info>/licenses/ in the wheel.
        "License-File: LICENSE",
        "License-File: NOTICE",
        # No Python runs, so this is only a floor for pip's own resolution. Keep
        # it permissive: a narrow value here would exclude users for no reason.
        "Requires-Python: >=3.8",
        f"Project-URL: Homepage, {HOMEPAGE}",
        f"Project-URL: Source, {HOMEPAGE}",
        f"Project-URL: Documentation, {HOMEPAGE}/blob/main/docs/README.md",
        f"Project-URL: Changelog, {HOMEPAGE}/blob/main/CHANGELOG.md",
    ]
    headers += [f"Classifier: {c}" for c in CLASSIFIERS]
    return ("\n".join(headers) + "\n\n" + description).encode("utf-8")


def build_wheel_file(tags: list[str]) -> bytes:
    """WHEEL lists one Tag line per platform, even though the filename joins them
    with dots. Both spellings describe the same file; the filename is a compressed
    tag set and this is its expansion."""
    lines = [
        "Wheel-Version: 1.0",
        "Generator: enola build_wheel.py",
        # False because the payload is a platform binary, not pure Python.
        "Root-Is-Purelib: false",
    ]
    lines += [f"Tag: py3-none-{t}" for t in tags]
    return ("\n".join(lines) + "\n").encode("utf-8")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--platform-tag", required=True)
    parser.add_argument("--outdir", required=True, type=Path)
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=Path(__file__).resolve().parents[2],
        help="where LICENSE, NOTICE and the description file are read from",
    )
    parser.add_argument(
        "--description",
        type=Path,
        default=Path(__file__).resolve().parent / "DESCRIPTION.md",
        help="long description; deliberately not README.md, whose relative links "
        "and images do not resolve on PyPI",
    )
    args = parser.parse_args()

    if not VERSION_RE.match(args.version):
        print(
            f"error: version {args.version!r} is not a bare MAJOR.MINOR.PATCH.\n"
            "PyPI needs PEP 440, so a tag like v0.5.0-rc1 has to be normalised "
            "before it gets here.",
            file=sys.stderr,
        )
        return 2

    # A "compressed tag set": several platform tags in one wheel, joined by dots
    # in the filename and listed one per line in WHEEL. It is how a single file
    # answers to both the modern and the legacy spelling of the same platform.
    tags = args.platform_tag.split(".")
    bad = [t for t in tags if not PLATFORM_TAG_RE.match(t)]
    if bad:
        print(
            f"error: {', '.join(repr(t) for t in bad)} "
            f"{'is not a well-formed platform tag' if len(bad) == 1 else 'are not well-formed platform tags'}.\n"
            "A wheel with a tag nothing matches installs nowhere and fails "
            "silently, so the shape is checked before the wheel is written.",
            file=sys.stderr,
        )
        return 2

    if not args.binary.is_file():
        print(f"error: no binary at {args.binary}", file=sys.stderr)
        return 2

    binary = args.binary.read_bytes()
    license_text = (args.repo_root / "LICENSE").read_bytes()
    notice_text = (args.repo_root / "NOTICE").read_bytes()
    description = args.description.read_text(encoding="utf-8")

    data_dir = f"{DIST_NAME}-{args.version}.data/scripts"
    info_dir = f"{DIST_NAME}-{args.version}.dist-info"
    # pip installs this name verbatim into the script directory, so it is the
    # command users end up typing.
    command = "enola.exe" if tags[0].startswith("win") else "enola"

    args.outdir.mkdir(parents=True, exist_ok=True)
    out = args.outdir / f"{DIST_NAME}-{args.version}-py3-none-{args.platform_tag}.whl"

    w = WheelWriter(out)
    w.add(f"{data_dir}/{command}", binary, executable=True)
    w.add(f"{info_dir}/METADATA", build_metadata(args.version, description))
    w.add(f"{info_dir}/WHEEL", build_wheel_file(tags))
    # The vendored Swift and Dart tree-sitter grammars are MIT, and MIT requires
    # the notice to travel with copies of the software. The release tarball
    # already carries both files for exactly this reason; the wheel is the copy
    # pip users receive, so it carries them too.
    w.add(f"{info_dir}/licenses/LICENSE", license_text)
    w.add(f"{info_dir}/licenses/NOTICE", notice_text)
    w.close(f"{info_dir}/RECORD")

    print(f"{out}  ({out.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
