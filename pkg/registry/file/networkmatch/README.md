# networkmatch

Wildcard-aware matchers for the `NetworkNeighbor.IPAddresses` and
`NetworkNeighbor.DNSNames` fields, used by node-agent's CEL functions
`nn.was_address_in_{egress,ingress}` and `nn.is_domain_in_{egress,ingress}`.

This package is the runtime counterpart to the spec sections §5.7 (IP)
and §5.8 (DNS) at <https://billofbehavior.fusioncore.ai/bob/docs/drafts/spec-v0.0.2/>.

## Wildcard token vocabulary

Same tokens as the path / argv matchers in `dynamicpathdetector` — see
that package's `coverage_test.go` for the contract.

| Token | IP semantics | DNS semantics |
|---|---|---|
| Literal | byte-equality after canonicalization (net.IP) | byte-equality after trailing-dot normalization |
| CIDR (`a.b.c.d/n`) | `net.IPNet.Contains(observed)` | — |
| `*` as full entry | sugar for `0.0.0.0/0` ∪ `::/0` (any IP) | — |
| `*.<suffix>` (leading) | — | RFC 4592 — exactly one DNS label before `<suffix>` |
| `<a>.⋯.<b>` (mid) | — | DynamicIdentifier — exactly one DNS label between `<a>` and `<b>` |
| `<prefix>.*` (trailing) | — | one or more DNS labels after `<prefix>` (never zero) |
| `**` | reserved (rejected at admission) | reserved (rejected at admission) |

## API

```go
// MatchIP reports whether observedIP matches any of the profile entries.
// Each entry MAY be: a literal IP, a CIDR, or the "*" sentinel.
//
// observedIP is matched as text (the function calls net.ParseIP internally
// so the caller does not need to pre-parse it). Empty profile slice
// returns false (no entries → nothing to match against). Empty observedIP
// returns false (no observation to match).
//
// Compile-once contract: callers running this in a hot path SHOULD wrap
// it in a closure that captures pre-compiled *IPNet values across calls
// (the caller knows the profile's lifecycle, this function does not).
func MatchIP(profileEntries []string, observedIP string) bool

// MatchDNS reports whether observedName matches any of the profile entries.
// Each entry MAY use the wildcard tokens above.
//
// Both profile entries and observedName are normalized before
// comparison: a trailing dot is stripped if present, and labels are
// lowercased for case-insensitive equality.
func MatchDNS(profileEntries []string, observedName string) bool
```

## Performance contract

Both functions are called per network event from R0005 / R0011 / R1003 /
R1009. The benchmarks in `bench_test.go` track:

- `BenchmarkMatchIP_Literal` — baseline byte-equality
- `BenchmarkMatchIP_CIDR` — single CIDR match
- `BenchmarkMatchIP_LongMixedList` — 10-entry mixed list, observed IP not in list (worst case)
- `BenchmarkMatchDNS_Literal` — baseline
- `BenchmarkMatchDNS_LeadingWildcard` — RFC 4592
- `BenchmarkMatchDNS_DeepName` — 10-label observed name against a leading-`*` profile

Targets (CI runner reference):
- IP literal / CIDR: < 200 ns per call
- DNS literal: < 300 ns per call
- DNS wildcard: < 600 ns per call

Beat or hold these on every change; the matcher fires on every network
event captured by the eBPF tracers.

## Testing

`match_ip_test.go` and `match_dns_test.go` are the contract pinning. The
fixtures in `node-agent/tests/resources/network-wildcards/` are the
end-to-end examples; both layers MUST agree.
