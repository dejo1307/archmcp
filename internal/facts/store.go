package facts

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// Store provides in-memory storage and querying of facts with JSONL persistence.
type Store struct {
	mu    sync.RWMutex
	facts []Fact

	// Indexes for fast lookups
	byKind map[string][]int // kind -> indices into facts
	byFile map[string][]int // file -> indices into facts
	byName map[string][]int // name -> indices into facts
}

// NewStore creates an empty fact store.
func NewStore() *Store {
	return &Store{
		byKind: make(map[string][]int),
		byFile: make(map[string][]int),
		byName: make(map[string][]int),
	}
}

// Add adds facts to the store.
func (s *Store) Add(ff ...Fact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range ff {
		idx := len(s.facts)
		s.facts = append(s.facts, f)
		s.byKind[f.Kind] = append(s.byKind[f.Kind], idx)
		if f.File != "" {
			s.byFile[f.File] = append(s.byFile[f.File], idx)
		}
		if f.Name != "" {
			s.byName[f.Name] = append(s.byName[f.Name], idx)
		}
	}
}

// All returns all facts in the store.
func (s *Store) All() []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Fact, len(s.facts))
	copy(result, s.facts)
	return result
}

// Count returns the number of facts in the store.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.facts)
}

// ByKind returns all facts of the given kind.
func (s *Store) ByKind(kind string) []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.collectByIndex(s.byKind[kind])
}

// ByFile returns all facts for the given file.
func (s *Store) ByFile(file string) []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.collectByIndex(s.byFile[file])
}

// ByName returns all facts with the given name.
func (s *Store) ByName(name string) []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.collectByIndex(s.byName[name])
}

// ByRelation returns all facts that have a relation of the given kind.
func (s *Store) ByRelation(relKind string) []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Fact
	for _, f := range s.facts {
		for _, r := range f.Relations {
			if r.Kind == relKind {
				result = append(result, f)
				break
			}
		}
	}
	return result
}

// Query returns facts matching all provided filter criteria.
// Empty filter values are ignored (match all).
func (s *Store) Query(kind, file, name, relKind string) []Fact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []Fact
	for _, f := range s.facts {
		if kind != "" && f.Kind != kind {
			continue
		}
		if file != "" && f.File != file {
			continue
		}
		if name != "" && !strings.Contains(f.Name, name) {
			continue
		}
		if relKind != "" {
			hasRel := false
			for _, r := range f.Relations {
				if r.Kind == relKind {
					hasRel = true
					break
				}
			}
			if !hasRel {
				continue
			}
		}
		result = append(result, f)
	}
	return result
}

// Modules returns all module facts.
func (s *Store) Modules() []Fact {
	return s.ByKind(KindModule)
}

// Symbols returns all symbol facts.
func (s *Store) Symbols() []Fact {
	return s.ByKind(KindSymbol)
}

// Clear removes all facts from the store.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts = nil
	s.byKind = make(map[string][]int)
	s.byFile = make(map[string][]int)
	s.byName = make(map[string][]int)
}

// WriteJSONL writes all facts as JSONL to the given writer.
func (s *Store) WriteJSONL(w io.Writer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	enc := json.NewEncoder(w)
	for _, f := range s.facts {
		if err := enc.Encode(f); err != nil {
			return fmt.Errorf("encoding fact %q: %w", f.Name, err)
		}
	}
	return nil
}

// WriteJSONLFile writes all facts as JSONL to the given file path.
func (s *Store) WriteJSONLFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	bw := bufio.NewWriter(f)
	if err := s.WriteJSONL(bw); err != nil {
		return err
	}
	return bw.Flush()
}

// ReadJSONL reads facts from a JSONL reader and adds them to the store.
func (s *Store) ReadJSONL(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	// Allow large lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var f Fact
		if err := json.Unmarshal(line, &f); err != nil {
			return fmt.Errorf("decoding fact: %w", err)
		}
		s.Add(f)
	}
	return scanner.Err()
}

// ReadJSONLFile reads facts from a JSONL file and adds them to the store.
func (s *Store) ReadJSONLFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	return s.ReadJSONL(f)
}

func (s *Store) collectByIndex(indices []int) []Fact {
	result := make([]Fact, 0, len(indices))
	for _, idx := range indices {
		if idx < len(s.facts) {
			result = append(result, s.facts[idx])
		}
	}
	return result
}
