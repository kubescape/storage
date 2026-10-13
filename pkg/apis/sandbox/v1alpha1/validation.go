package v1alpha1

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidatePolicySpec checks semantic constraints that cannot all be expressed
// in the structural CRD schema. It does not compile rules, resolve classes, or
// prove that an allow is shadowed. Consumers must also respect admission schema
// constraints when constructing typed objects without using the API server.
func ValidatePolicySpec(spec PolicySpec) error {
	if spec.Classes != nil {
		if err := ValidatePolicyClasses(*spec.Classes); err != nil {
			return err
		}
	}
	names := map[string]bool{}
	check := func(rule Rule) error {
		if rule.Name == "" {
			return fmt.Errorf("rule name must not be empty")
		}
		if utf8.RuneCountInString(rule.Name) > MaxRuleNameLength {
			return fmt.Errorf("rule name must be at most %d characters", MaxRuleNameLength)
		}
		if names[rule.Name] {
			return fmt.Errorf("rule name %q must be unique across the policy", rule.Name)
		}
		names[rule.Name] = true
		return nil
	}
	if spec.Exec != nil {
		for _, rule := range spec.Exec.Rules {
			if err := check(rule.Rule); err != nil {
				return err
			}
		}
	}
	if spec.File != nil {
		if err := spec.File.Default.Validate(); err != nil {
			return err
		}
		for _, rule := range spec.File.Rules {
			if err := check(rule.Rule); err != nil {
				return err
			}
		}
	}
	if spec.Network != nil {
		for _, rule := range spec.Network.Rules {
			if err := check(rule.Rule); err != nil {
				return err
			}
			for _, cidr := range rule.Target.CIDR {
				if _, err := netip.ParsePrefix(cidr); err != nil {
					return fmt.Errorf("rule %q: invalid CIDR %q: %w", rule.Name, cidr, err)
				}
			}
			for _, port := range rule.Target.Ports {
				if err := validatePort(port); err != nil {
					return fmt.Errorf("rule %q: %w", rule.Name, err)
				}
			}
		}
	}
	if spec.Privilege != nil {
		for _, rule := range spec.Privilege.Rules {
			if err := check(rule.Rule); err != nil {
				return err
			}
		}
	}
	if spec.Identity != nil {
		for _, rule := range spec.Identity.Rules {
			if err := check(rule.Rule); err != nil {
				return err
			}
		}
	}
	return nil
}
func validatePort(port intstr.IntOrString) error {
	if port.Type == intstr.Int {
		if port.IntVal >= 1 && port.IntVal <= 65535 {
			return nil
		}
	} else if port.Type == intstr.String {
		parts := strings.Split(port.StrVal, "-")
		if len(parts) == 2 {
			start, err1 := strconv.ParseUint(parts[0], 10, 16)
			end, err2 := strconv.ParseUint(parts[1], 10, 16)
			if err1 == nil && err2 == nil && start >= 1 && end >= start {
				return nil
			}
		}
	}
	return fmt.Errorf("port %q must be 1..65535 or an ordered inclusive range within 1..65535", port.String())
}

// ValidatePolicyClasses checks named-set semantics independently of membership
// resolution. Empty sets are supported, but their names must be non-empty.
func ValidatePolicyClasses(classes PolicyClasses) error {
	for _, entries := range []map[string][]string{classes.PathClasses, classes.DestClasses} {
		if _, exists := entries[""]; exists {
			return fmt.Errorf("class names must not be empty")
		}
	}
	return nil
}

// ValidateSandboxPolicySpec includes native Kubernetes selector syntax checks
// that structural shape/operator validation alone does not provide. Like
// ValidatePolicySpec, it complements rather than replaces admission validation.
func ValidateSandboxPolicySpec(spec SandboxPolicySpec) error {
	errs := metav1validation.ValidateLabelSelector(&spec.Selector.PodSelector, metav1validation.LabelSelectorValidationOptions{}, field.NewPath("spec", "selector", "podSelector"))
	if err := errs.ToAggregate(); err != nil {
		return err
	}
	return ValidatePolicySpec(spec.PolicySpec)
}

// ValidateClusterSandboxPolicySpec validates both native label selectors and
// the shared semantic constraints without imposing arbitrary selector limits.
func ValidateClusterSandboxPolicySpec(spec ClusterSandboxPolicySpec) error {
	opts := metav1validation.LabelSelectorValidationOptions{}
	errs := metav1validation.ValidateLabelSelector(&spec.Selector.PodSelector, opts, field.NewPath("spec", "selector", "podSelector"))
	errs = append(errs, metav1validation.ValidateLabelSelector(spec.Selector.NamespaceSelector, opts, field.NewPath("spec", "selector", "namespaceSelector"))...)
	if err := errs.ToAggregate(); err != nil {
		return err
	}
	return ValidatePolicySpec(spec.PolicySpec)
}
