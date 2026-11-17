//go:build crdvalidation

package crd

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestKubernetesDefaultMetadata(t *testing.T) {
	for i, definition := range Definitions()[:2] {
		for _, tc := range []struct {
			name, section string
			valid         bool
		}{
			{"exec", `exec: {default: deny, defaultMode: enforce, defaultOrigin: ""}`, true},
			{"file-scalar", `file: {default: allow, defaultMode: audit, defaultOrigin: "catalog/F1"}`, true},
			{"file-map", `file: {defaultByAccess: {read: allow, write: deny, create: deny, delete: deny, rename: deny, chmod: deny, execMap: deny}, defaultModeByAccess: {read: audit, execMap: enforce}, defaultOriginByAccess: {read: "", write: "catalog/F1"}}`, true},
			{"network", `network: {default: {egress: deny, listen: allow}, defaultMode: {listen: enforce}, defaultOrigin: {egress: ""}}`, true},
			{"empty-partial-maps", `network: {default: {egress: deny, listen: allow}, defaultMode: {}, defaultOrigin: {}}`, true},
			{"privilege", `privilege: {default: deny, defaultMode: audit, defaultOrigin: "catalog/P1"}`, true},
			{"identity", `identity: {default: deny, defaultMode: enforce, defaultOrigin: "catalog/I1"}`, true},
			{"exec-off", `exec: {default: deny, defaultMode: 'off'}`, false},
			{"file-map-off", `file: {defaultByAccess: {read: allow, write: deny, create: deny, delete: deny, rename: deny, chmod: deny, execMap: deny}, defaultModeByAccess: {chmod: 'off'}}`, false},
			{"network-off", `network: {default: {egress: deny, listen: deny}, defaultMode: {egress: 'off'}}`, false},
			{"exec-mode-shape", `exec: {default: deny, defaultMode: {egress: enforce}}`, false},
			{"exec-origin-shape", `exec: {default: deny, defaultOrigin: [catalog/E1]}`, false},
			{"network-mode-shape", `network: {default: {egress: deny, listen: deny}, defaultMode: enforce}`, false},
			{"network-origin-shape", `network: {default: {egress: deny, listen: deny}, defaultOrigin: catalog/E1}`, false},
			{"file-cross-mode", `file: {default: deny, defaultModeByAccess: {read: enforce}}`, false},
			{"file-cross-origin", `file: {default: deny, defaultOriginByAccess: {}}`, false},
			{"file-map-cross-mode", `file: {defaultByAccess: {read: allow, write: deny, create: deny, delete: deny, rename: deny, chmod: deny, execMap: deny}, defaultMode: audit}`, false},
			{"file-map-cross-empty-origin", `file: {defaultByAccess: {read: allow, write: deny, create: deny, delete: deny, rename: deny, chmod: deny, execMap: deny}, defaultOrigin: ""}`, false},
		} {
			t.Run(definition.Spec.Names.Kind+"/"+tc.name, func(t *testing.T) {
				selector := "{podSelector: {}}"
				if i == 1 {
					selector = "{podSelector: {}, namespaceSelector: {}}"
				}
				raw := fmt.Sprintf("apiVersion: kubescape.io/v1alpha1\nkind: %s\nmetadata: {name: test}\nspec:\n  selector: %s\n  containers: ['*']\n  mode: 'off'\n  %s\n", definition.Spec.Names.Kind, selector, tc.section)
				obj := decode(t, raw)
				before := runtime.DeepCopyJSONValue(obj)
				if errs := validate(t, definition, obj); (len(errs) == 0) != tc.valid {
					t.Fatalf("valid=%v, errors=%v", tc.valid, errs.ToAggregate())
				}
				if tc.valid && !reflect.DeepEqual(before, obj) {
					t.Fatal("valid metadata was pruned")
				}
			})
		}
	}
}

