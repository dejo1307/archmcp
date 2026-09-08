// Package customers holds personal data, which is what makes the laws in
// enola/constraints/gdpr.yaml apply to it.
package customers

// ProfileStore holds names and addresses.
type ProfileStore struct {
	rows map[string]string
}

// Erase removes every trace of one subject.
func (s *ProfileStore) Erase(subject string) {
	delete(s.rows, subject)
}

// ConsentStore holds what each subject agreed to.
type ConsentStore struct {
	rows map[string]bool
}

// Erase removes every trace of one subject.
func (s *ConsentStore) Erase(subject string) {
	delete(s.rows, subject)
}
