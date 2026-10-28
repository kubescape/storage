// Package crd provides the canonical SandboxPolicy CRD definitions. Keeping
// construction here lets manifests and API tests share one reproducible schema.
package crd

import (
	"encoding/json"
	"fmt"
	sandbox "github.com/kubescape/storage/pkg/apis/sandbox/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type schema = apiextensions.JSONSchemaProps

func object(properties map[string]schema, required ...string) schema {
	return schema{Type: "object", Properties: properties, Required: required}
}
func text() schema { return schema{Type: "string", MinLength: int64ptr(1)} }
func enumeration(values ...string) schema {
	s := text()
	for _, value := range values {
		b, _ := json.Marshal(value)
		s.Enum = append(s.Enum, apiextensions.JSON{Raw: b})
	}
	return s
}
func array(item schema) schema {
	return schema{Type: "array", Items: &apiextensions.JSONSchemaPropsOrArray{Schema: &item}, MinItems: int64ptr(1)}
}
func listMap(item schema, key string) schema {
	s := array(item)
	s.MinItems = nil
	s.XListType = stringptr("map")
	s.XListMapKeys = []string{key}
	return s
}
func stringList() schema          { return array(text()) }
func nonnegative() schema         { return schema{Type: "integer", Format: "int64", Minimum: floatptr(0)} }
func boolean() schema             { return schema{Type: "boolean"} }
func int64ptr(v int64) *int64     { return &v }
func floatptr(v float64) *float64 { return &v }
func stringptr(v string) *string  { return &v }
func mapOf(value schema) schema {
	return schema{Type: "object", AdditionalProperties: &apiextensions.JSONSchemaPropsOrBool{Allows: true, Schema: &value}}
}
func action() schema   { return enumeration("allow", "deny") }
func ruleMode() schema { return enumeration("audit", "enforce") }
func pathFields() map[string]schema {
	return map[string]schema{"path": stringList(), "pathClass": stringList(), "fromImage": boolean(), "fsType": array(enumeration("tmpfs", "overlay", "memfd", "deleted", "procfs", "sysfs"))}
}
func actor() schema {
	uid := nonnegative()
	uid.Maximum = floatptr(4294967295)
	depth := nonnegative()
	depth.Minimum = floatptr(1)
	depth.Maximum = floatptr(sandbox.MaxAncestorDepth)
	depth.Format = "int32"
	return object(map[string]schema{
		"path": stringList(), "fromImage": boolean(), "role": stringList(), "uid": array(uid), "container": stringList(),
		"ancestor": object(map[string]schema{"path": stringList(), "depth": depth}, "path", "depth"),
	})
}
func labelSelector() schema {
	values := array(schema{Type: "string"})
	values.MinItems = nil
	expression := object(map[string]schema{"key": text(), "operator": enumeration("In", "NotIn", "Exists", "DoesNotExist"), "values": values}, "key", "operator")
	expression.XValidations = apiextensions.ValidationRules{{Rule: "self.operator in ['In', 'NotIn'] ? has(self.values) && size(self.values) > 0 : !has(self.values) || size(self.values) == 0", Message: "In/NotIn require values; Exists/DoesNotExist forbid values"}}
	labels := mapOf(schema{Type: "string"})
	expressions := array(expression)
	expressions.MinItems = nil
	selector := object(map[string]schema{"matchLabels": labels, "matchExpressions": expressions})
	// metav1.LabelSelector is an atomic struct. Preserve that native apply
	// contract so multiple managers cannot assemble an unintended selector.
	selector.XMapType = stringptr("atomic")
	return selector
}
func classes() schema {
	entries := stringList()
	entries.MinItems = nil
	named := mapOf(entries)
	named.XValidations = apiextensions.ValidationRules{{Rule: "self.all(k, size(k) > 0)", Message: "class names must not be empty"}}
	return object(map[string]schema{"pathClasses": named, "destClasses": named})
}
func rule(target schema) schema {
	return object(map[string]schema{
		"name": boundedText(sandbox.MaxRuleNameLength), "description": {Type: "string"}, "actor": actor(), "target": target,
		"action": action(), "mode": ruleMode(), "origin": text(),
	}, "name", "target", "action")
}
func section(defaultSchema, target schema) schema {
	return object(map[string]schema{"default": defaultSchema, "rules": listMap(rule(target), "name")}, "default")
}
func execSection() schema {
	fields := pathFields()
	fields["argv"] = object(map[string]schema{"contains": stringList(), "prefix": stringList(), "stdinIsPipe": boolean()})
	fields["setRole"] = text()
	return section(action(), object(fields))
}
func fileSection() schema {
	fields := pathFields()
	fields["access"] = array(enumeration("read", "write", "create", "delete", "rename", "chmod", "execMap"))
	defaults := object(map[string]schema{"read": action(), "write": action(), "create": action(), "delete": action(), "rename": action(), "chmod": action(), "execMap": action()}, "read", "write", "create", "delete", "rename", "chmod", "execMap")
	result := object(map[string]schema{"default": action(), "defaultByAccess": defaults, "rules": listMap(rule(object(fields)), "name")})
	result.XValidations = apiextensions.ValidationRules{{Rule: "has(self.default) != has(self.defaultByAccess)", Message: "exactly one of default and defaultByAccess is required"}}
	return result
}
func networkSection() schema {
	port := schema{
		XIntOrString: true,
		AnyOf:        []schema{{Type: "integer"}, {Type: "string"}},
		Minimum:      floatptr(1), Maximum: floatptr(65535),
		MaxLength: int64ptr(11), Pattern: `^[0-9]{1,5}-[0-9]{1,5}$`,
	}
	// Ordered string-range endpoints are checked by ValidatePolicySpec. CEL
	// here would require arbitrary limits on enclosing rule/port lists merely
	// to satisfy admission cost estimates; the design defines no such limits.

	cidr := text()
	cidr.Format = "cidr"
	target := object(map[string]schema{
		"cidr": array(cidr), "ports": array(port), "proto": array(enumeration("tcp", "udp", "icmp", "unix")),
		"dnsName": stringList(), "destClass": stringList(), "direction": enumeration("egress", "listen"),
	})
	// CIDR format provides baseline admission on Kubernetes 1.28; consumers use
	// netip for strict syntax (the format allows leading zeros). DNS/globs remain
	// consumer checks.
	return section(object(map[string]schema{"egress": action(), "listen": action()}, "egress", "listen"), target)
}

// forbidden retains known removed predicates through pruning so admission rejects
// them, rather than silently broadening a rule by deleting its constraint.
func forbidden(s schema) schema { s.Not = &schema{}; return s }
func privilegeSection() schema {
	fields := map[string]schema{
		"pathClass": stringList(),
		"path":      forbidden(stringList()), "fromImage": forbidden(boolean()), "fsType": forbidden(stringList()),
		"syscall":    array(enumeration("unshare", "setns", "clone", "clone3", "mount", "umount2", "pivot_root", "chroot", "open_tree", "move_mount", "ptrace", "process_vm_readv", "process_vm_writev", "init_module", "finit_module", "bpf", "perf_event_open", "kexec_load", "keyctl", "userfaultfd", "io_uring_setup", "mknod", "setuid", "setgid", "capset")),
		"flags":      array(enumeration("CLONE_NEWUSER", "CLONE_NEWNS", "CLONE_NEWPID", "CLONE_NEWNET", "CLONE_NEWUTS", "CLONE_NEWIPC", "CLONE_NEWCGROUP")),
		"capability": stringList(), "mountFsType": stringList(),
	}
	target := object(fields)
	// Presence-only oneOf avoids CEL cost amplification across unbounded rule lists
	// on Kubernetes 1.28 while preserving the single-primitive contract.
	target.OneOf = []schema{{Required: []string{"syscall"}}, {Required: []string{"capability"}}, {Required: []string{"mountFsType"}}, {Required: []string{"pathClass"}}}
	target.XValidations = apiextensions.ValidationRules{{Rule: "!has(self.flags) || has(self.syscall)", Message: "flags require syscall"}}
	return section(action(), target)
}
func identitySection() schema {
	fields := map[string]schema{"path": stringList(), "pathClass": stringList(), "imds": boolean(), "envRead": boolean(), "fromImage": forbidden(boolean()), "fsType": forbidden(stringList())}
	target := object(fields)
	target.OneOf = []schema{
		{AnyOf: []schema{{Required: []string{"path"}}, {Required: []string{"pathClass"}}}},
		{Required: []string{"imds"}}, {Required: []string{"envRead"}},
	}
	return section(action(), target)
}
func policySpec(cluster bool) schema {
	selectors := map[string]schema{"podSelector": labelSelector()}
	requiredSelectors := []string{"podSelector"}
	if cluster {
		selectors["namespaceSelector"] = labelSelector()
		requiredSelectors = append(requiredSelectors, "namespaceSelector")
	} else {
		// Reject rather than silently prune a misplaced cross-namespace selector.
		// This is a forbidden field, not a supported namespaced API field.
		forbidden := labelSelector()
		forbidden.Not = &schema{}
		forbidden.Description = "Forbidden on SandboxPolicy; use ClusterSandboxPolicy for namespace selection."
		selectors["namespaceSelector"] = forbidden
	}
	source := object(map[string]schema{
		"containerProfile": object(map[string]schema{"name": text()}, "name"),
		"include":          array(enumeration("execs", "opens", "egress", "syscalls", "capabilities")),
	}, "containerProfile", "include")
	return object(map[string]schema{
		"selector": object(selectors, requiredSelectors...), "containers": stringList(), "mode": enumeration("audit", "enforce", "off"),
		"rootRole": text(), "sources": array(source), "classes": classes(),
		"exec": execSection(), "file": fileSection(), "network": networkSection(),
		"privilege": privilegeSection(), "identity": identitySection(),
	}, "selector", "containers", "mode")
}
func boundedText(limit int64) schema {
	s := text()
	s.MaxLength = int64ptr(limit)
	return s
}
func timestamp() schema { return schema{Type: "string", Format: "date-time", MaxLength: int64ptr(64)} }
func conditions() schema {
	s := listMap(object(map[string]schema{
		"type": boundedText(sandbox.MaxStatusConditionKeyLength), "status": enumeration("True", "False", "Unknown"), "observedGeneration": nonnegative(),
		"lastTransitionTime": timestamp(), "reason": boundedText(sandbox.MaxStatusConditionKeyLength),
		"message": {Type: "string", MaxLength: int64ptr(sandbox.MaxStatusDiagnosticLength)},
	}, "type", "status", "lastTransitionTime", "reason", "message"), "type")
	s.MaxItems = int64ptr(sandbox.MaxStatusConditions)
	return s
}
func status() schema {
	summary := object(map[string]schema{"total": nonnegative(), "reporting": nonnegative(), "ready": nonnegative(), "degraded": nonnegative(), "stale": nonnegative()}, "total", "reporting", "ready", "degraded", "stale")
	summary.XValidations = apiextensions.ValidationRules{{Rule: "self.reporting <= self.total && self.ready <= self.reporting && self.degraded <= self.reporting && self.stale <= self.reporting && self.ready <= self.reporting - self.stale", Message: "reporting must be at most total; categories must fit reporting and ready cannot include stale nodes"}}
	return object(map[string]schema{
		"observedGeneration": nonnegative(), "conditions": conditions(), "nodeSummary": summary, "lastAggregated": timestamp(),
	})
}
func nodeStatusSpec() schema {
	spec := object(map[string]schema{
		"policyRef": object(map[string]schema{"kind": enumeration("SandboxPolicy", "ClusterSandboxPolicy"), "name": boundedText(sandbox.MaxReferenceNameLength), "uid": boundedText(sandbox.MaxReferenceUIDLength)}, "kind", "name", "uid"),
		"nodeRef":   object(map[string]schema{"name": boundedText(sandbox.MaxReferenceNameLength), "uid": boundedText(sandbox.MaxReferenceUIDLength)}, "name", "uid"),
	}, "policyRef", "nodeRef")
	spec.XValidations = apiextensions.ValidationRules{{Rule: "self == oldSelf", Message: "policy/node report identity is immutable"}}
	return spec
}
func nodeStatus() schema {
	details := array(boundedText(sandbox.MaxRuleNameLength))
	details.MinItems = nil
	details.MaxItems = int64ptr(sandbox.MaxStatusRuleDetails)
	details.XListType = stringptr("set")
	degraded := listMap(object(map[string]schema{
		"rule": boundedText(sandbox.MaxRuleNameLength), "requested": ruleMode(), "effective": enumeration("audit", "enforce", "tripwire"), "reason": boundedText(sandbox.MaxStatusDiagnosticLength),
	}, "rule", "requested", "effective", "reason"), "rule")
	degraded.MaxItems = int64ptr(sandbox.MaxStatusRuleDetails)
	hits := mapOf(nonnegative())
	hits.MaxProperties = int64ptr(sandbox.MaxStatusRuleDetails)
	hits.XValidations = apiextensions.ValidationRules{{Rule: fmt.Sprintf("self.all(k, size(k) > 0 && size(k) <= %d)", sandbox.MaxRuleNameLength), Message: "rule names must be at most 256 characters"}}
	s := object(map[string]schema{
		"tier":               enumeration("bpf-lsm", "kprobe-override", "tripwire", "seccomp-only", "audit-only"),
		"observedGeneration": nonnegative(), "lastReported": timestamp(), "conditions": conditions(),
		"compiled": object(map[string]schema{"rules": nonnegative(), "fromSources": nonnegative(), "classesResolved": nonnegative(), "classesUnresolved": nonnegative()}, "rules", "fromSources", "classesResolved", "classesUnresolved"),
		"degraded": degraded, "appliesOnRestart": details, "shadowedAllows": details, "ruleHitCounts": hits,
		"counts":    object(map[string]schema{"degraded": nonnegative(), "appliesOnRestart": nonnegative(), "shadowedAllows": nonnegative(), "ruleHitCounts": nonnegative()}, "degraded", "appliesOnRestart", "shadowedAllows", "ruleHitCounts"),
		"truncated": boolean(), "stale": boolean(), "error": {Type: "string", MaxLength: int64ptr(sandbox.MaxStatusErrorLength)}, "lastCompiled": timestamp(),
	}, "tier", "observedGeneration", "lastReported", "counts", "truncated", "stale")
	for _, name := range []string{"degraded", "appliesOnRestart", "shadowedAllows", "ruleHitCounts"} {
		size := "(has(self." + name + ") ? size(self." + name + ") : 0)"
		s.XValidations = append(s.XValidations,
			apiextensions.ValidationRule{Rule: "self.counts." + name + " >= " + size, Message: "counts." + name + " must include every stored detail"},
			apiextensions.ValidationRule{Rule: "self.truncated || self.counts." + name + " == " + size, Message: "omitted " + name + " details require truncated=true"},
		)
	}
	return s
}

// Definitions returns fresh CRDs in a stable order. Policy status uses a
// subresource; classes are configuration and have no agent-written status.
func Definitions() []apiextensions.CustomResourceDefinition {
	var definitions []apiextensions.CustomResourceDefinition
	for _, entry := range []struct {
		kind, plural, singular string
		scope                  apiextensions.ResourceScope
		spec                   schema
		hasStatus              bool
		statusSchema           schema
	}{
		{"SandboxPolicy", "sandboxpolicies", "sandboxpolicy", apiextensions.NamespaceScoped, policySpec(false), true, status()},
		{"ClusterSandboxPolicy", "clustersandboxpolicies", "clustersandboxpolicy", apiextensions.ClusterScoped, policySpec(true), true, status()},
		{"SandboxPolicyClasses", "sandboxpolicyclasses", "sandboxpolicyclasses", apiextensions.ClusterScoped, classes(), false, schema{}},
		{"SandboxPolicyNodeStatus", "sandboxpolicynodestatuses", "sandboxpolicynodestatus", apiextensions.NamespaceScoped, nodeStatusSpec(), true, nodeStatus()},
	} {
		props := map[string]schema{"apiVersion": {Type: "string"}, "kind": {Type: "string"}, "metadata": {Type: "object"}, "spec": entry.spec}
		version := apiextensions.CustomResourceDefinitionVersion{Name: sandbox.SchemeGroupVersion.Version, Served: true, Storage: true}
		if entry.hasStatus {
			props["status"] = entry.statusSchema
			version.Subresources = &apiextensions.CustomResourceSubresources{Status: &apiextensions.CustomResourceSubresourceStatus{}}
		}
		version.Schema = &apiextensions.CustomResourceValidation{OpenAPIV3Schema: ptrSchema(object(props, "spec"))}
		definitions = append(definitions, apiextensions.CustomResourceDefinition{
			TypeMeta:   metav1.TypeMeta{APIVersion: "apiextensions.k8s.io/v1", Kind: "CustomResourceDefinition"},
			ObjectMeta: metav1.ObjectMeta{Name: entry.plural + "." + sandbox.GroupName},
			Spec:       apiextensions.CustomResourceDefinitionSpec{Group: sandbox.GroupName, Names: apiextensions.CustomResourceDefinitionNames{Kind: entry.kind, ListKind: entry.kind + "List", Plural: entry.plural, Singular: entry.singular}, Scope: entry.scope, Versions: []apiextensions.CustomResourceDefinitionVersion{version}},
		})
	}
	return definitions
}
func ptrSchema(s schema) *schema { return &s }
