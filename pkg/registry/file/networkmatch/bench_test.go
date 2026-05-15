package networkmatch

import "testing"

// Benchmarks for the IP matcher.
//
// Targets (CI runner reference, see README.md):
//   IP literal / CIDR : < 200 ns/op
//   long mixed list    : < 1 µs/op
//
// Run: go test -bench=. -benchmem ./pkg/registry/file/networkmatch/

func BenchmarkMatchIP_Literal(b *testing.B) {
	profile := []string{"10.1.2.3"}
	for i := 0; i < b.N; i++ {
		_ = MatchIP(profile, "10.1.2.3")
	}
}

func BenchmarkMatchIP_CIDR(b *testing.B) {
	profile := []string{"10.0.0.0/8"}
	for i := 0; i < b.N; i++ {
		_ = MatchIP(profile, "10.1.2.3")
	}
}

func BenchmarkMatchIP_AnySentinel(b *testing.B) {
	profile := []string{"*"}
	for i := 0; i < b.N; i++ {
		_ = MatchIP(profile, "10.1.2.3")
	}
}

func BenchmarkMatchIP_LongMixedList(b *testing.B) {
	// Worst case: 10 entries, observed IP not in any of them.
	// Validates that adding entries scales linearly without per-entry alloc.
	profile := []string{
		"10.1.2.3", "10.1.2.4", "10.1.2.5",
		"192.168.0.0/16",
		"172.16.0.0/12",
		"8.8.8.8", "8.8.4.4",
		"2001:db8::/32",
		"203.0.113.0/24",
		"198.51.100.0/24",
	}
	for i := 0; i < b.N; i++ {
		_ = MatchIP(profile, "1.1.1.1")
	}
}

func BenchmarkMatchIP_LongMixedList_HitFirst(b *testing.B) {
	// Best case for early-exit: hit on the first entry.
	profile := []string{
		"10.1.2.3", "10.1.2.4", "10.1.2.5",
		"192.168.0.0/16", "172.16.0.0/12",
	}
	for i := 0; i < b.N; i++ {
		_ = MatchIP(profile, "10.1.2.3")
	}
}

// Hot-path benchmark: compile once, match many. This is how
// the CEL-function callers in node-agent SHOULD use the matcher.

func BenchmarkCompiledIPMatcher_Literal(b *testing.B) {
	m := CompileIP([]string{"10.1.2.3"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("10.1.2.3")
	}
}

func BenchmarkCompiledIPMatcher_CIDR(b *testing.B) {
	m := CompileIP([]string{"10.0.0.0/8"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("10.1.2.3")
	}
}

func BenchmarkCompiledIPMatcher_LongMixedList(b *testing.B) {
	m := CompileIP([]string{
		"10.1.2.3", "10.1.2.4", "10.1.2.5",
		"192.168.0.0/16",
		"172.16.0.0/12",
		"8.8.8.8", "8.8.4.4",
		"2001:db8::/32",
		"203.0.113.0/24",
		"198.51.100.0/24",
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("1.1.1.1")
	}
}

// DNS matcher benchmarks. Targets (CI runner reference):
//   DNS literal     : < 300 ns/op
//   DNS wildcard    : < 600 ns/op

func BenchmarkMatchDNS_Literal(b *testing.B) {
	profile := []string{"api.stripe.com."}
	for i := 0; i < b.N; i++ {
		_ = MatchDNS(profile, "api.stripe.com.")
	}
}

func BenchmarkMatchDNS_LeadingWildcard(b *testing.B) {
	profile := []string{"*.stripe.com."}
	for i := 0; i < b.N; i++ {
		_ = MatchDNS(profile, "webhooks.stripe.com.")
	}
}

func BenchmarkCompiledDNSMatcher_Literal(b *testing.B) {
	m := CompileDNS([]string{"api.stripe.com."})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("api.stripe.com.")
	}
}

func BenchmarkCompiledDNSMatcher_LeadingWildcard(b *testing.B) {
	m := CompileDNS([]string{"*.stripe.com."})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("webhooks.stripe.com.")
	}
}

func BenchmarkCompiledDNSMatcher_DeepName(b *testing.B) {
	// 10-label observed name against a leading-* pattern (will miss).
	m := CompileDNS([]string{"*.stripe.com."})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("a.b.c.d.e.f.g.h.stripe.com.")
	}
}

func BenchmarkCompiledDNSMatcher_LongMixedList(b *testing.B) {
	m := CompileDNS([]string{
		"api.stripe.com.",
		"*.stripe.com.",
		"api.partner.io.",
		"kubernetes.⋯.svc.cluster.local.",
		"internal.*",
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Match("kubernetes.production.svc.cluster.local.")
	}
}
