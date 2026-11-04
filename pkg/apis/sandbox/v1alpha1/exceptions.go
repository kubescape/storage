package v1alpha1

import (
	"fmt"
	"net/netip"
)

func validateExceptions[T interface{ Validate() error }](rule Rule, exceptions []T) error {
	if exceptions != nil && rule.Action != ActionDeny {
		return fmt.Errorf("rule %q: except is only valid on deny rules", rule.Name)
	}
	for i, exception := range exceptions {
		if err := exception.Validate(); err != nil {
			return fmt.Errorf("rule %q exception %d: %w", rule.Name, i, err)
		}
	}
	return nil
}

func actorHasPredicate(a *Actor) bool {
	return a != nil && (len(a.Path) > 0 || a.FromImage != nil || a.Ancestor != nil || len(a.Role) > 0 || len(a.UID) > 0 || len(a.Container) > 0)
}
func pathHasPredicate(t PathTarget) bool {
	return len(t.Path) > 0 || len(t.PathClass) > 0 || t.FromImage != nil || len(t.FSType) > 0
}
func argvHasPredicate(a *ArgvPredicate) bool {
	return a != nil && (len(a.Contains) > 0 || len(a.Prefix) > 0 || a.StdinIsPipe != nil)
}
func validateException(a *Actor, targetHasPredicate bool) error {
	if a != nil && a.Ancestor != nil {
		if a.Ancestor.Depth < 1 || a.Ancestor.Depth > MaxAncestorDepth || len(a.Ancestor.Path) == 0 {
			return fmt.Errorf("ancestor requires nonempty path and depth 1..%d", MaxAncestorDepth)
		}
	}
	if !actorHasPredicate(a) && !targetHasPredicate {
		return fmt.Errorf("exception requires at least one actor or target predicate")
	}
	return nil
}
func (e ExecException) Validate() error {
	return validateException(e.Actor, e.Target != nil && (pathHasPredicate(e.Target.PathTarget) || argvHasPredicate(e.Target.Argv)))
}
func (e FileException) Validate() error {
	return validateException(e.Actor, e.Target != nil && (pathHasPredicate(e.Target.PathTarget) || len(e.Target.Access) > 0))
}
func (e NetworkException) Validate() error {
	if e.Target != nil {
		if err := validateNetworkTarget(*e.Target); err != nil {
			return err
		}
	}
	return validateException(e.Actor, e.Target != nil && (len(e.Target.CIDR) > 0 || len(e.Target.Ports) > 0 || len(e.Target.Proto) > 0 || len(e.Target.DNSName) > 0 || len(e.Target.DestClass) > 0 || e.Target.Direction != ""))
}
func validateNetworkTarget(t NetworkTarget) error {
	for _, cidr := range t.CIDR {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", cidr, err)
		}
	}
	for _, port := range t.Ports {
		if err := validatePort(port); err != nil {
			return err
		}
	}
	return nil
}
func (e PrivilegeException) Validate() error {
	if e.Target != nil {
		if err := e.Target.Validate(); err != nil {
			return err
		}
	}
	return validateException(e.Actor, e.Target != nil)
}
func (e IdentityException) Validate() error {
	if e.Target != nil {
		if err := e.Target.Validate(); err != nil {
			return err
		}
	}
	return validateException(e.Actor, e.Target != nil)
}
