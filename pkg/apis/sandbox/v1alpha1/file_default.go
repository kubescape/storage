package v1alpha1

import "fmt"

func validAction(a Action) bool { return a == ActionAllow || a == ActionDeny }

// ValidateDefaults rejects missing or ambiguous defaults without making JSON
// decoding perform semantic validation (which could poison an entire list).
func (s FileSection) ValidateDefaults() error {
	if s.DefaultByAccess == nil {
		if validAction(s.Default) {
			return nil
		}
		return fmt.Errorf("file requires default allow/deny or complete defaultByAccess")
	}
	if s.Default != "" {
		return fmt.Errorf("file default and defaultByAccess are mutually exclusive")
	}
	p := s.DefaultByAccess
	for _, a := range []Action{p.Read, p.Write, p.Create, p.Delete, p.Rename, p.Chmod, p.ExecMap} {
		if !validAction(a) {
			return fmt.Errorf("every defaultByAccess action must be allow or deny")
		}
	}
	return nil
}