func TestKubernetesDenyExceptions(t *testing.T) {
	targets := map[string]string{"exec": "{}", "file": "{}", "network": "{}", "privilege": "{syscall: [unshare]}", "identity": "{imds: true}"}
	for i, definition := range Definitions()[:2] {
		for section, target := range targets {
			for _, tc := range []struct {
				name, action, exceptions string
				valid                    bool
			}{
				{"actor-only", "deny", `[{actor: {fromImage: false}}]`, true},
				{"actor-and-target", "deny", `[{actor: {role: [helper]}, target: ` + target + `}]`, true},
				{"empty-list", "deny", `[]`, true},
				{"empty-entry", "deny", `[{}]`, false},
				{"empty-actor", "deny", `[{actor: {}}]`, false},
				{"empty-target", "deny", `[{target: {}}]`, false},
				{"empty-both", "deny", `[{actor: {}, target: {}}]`, false},
				{"empty-list-predicate", "deny", `[{actor: {path: []}}]`, false},
				{"depth-zero", "deny", `[{actor: {ancestor: {path: [/bin/sh], depth: 0}}}]`, false},
				{"depth-large", "deny", `[{actor: {ancestor: {path: [/bin/sh], depth: 17}}}]`, false},
				{"allow-entry", "allow", `[{actor: {role: [helper]}}]`, false},
				{"allow-empty-list", "allow", `[]`, false},
			} {
				t.Run(definition.Spec.Names.Kind+"/"+section+"/"+tc.name, func(t *testing.T) {
					obj := exceptionPolicy(t, definition.Spec.Names.Kind, i == 1, section, target, tc.action, tc.exceptions)
					before := runtime.DeepCopyJSONValue(obj)
					if errs := validate(t, definition, obj); (len(errs) == 0) != tc.valid {
						t.Fatalf("valid=%v, errors=%v", tc.valid, errs.ToAggregate())
					}
					if tc.valid && !reflect.DeepEqual(before, obj) {
						t.Fatal("valid exception was pruned")
					}
				})
			}
		}
		for _, tc := range []struct {
			name, section, exception string
			valid                    bool
		}{
			{"exec-argv-false", "exec", `{target: {argv: {stdinIsPipe: false}}}`, true},
			{"exec-unresolved", "exec", `{target: {pathClass: [not-found]}}`, true},
			{"exec-empty-argv", "exec", `{target: {argv: {}}}`, false},
			{"exec-set-role", "exec", `{target: {path: [/bin/helper], setRole: helper}}`, false},
			{"exec-set-role-with-actor", "exec", `{actor: {role: [helper]}, target: {setRole: helper}}`, false},
			{"file-access", "file", `{target: {access: [read]}}`, true},
			{"file-bad-access", "file", `{target: {access: [execute]}}`, false},
			{"network-port", "network", `{target: {ports: [443, "8000-9000"], cidr: ["10.0.0.0/8"]}}`, true},
			{"network-cidr", "network", `{target: {cidr: [bad]}}`, false},
			{"network-port-zero", "network", `{target: {ports: [0]}}`, false},
			{"privilege-flags", "privilege", `{target: {syscall: [unshare], flags: [CLONE_NEWUSER]}}`, true},
			{"privilege-two-primitives", "privilege", `{target: {syscall: [unshare], capability: [CAP_SYS_ADMIN]}}`, false},
			{"privilege-flags-without-syscall", "privilege", `{target: {capability: [CAP_SYS_ADMIN], flags: [CLONE_NEWUSER]}}`, false},
			{"identity-false", "identity", `{target: {imds: false}}`, true},
			{"identity-two-primitives", "identity", `{target: {imds: false, envRead: false}}`, false},
		} {
			t.Run(definition.Spec.Names.Kind+"/"+tc.name, func(t *testing.T) {
				obj := exceptionPolicy(t, definition.Spec.Names.Kind, i == 1, tc.section, targets[tc.section], "deny", "["+tc.exception+"]")
				if errs := validate(t, definition, obj); (len(errs) == 0) != tc.valid {
					t.Fatalf("valid=%v, errors=%v", tc.valid, errs.ToAggregate())
				}
			})
		}
	}
}

func exceptionPolicy(t *testing.T, kind string, cluster bool, section, target, action, exceptions string) map[string]any {
	t.Helper()
	selector := "{podSelector: {}}"
	if cluster {
		selector = "{podSelector: {}, namespaceSelector: {}}"
	}
	defaultValue := "deny"
	if section == "network" {
		defaultValue = "{egress: deny, listen: deny}"
	}
	return decode(t, fmt.Sprintf("apiVersion: kubescape.io/v1alpha1\nkind: %s\nmetadata: {name: test}\nspec:\n  selector: %s\n  containers: ['*']\n  mode: enforce\n  %s: {default: %s, rules: [{name: test, action: %s, origin: '', target: %s, except: %s}]}\n", kind, selector, section, defaultValue, action, target, exceptions))
}

func TestKubernetesExceptionsHaveNoArbitraryListLimit(t *testing.T) {
	entries := strings.TrimSuffix(strings.Repeat("{actor: {role: [helper]}},", 1025), ",")
	for _, section := range []string{"exec", "privilege"} {
		target := "{}"
		if section == "privilege" {
			target = "{syscall: [unshare]}"
		}
		obj := exceptionPolicy(t, "SandboxPolicy", false, section, target, "deny", "["+entries+"]")
		if errs := validate(t, Definitions()[0], obj); len(errs) != 0 {
			t.Fatal(errs.ToAggregate())
		}
	}
}
