# The architecture history

*How enola remembers what your architecture used to look like, and why it is allowed to.*

`enola log` answers a question a snapshot structurally cannot: **when did this happen?**

> When did `internal/server` start importing `internal/extractors`?
> Which change introduced this dependency cycle?
> What did this codebase look like in March?

Every other surface in enola describes the tree as it is now — `diff_snapshot` included, which
compares two nows. This one describes the past.

**Experimental.** The commands and the on-disk format may change.

---

## Reading a history

```
$ enola log
e8847af  2026-08-03 17:54  (d2293a18, main)   +1 facts · ~1 changed · +6 edges
0bd6c73  2026-08-03 17:55  (d2293a18, main)   +3 facts · +12 edges
c1caa0a  2026-08-03 17:57  (8db49777, main)   no architectural change
3cdf107  2026-08-03 18:07  (8db49777, main)   1 new finding · +36 facts · +127/-1 edges
```

Oldest first — the opposite of `git log`. A changelog answers *what landed recently*; this answers
*how did it get like this*, and that question runs forward. With `--graph` the lines therefore
diverge downward at a branch and converge downward at a merge.

| Command | Question |
|---|---|
| `enola log` | what has this architecture done over time? |
| `enola log --graph` | …and along which branches? |
| `enola log --stat` | …broken down by what kind of thing changed |
| `enola show <rev>` | what did THIS revision do? |
| `enola diff <a>..<b>` | what happened between these two points? |
| `enola blame <pattern>` | when did this appear, and when did it go? |
| `enola gc` | what is stored, and what can go? |

A revision is a snapshot id or its prefix, a git commit, `HEAD~3`, `@7`, a ref name, or `latest`.

Agents reach the same two questions through `architecture_history` and `architecture_blame`.

### blame

```
$ enola blame "internal/engine -> internal/history"
e8847af  2026-08-03 17:54  (d2293a18, main)
    + dependency  internal/engine -> …/internal/history  (internal/engine/history.go:11)
```

The pattern matches a module or symbol name, a file path, or both endpoints of an edge. `--findings`
searches recorded findings instead ("which snapshot introduced this cycle?"); `--first` stops at the
introduction.

Revisions whose stored contents have aged out are reported as **unsearched**, never as absent —
"not found" and "not found in what I could read" are different answers, and the second means look
further back.

---

## Starting one

Recording is **on by default**: every `generate_snapshot` appends a revision.

That default is deliberate and is the opposite of how a feature like this usually ships. A history
answers questions about the PAST, so opt-in guarantees that the first time anybody wants one there is
nothing to read. What makes it affordable is that a revision is a ~600-byte line, working-tree
snapshots are capped per commit, and the whole thing lives outside your repository.

To read a past that predates enola, build it:

```
$ enola log --backfill --sample=daily
165 commit(s) selected, 0 already recorded, 165 to snapshot.
Backfilled 165 revision(s) in 3m12s.
```

`--sample=all|merges|tags|daily`, `--since`, `-n` and `--dry-run` choose what to walk. It **reads**
your repository and writes nothing into it: each commit's tree is extracted to a temporary directory,
so a read-only or borrowed checkout is a valid target. Re-running resumes rather than repeating.

Two things to know about a backfilled revision. It is stamped with the **commit's** date, not the
moment you ran the command. And it is snapshotted with **today's** enola and today's config — not the
config that happened to be committed alongside that commit, which would make every historical config
edit look like an architectural event.

---

## Where it lives, and what it costs

`~/.enola/graphs/<workspace>/history/` — outside your repository, so `enola log` is safe to run
against a checkout you do not own, and clearing `.enola` (everything in there is derivable) does not
take the history with it. Set `history.dir` to move it, for instance to commit it or publish it from
CI.

```yaml
history:
  enabled: true      # record each snapshot as a revision
  blobs: true        # store each revision's graph, so it can be replayed
  blob_keep: 200     # roughly how many recent revisions keep their contents
  working_keep: 20   # working-tree revisions kept per commit
  dir: ""            # override the location
```

Measured:

| | header | with contents |
|---|---:|---:|
| ~4k-fact repository | ~600 B / revision | ~4 KB / revision |
| ~23k-fact repository | ~600 B / revision | ~34 KB / revision |

165 revisions of a substantial frontend cost 5.6 MB. Storage is linear in graph size, not in history
length: a revision is stored as a patch against the previous one, with a full copy every so often.

`enola gc` reports what is there and removes what is not needed. With no flags it removes only
garbage; `--thin-older-than=90d` drops old contents while keeping the timeline, and
`--prune-working` discards uncommitted-tree snapshots.

---

## The rule this feature lives under

[SNAPSHOTS.md](SNAPSHOTS.md) rests on a claim about **authority**: everything enola writes is
derivable from your tree, and nothing it keeps can tell you something your source does not say. A
history is the first thing enola keeps that the working tree has forgotten, so it is bounded by three
properties, each enforced by a test rather than by intention:

- **It is replayable.** Every revision is reproducible by re-running enola at that commit; that is
  what `--backfill` does. Nothing here is a measurement that could not be taken again.
- **Nothing that judges the present reads it.** `check`, `diff_snapshot`, freshness and drift consult
  only the current snapshot and the pinned baseline. Deleting the history changes no verdict, and no
  `snapshot_id`.
- **Deleting it loses convenience, never truth.**

The third point is why `previous/` and `baseline/` are still full copies under `.enola/` rather than
references into the history. Making them references would save a few megabytes and would mean that
removing the history could change what `enola check` says — which is exactly the dependency the rule
forbids.

### When enola itself changes

A delta is only a description of somebody's change when both sides were produced the same way. Bump
an extractor, enable a language, edit an ignore glob, and hundreds of facts move because the PARSER
changed. Each revision records an **epoch** — a fingerprint of enola's version, its extraction
behaviour, the effective config and the plugin sets — and the log marks the seam:

```
* 9d930a7  2026-08-03 18:37  (25f265c8, main)  no architectural change
| ══ epoch changed (afb99d → 846a1f) — the delta below is rebuild noise, not a change to the code
* 5a6d794  2026-08-03 18:43  (25f265c8, main)  ~1 changed · +1 edges
```

Revisions marked `incomparable` sit across such a seam. Elapsed time alone never earns that mark:
revisions months apart are the normal shape of a timeline, not a defect.

---

## What it cannot tell you

- **Only what was observed.** enola sees the commits somebody snapshotted, which is a sparse subset
  of your history. `--graph` closes the gaps by asking git which observed revision is the nearest
  ancestor of which — the same parent rewriting `git log --graph -- <path>` does — so the picture is
  honest about being sparse rather than pretending to be complete.
- **A summary is measured against the PREVIOUS SNAPSHOT, not the graph parent.** Usually the same
  revision. Switch branches between snapshots and they differ: the row reports what changed since
  enola last looked, which can include another branch's work arriving or leaving. `--graph` says so
  when it happens, and `enola diff <a>..<b>` answers the ancestry question directly.
- **A multi-repo graph has no single commit**, it has one per repository. The shape follows the
  primary repository and each row carries the others' positions; no DAG is invented over a vector.
- **A backfilled revision only sees what git tracks.** Generated or untracked files present in a
  working tree are absent from an extracted one.
