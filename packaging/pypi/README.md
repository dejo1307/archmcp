# PyPI packaging

Publishes the compiled enola binary as platform wheels under the PyPI project
name `enola-cli`. The name `enola` was already taken; the installed command is
still `enola`.

## What the wheel contains

```
enola_cli-<ver>.data/scripts/enola          the Go binary, nothing else
enola_cli-<ver>.dist-info/METADATA
enola_cli-<ver>.dist-info/WHEEL
enola_cli-<ver>.dist-info/RECORD
enola_cli-<ver>.dist-info/licenses/LICENSE
enola_cli-<ver>.dist-info/licenses/NOTICE
```

There is no Python code in it. `import enola_cli` fails by design, and tools that
scan wheels for an importable module report `W007: Wheel library is empty`, which
is expected here rather than a problem to fix.

## Why `.data/scripts` and not an entry point

pip installs files from `.data/scripts` straight into the environment's script
directory with the executable bit set, so `enola` on PATH is the Go binary
itself. The obvious alternative,

```toml
[project.scripts]
enola = "enola_cli:main"
```

installs a small Python launcher instead, which means an interpreter start plus
an exec in front of every single invocation. That is a bad trade anywhere and a
worse one here: enola installs a git hook, and `internal/updatecheck` is written
specifically to start and finish in milliseconds. A launcher would put 30 to 60
milliseconds ahead of exactly the paths that package exists to keep fast.

`local_test.sh` asserts the outcome rather than the intent. It checks that the
installed file has no shebang and is byte-identical to the binary that went into
the wheel, because an entry-point wheel also puts a working `enola` on PATH and
the difference is otherwise invisible.

## Why LICENSE and NOTICE are in here

The vendored Swift and Dart tree-sitter grammars are MIT, and MIT requires the
notice to travel with copies of the software. The release tarball already carries
both files for that reason. The wheel is the copy pip users receive, so it
carries them too.

## Why not README.md as the long description

`README.md` uses relative links and a relative gif path, neither of which
resolves on PyPI. `DESCRIPTION.md` is a short standalone version that points at
GitHub for everything else.

## Platform tags

Tags are not guesses. Two things decide them and both are measured:

- **macOS.** With cgo the link goes through clang, which stamps
  `LC_BUILD_VERSION minos` with the host SDK version unless
  `MACOSX_DEPLOYMENT_TARGET` is set. The build sets it to `12.0`, which is Go's
  own oldest supported macOS release, and the tag follows.
- **Linux.** With cgo the binary links against the glibc of whichever image
  built it, so the floor is a property of the build image. `linux_test.sh`
  measures it with `objdump -T` and tags at what the binary actually needs.

A tag that claims less than the binary needs is the failure worth caring about:
pip installs it happily and the binary then refuses to start.

## Scripts

| Script | What it does |
| --- | --- |
| `build_wheel.py` | Builds one wheel. Stdlib only, deterministic output, rejects unknown platform tags and non-PEP-440 versions |
| `local_test.sh` | darwin/arm64: build, wheel, install, run, uninstall, and the pipx-shaped paths |
| `linux_test.sh` | Measures the glibc floor across build images, then checks each wheel installs where it should and is refused where it should not |

`local_test.sh` will not run `enola upgrade` unless the pip guard is compiled in.
Without it that command performs a real self-update over the network and
overwrites the binary under test, which is not a test.
