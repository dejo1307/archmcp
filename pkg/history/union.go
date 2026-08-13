package history

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const OriginLocal = "local"

type UnionRevision struct {
	Entry   Entry
	Origins []string
	Local   bool
	Record  *ShareRecord
}

func LocalUnion(entries []Entry) []UnionRevision {
	out := make([]UnionRevision, 0, len(entries))
	for _, e := range entries {
		out = append(out, UnionRevision{Entry: e, Origins: []string{localOrigin(e)}, Local: true})
	}
	return out
}

func localOrigin(e Entry) string {
	if e.Origin != "" {
		return OriginLocal + " (pulled from " + e.Origin + ")"
	}
	return OriginLocal
}

func BuildUnion(local []Entry, sh *Share, repo string) []UnionRevision {
	byID := map[string]int{}
	var out []UnionRevision
	for _, e := range local {
		if e.ID != "" {
			if _, dup := byID[e.ID]; dup {
				continue
			}
			byID[e.ID] = len(out)
		}
		out = append(out, UnionRevision{Entry: e, Origins: []string{localOrigin(e)}, Local: true})
	}
	if sh != nil {
		for _, rec := range sh.RevisionsFor(repo) {
			rec := rec
			origin := "store:" + rec.Source
			if i, dup := byID[rec.ID]; dup {
				out[i].Origins = append(out[i].Origins, origin)
				if out[i].Record == nil {
					out[i].Record = &rec
				}
				continue
			}
			byID[rec.ID] = len(out)
			out = append(out, UnionRevision{Entry: sh.EntryFor(rec), Origins: []string{origin}, Record: &rec})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return entryBefore(out[i].Entry, out[j].Entry) })
	return out
}

func UnionRepo(local []Entry, sh *Share, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	for i := len(local) - 1; i >= 0; i-- {
		if local[i].Repo != "" {
			return local[i].Repo, nil
		}
	}
	if sh == nil {
		return "", nil
	}
	repos := sh.Repos()
	switch len(repos) {
	case 0:
		return "", fmt.Errorf("the store at %s holds no revisions", sh.Dir)
	case 1:
		return repos[0], nil
	}
	return "", fmt.Errorf(
		"cannot tell which repository is meant: the local history is empty and the store holds %d (%s). Snapshot the repository once so its identity is recorded, or pass --repo",
		len(repos), strings.Join(repos, ", "))
}

func entryBefore(a, b Entry) bool {
	ta, erra := time.Parse(time.RFC3339, a.At)
	tb, errb := time.Parse(time.RFC3339, b.At)
	switch {
	case erra == nil && errb == nil && !ta.Equal(tb):
		return ta.Before(tb)
	case (erra != nil || errb != nil) && a.At != b.At:
		return a.At < b.At
	}
	return a.ID < b.ID
}

func UnionEntries(revs []UnionRevision) []Entry {
	out := make([]Entry, 0, len(revs))
	for _, u := range revs {
		out = append(out, u.Entry)
	}
	return out
}

func (u UnionRevision) Lines(localRoot string, sh *Share) (factLines, insightLines []string, err error) {
	if u.Local && u.Entry.Blob != nil {
		factLines, insightLines, _, err = LoadLines(localRoot, u.Entry.Blob.Segment, u.Entry.Blob.Member)
		if err == nil {
			return factLines, insightLines, nil
		}
		if !errors.Is(err, ErrThinned) {
			return nil, nil, err
		}
	}
	if sh != nil && u.Record != nil {
		p, err := sh.LoadPayload(u.Record.ID)
		if err != nil {
			return nil, nil, err
		}
		return p.FactLines, p.InsightLines, nil
	}
	return nil, nil, ErrThinned
}
