// Package crd provides the canonical SandboxPolicy CRD definitions. Keeping
// construction here lets manifests and API tests share one reproducible schema.
package crd

import (
	"encoding/json"
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
	return object(map[string]schema{"pathClasses": mapOf(entries), "destClasses": mapOf(entries)})
}
func rule(target schema) schema {
	return object(map[string]schema{
		"name": text(), "description": {Type: "string"}, "actor": actor(), "target": target,
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
	// Kubernetes structural schemas have no string-or-object union. This opaque
	// field retains both design forms without pruning. FileDefault.Validate in
	// the typed API checks the union; consumers must not assume admission did so.
	preserve := true
	defaultSchema := schema{XPreserveUnknownFields: &preserve, Description: "allow or deny, or an object with read/write/create/delete/rename/chmod/execMap each set to allow or deny. Consumers must validate this string-or-object union; Kubernetes structural validation cannot do so."}
	return section(defaultSchema, object(fields))
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

	target := object(map[string]schema{
		"cidr": stringList(), "ports": array(port), "proto": array(enumeration("tcp", "udp", "icmp", "unix")),
		"dnsName": stringList(), "destClass": stringList(), "direction": enumeration("egress", "listen"),
	})
	// CIDR validity and DNS/glob semantics are consumer checks, not string formats.
	return section(object(map[string]schema{"egress": action(), "listen": action()}, "egress", "listen"), target)
}
func privilegeSection() schema {
	fields := pathFields()
	fields["syscall"] = array(enumeration("unshare", "setns", "clone", "clone3", "mount", "umount2", "pivot_root", "chroot", "open_tree", "move_mount", "ptrace", "process_vm_readv", "process_vm_writev", "init_module", "finit_module", "bpf", "perf_event_open", "kexec_load", "keyctl", "userfaultfd", "io_uring_setup", "mknod", "setuid", "setgid", "capset"))
	fields["flags"] = array(enumeration("CLONE_NEWUSER", "CLONE_NEWNS", "CLONE_NEWPID", "CLONE_NEWNET", "CLONE_NEWUTS", "CLONE_NEWIPC", "CLONE_NEWCGROUP"))
	fields["capability"] = stringList()
	fields["mountFsType"] = stringList()
	return section(action(), object(fields))
}
func identitySection() schema {
	fields := pathFields()
	fields["imds"] = boolean()
	fields["envRead"] = boolean()
	return section(action(), object(fields))
}
func policySpec(cluster bool) schema {
	selectors := map[string]schema{"podSelector": labelSelector()}
	if cluster {
		selectors["namespaceSelector"] = labelSelector()
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
		"selector": object(selectors, "podSelector"), "containers": stringList(), "mode": enumeration("audit", "enforce", "off"),
		"rootRole": text(), "sources": array(source), "classes": classes(),
		"exec": execSection(), "file": fileSection(), "network": networkSection(),
		"privilege": privilegeSection(), "identity": identitySection(),
	}, "selector", "containers", "mode")
}
func conditions() schema {
	return listMap(object(map[string]schema{
		"type": text(), "status": enumeration("True", "False", "Unknown"), "observedGeneration": nonnegative(),
		"lastTransitionTime": {Type: "string", Format: "date-time"}, "reason": text(), "message": {Type: "string"},
	}, "type", "status", "lastTransitionTime", "reason", "message"), "type")
}
func status() schema {
	tier := enumeration("bpf-lsm", "kprobe-override", "tripwire", "seccomp-only", "audit-only")
	node := object(map[string]schema{
		"name": text(), "tier": tier, "observedGeneration": nonnegative(), "conditions": conditions(),
		"degraded":         listMap(object(map[string]schema{"rule": text(), "requested": ruleMode(), "effective": enumeration("audit", "enforce", "tripwire"), "reason": text()}, "rule", "requested", "effective", "reason"), "rule"),
		"appliesOnRestart": stringList(), "shadowedAllows": stringList(), "ruleHitCounts": mapOf(nonnegative()),
		"stale": boolean(), "error": {Type: "string"}, "lastCompiled": {Type: "string", Format: "date-time"},
	}, "name", "tier", "stale")
	return object(map[string]schema{
		"observedGeneration": nonnegative(), "conditions": conditions(), "nodes": listMap(node, "name"),
		"compiled": object(map[string]schema{"rules": nonnegative(), "fromSources": nonnegative(), "classesResolved": nonnegative(), "classesUnresolved": nonnegative()}, "rules", "fromSources", "classesResolved", "classesUnresolved"),
	})
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
	}{
		{"SandboxPolicy", "sandboxpolicies", "sandboxpolicy", apiextensions.NamespaceScoped, policySpec(false), true},
		{"ClusterSandboxPolicy", "clustersandboxpolicies", "clustersandboxpolicy", apiextensions.ClusterScoped, policySpec(true), true},
		{"SandboxPolicyClasses", "sandboxpolicyclasses", "sandboxpolicyclasses", apiextensions.ClusterScoped, classes(), false},
	} {
		props := map[string]schema{"apiVersion": {Type: "string"}, "kind": {Type: "string"}, "metadata": {Type: "object"}, "spec": entry.spec}
		version := apiextensions.CustomResourceDefinitionVersion{Name: sandbox.SchemeGroupVersion.Version, Served: true, Storage: true}
		if entry.hasStatus {
			props["status"] = status()
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
