# NetworkNeighbors CIDR-based collapsing

## Summary

When a node-agent observes external traffic from a workload, it records each distinct destination IP as a separate `NetworkNeighbor` entry. Since the `Identifier` includes the IP address, high-traffic profiles targeting a range of IPs (e.g. a cloud provider's IP block) can explode into thousands of entries — a real-world case hit **8,687 entries** for a single prefix.

The collapsing pass groups `NetworkNeighbor` entries that differ only by IP (same `Type`, `DNS`, namespace selector, pod selector) and replaces them with a small number of **CIDR-bearing entries** when a group exceeds a configurable threshold. CIDR aggregation is bounded by a configurable floor (minimum prefix length / maximum breadth), so output size is predictable even for scattered IP distributions.

Two new fields in the `CollapseConfiguration` CRD control this:
- `NetworkIPGroupThreshold` (default 50): group size threshold above which IP collapsing is triggered.
- `NetworkCIDRFloorBits` (default 16): minimum CIDR prefix length — no emitted block is ever broader than `/<floorBits>`.

The pass is a **fixpoint**: running it twice on the same input is guaranteed to produce the same output, so collapsed entries remain stable across successive saves without oscillation or duplication.

## Why it matters

Before collapsing, external-traffic profiles with 8,687 individual IP entries consumed excessive storage, slowed queries, and produced NetworkPolicies with thousands of rules — most of which could be expressed as a handful of CIDR blocks.

Collapsing reduces entry count by orders of magnitude for traffic aimed at cloud IP blocks (e.g. `100.68.24.0/22`, `16.15.183.0/24`) while preserving policy correctness: generated NetworkPolicies still include the same destination ranges, just expressed more efficiently as CIDR blocks instead of `/32` host routes.

## How it works

**Grouping**: Entries are grouped by a deterministic key over `(Type, DNS, NamespaceSelector, PodSelector)`. Entries in the same group differ only by IP address.

**Classification**: Each IP value is classified as:
- **IPv4 host literal** (e.g., `192.168.1.5`): aggregatable, fed to the CIDR algorithm.
- **CIDR block** (e.g., `192.168.0.0/24`): treated as an already-covering range, held stable (not re-parsed or re-tightened) to preserve idempotency.
- **`"*"` sentinel or IPv6**: pass-through, never aggregated.

**Aggregation**: If a group's count of aggregatable IPv4 host addresses exceeds `NetworkIPGroupThreshold`:
1. Compute the smallest covering prefix (common leading bits across all hosts).
2. If that prefix's length is at least `NetworkCIDRFloorBits` (e.g., `/16` or longer), emit it as-is.
3. If it would be broader than `NetworkCIDRFloorBits`, split the group into floor-length buckets (e.g., `/16` buckets) and emit one CIDR entry per non-empty bucket.

**Output entries**: Each emitted CIDR entry carries:
- The computed CIDR block(s) in `IPAddresses`.
- The group's **full merged/deduped DNS names and ports** replicated onto every bucket (so each output entry is independently correlated with its ports and DNS for policy generation).
- The group's shared singular `DNS` value (constant across the group by construction, used for policy metadata).
- An `Identifier` derived from the group key plus sorted CIDR list — so the identifier-merge pass on a subsequent save recognizes and re-merges the same collapsed entry instead of duplicating it.
- Empty `IPAddress` (singular field), non-empty `IPAddresses` (plural field).

**Policy generation**: `GenerateNetworkPolicy`'s rule generators (`generateEgressRule`, `generateIngressRule`) now consume the plural `IPAddresses` field:
- **CIDR block** (e.g., `192.168.0.0/16`): becomes an `IPBlock` peer with empty `OriginalIP` (no single original IP for a range).
- **Bare IPv4** (e.g., `192.168.1.5`): mirrored through the existing singular-path logic exactly, including known-server enrichment and `/32` formatting.
- **`"*"` sentinel**: becomes `0.0.0.0/0`.
- **IPv6**: skipped (out of scope for v1).

## Scope / limitations

**Held-stable CIDRs do not retroactively re-narrow when floor is tightened**: If an operator later changes `NetworkCIDRFloorBits` from 16 to 24 (smaller blocks, higher precision), already-emitted `/16` blocks are held stable for idempotency and won't be split retroactively. They persist until that group naturally re-collapses (e.g., new IPs arrive, triggering re-aggregation). This is an intentional trade-off: predictable idempotency wins over floor freshness for held entries.

**New host IPs inside an already-held CIDR are not immediately absorbed**: If a `/16` block covers `192.168.0.0/16` and new traffic arrives to `192.168.5.100` (which falls inside that CIDR), the new IP persists as a separate entry until its own group independently exceeds the threshold. Entry-count creep is bounded by `NetworkIPGroupThreshold` and the existing merge logic, so this is not unbounded.

**CIDR/`"*"` peers skip known-server enrichment**: A CIDR block is a range, not a single IP, so it cannot be looked up in the known-servers registry. CIDR and `"*"` entries produce bare `IPBlock` peers without the `PolicyRef` name/server enrichment that singular IPs enjoy. Bare-IP elements of the plural `IPAddresses` field retain full known-server matching identical to the singular-field path.

**IPv4 only for v1**: IPv6 addresses pass through uncollapsed; future work may add IPv6 support.

## Configuration

Both fields are part of the existing `CollapseConfiguration` CRD singleton (`default`), using the same zero-means-default semantics as the existing path-collapsing thresholds:

```yaml
apiVersion: spdx.softwarecomposition.kubescape.io/v1beta1
kind: CollapseConfiguration
metadata:
  name: default
spec:
  # ... existing fields (OpenDynamicThreshold, EndpointDynamicThreshold, CollapseConfigs) ...

  # IP collapsing thresholds (optional; omit or set to 0 for defaults)
  networkIPGroupThreshold: 50     # Collapse groups of 50+ hosts
  networkCIDRFloorBits: 16        # No block narrower than /16
```

Zero or omitted values use the compiled-in defaults (50 and 16 respectively). No operator restart is required; the provider reads the singleton at each request.

## Verifying

**Entry count**: Compare entry counts before and after enabling the feature on a real profile with external traffic. A profile with 8,687 external-traffic entries should drop to a small number (typically hundreds or fewer CIDR entries, depending on traffic distribution).

**CIDR breadth**: Inspect emitted `NetworkNeighbor` entries; no single `IPAddresses` CIDR block should exceed the configured floor (default `/16`).

**Idempotency**: Run the collapse pass twice in succession (e.g., two sequential saves) on the same profile and assert the second pass's output is byte-identical to the first (fixpoint).

**Policy generation**: Feed a collapsed `NetworkNeighborhood` through `GenerateNetworkPolicy` and verify the generated NetworkPolicy includes `IPBlock` rules covering the collapsed CIDRs (test CIDR, bare-IP, and `"*"` cases). Confirm no rule has ports without at least one corresponding peer.

**Known-server enrichment**: Verify that bare-IP entries of `IPAddresses` still receive known-server `PolicyRef` enrichment (identical to singular-field behavior), while CIDR and `"*"` entries produce bare `IPBlock` peers without enrichment.
