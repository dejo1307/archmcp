package command

import "github.com/enola-labs/enola/pkg/cli"

// testRunner is the Runner this package's own tests exercise: the OSS binary, whose
// name is what the moved tests' expectations were written against.
// "upgrade" mirrors cmd/enola, which dispatches it itself — the moved tests assert
// it is still offered as a typo suggestion.
func testRunner() *Runner { return New(cli.Binary{Name: "enola"}, "upgrade") }
