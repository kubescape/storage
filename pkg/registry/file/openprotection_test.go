package file

import (
	"context"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseOpenProtectionConfigMap(t *testing.T) {
	t.Run("missing key yields empty union", func(t *testing.T) {
		m, err := ParseOpenProtectionConfigMap(map[string]string{"other": "x"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !m.Empty() {
			t.Fatalf("expected empty union, got %+v", m)
		}
	})

	t.Run("empty value yields empty union", func(t *testing.T) {
		m, err := ParseOpenProtectionConfigMap(map[string]string{OpenProtectionConfigMapKey: ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !m.Empty() {
			t.Fatalf("expected empty union, got %+v", m)
		}
	})

	t.Run("valid json parses all matcher kinds", func(t *testing.T) {
		raw := `{"prefix":["/etc/shadow"],"exact":["/etc/sudoers"],"contains":["/.ssh/"],"suffix":[".key"]}`
		m, err := ParseOpenProtectionConfigMap(map[string]string{OpenProtectionConfigMapKey: raw})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(m.Prefix) != 1 || m.Prefix[0] != "/etc/shadow" {
			t.Errorf("prefix mismatch: %+v", m.Prefix)
		}
		if len(m.Exact) != 1 || m.Exact[0] != "/etc/sudoers" {
			t.Errorf("exact mismatch: %+v", m.Exact)
		}
		if len(m.Contains) != 1 || m.Contains[0] != "/.ssh/" {
			t.Errorf("contains mismatch: %+v", m.Contains)
		}
		if len(m.Suffix) != 1 || m.Suffix[0] != ".key" {
			t.Errorf("suffix mismatch: %+v", m.Suffix)
		}
	})

	t.Run("invalid json errors", func(t *testing.T) {
		if _, err := ParseOpenProtectionConfigMap(map[string]string{OpenProtectionConfigMapKey: "{not json"}); err == nil {
			t.Fatal("expected error for invalid json")
		}
	})
}

func TestOpenProtectionStoreGetSet(t *testing.T) {
	s := NewOpenProtectionStore(armotypes.OpenMatchers{Prefix: []string{"/etc/shadow"}})
	if got := s.Get(); len(got.Prefix) != 1 || got.Prefix[0] != "/etc/shadow" {
		t.Fatalf("seed not applied: %+v", got)
	}

	s.Set(armotypes.OpenMatchers{Exact: []string{"/etc/sudoers"}})
	got := s.Get()
	if len(got.Prefix) != 0 {
		t.Errorf("expected prefix cleared after Set, got %+v", got.Prefix)
	}
	if len(got.Exact) != 1 || got.Exact[0] != "/etc/sudoers" {
		t.Errorf("expected exact replaced, got %+v", got.Exact)
	}
}

func TestOpenProtectionReloaderReloadOnce(t *testing.T) {
	const ns, name = "kubescape", "storage-open-protection"

	t.Run("present configmap is applied", func(t *testing.T) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
			Data:       map[string]string{OpenProtectionConfigMapKey: `{"prefix":["/etc/shadow"]}`},
		}
		client := fake.NewSimpleClientset(cm)
		store := NewOpenProtectionStore(armotypes.OpenMatchers{})
		r := NewOpenProtectionReloader(client, ns, name, time.Minute, store)
		if err := r.reloadOnce(context.Background()); err != nil {
			t.Fatalf("reloadOnce: %v", err)
		}
		if got := store.Get(); len(got.Prefix) != 1 || got.Prefix[0] != "/etc/shadow" {
			t.Fatalf("expected protection from configmap, got %+v", got)
		}
	})

	t.Run("missing configmap keeps current protection", func(t *testing.T) {
		client := fake.NewSimpleClientset() // no configmap
		store := NewOpenProtectionStore(armotypes.OpenMatchers{Prefix: []string{"/etc/shadow"}})
		r := NewOpenProtectionReloader(client, ns, name, time.Minute, store)
		if err := r.reloadOnce(context.Background()); err != nil {
			t.Fatalf("reloadOnce should tolerate NotFound: %v", err)
		}
		if got := store.Get(); len(got.Prefix) != 1 || got.Prefix[0] != "/etc/shadow" {
			t.Fatalf("expected seeded protection preserved on NotFound, got %+v", got)
		}
	})

	t.Run("default interval applied for non-positive", func(t *testing.T) {
		r := NewOpenProtectionReloader(fake.NewSimpleClientset(), ns, name, 0, NewOpenProtectionStore(armotypes.OpenMatchers{}))
		if r.interval != DefaultOpenProtectionRefreshInterval {
			t.Fatalf("expected default interval, got %v", r.interval)
		}
	})
}
