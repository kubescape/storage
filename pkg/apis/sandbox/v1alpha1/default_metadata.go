package v1alpha1

import "fmt"

func validateDefaultModes(modes ...RuleMode) error {
	for _, mode := range modes {
		if mode != "" && mode != RuleAudit && mode != RuleEnforce {
			return fmt.Errorf("default mode must be audit or enforce when specified")
		}
	}
	return nil
}

func validateDefaultMetadata(spec PolicySpec) error {
	if s := spec.Exec; s != nil {
		if err := validateDefaultModes(s.DefaultMode); err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}
	if s := spec.Privilege; s != nil {
		if err := validateDefaultModes(s.DefaultMode); err != nil {
			return fmt.Errorf("privilege: %w", err)
		}
	}
	if s := spec.Identity; s != nil {
		if err := validateDefaultModes(s.DefaultMode); err != nil {
			return fmt.Errorf("identity: %w", err)
		}
	}
	if s := spec.Network; s != nil && s.DefaultMode != nil {
		if err := validateDefaultModes(s.DefaultMode.Egress, s.DefaultMode.Listen); err != nil {
			return fmt.Errorf("network: %w", err)
		}
	}
	return nil
}

func (s FileSection) validateMetadata() error {
	if s.DefaultByAccess == nil {
		if s.DefaultModeByAccess != nil || s.DefaultOriginByAccess != nil {
			return fmt.Errorf("file per-access metadata requires defaultByAccess")
		}
		return validateDefaultModes(s.DefaultMode)
	}
	if s.DefaultMode != "" || s.DefaultOrigin != nil {
		return fmt.Errorf("file scalar metadata requires scalar default")
	}
	if m := s.DefaultModeByAccess; m != nil {
		return validateDefaultModes(m.Read, m.Write, m.Create, m.Delete, m.Rename, m.Chmod, m.ExecMap)
	}
	return nil
}
