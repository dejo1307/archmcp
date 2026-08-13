package providers

// Hostile-output coverage for the provider JSONL validator, beyond the
// per-rule rejections providers_test already pins: inputs that stress the
// scanner and decoder themselves. Every case must end in a named error or a
// clean parse — never a panic, and never a fact accepted beside an error.

import (
	"strings"
	"testing"
)

func TestParseFacts_HostileOutputs(t *testing.T) {
	valid := `{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"}}`
	cases := map[string]struct {
		input     string
		wantErr   string // empty means the input must parse cleanly
		wantFacts int
	}{
		"line beyond the scanner cap is a named error": {
			input:   `{"kind":"symbol","name":"` + strings.Repeat("a", 5*1024*1024) + `","props":{"resolution_level":"name-only"}}`,
			wantErr: "token too long",
		},
		"line under the cap parses": {
			input:     `{"kind":"symbol","name":"` + strings.Repeat("a", 1024*1024) + `","props":{"resolution_level":"name-only"}}`,
			wantFacts: 1,
		},
		"empty output yields nothing": {
			input: "",
		},
		"blank lines are skipped, facts kept": {
			input:     "\n\n" + valid + "\n\n",
			wantFacts: 1,
		},
		"invalid utf-8 is a decode error naming the line": {
			input:   valid + "\n" + "{\"kind\":\"symbol\",\"name\":\"\xff\xfe\"}",
			wantErr: "line 2",
		},
		"null props means no resolution level": {
			input:   `{"kind":"symbol","name":"x","props":null}`,
			wantErr: "resolution_level",
		},
		"null relation entry has no kind in the vocabulary": {
			input:   `{"kind":"symbol","name":"x","props":{"resolution_level":"name-only"},"relations":[null]}`,
			wantErr: "relation kind",
		},
		"trailing garbage on a valid line is rejected": {
			input:   valid + ` {"second":"object"}`,
			wantErr: "line 1",
		},
		"deeply nested props are decoded, not overflowed": {
			input:     `{"kind":"symbol","name":"x","props":{"resolution_level":"name-only","nest":` + strings.Repeat(`{"a":`, 100) + `1` + strings.Repeat(`}`, 100) + `}}`,
			wantFacts: 1,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			facts, err := parseFacts([]byte(tc.input))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want an error naming %q, got %d fact(s)", tc.wantErr, len(facts))
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error must name the problem %q, got: %v", tc.wantErr, err)
				}
				if facts != nil {
					t.Fatalf("an error must yield no facts, got %d", len(facts))
				}
				return
			}
			if err != nil {
				t.Fatalf("want a clean parse, got: %v", err)
			}
			if len(facts) != tc.wantFacts {
				t.Fatalf("facts = %d, want %d", len(facts), tc.wantFacts)
			}
		})
	}
}
