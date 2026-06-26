package networkmatch_test

// Differential-oracle tests for the network matchers.
//
// Idea: re-implement the SAME admission decision as the production matcher,
// but with a deliberately different algorithm — an "oracle" that is simple
// enough to be obviously correct:
//
//   - IP:  CIDR containment via big.Int prefix comparison (instead of
//          net.IPNet.Contains), plus independent routing of literal / CIDR /
//          "*" / malformed entries.
//   - DNS: each profile pattern is translated into an anchored regexp over the
//          dot-joined label string (instead of the hand-written label walk in
//          matchDNSPattern).
//
// Thousands of randomized (profile, observed) pairs are fed through BOTH the
// production matcher and the oracle; any divergence fails the test with the
// exact inputs. The RNG seed is fixed, so every failure is reproducible and
// the corpus is stable across runs.
//
// Generators may "derive" an observed value from a profile entry to exercise
// the match==true paths; a buggy generator can only reduce coverage (both
// implementations still see the same input and must agree), never produce a
// false failure.

import (
	"fmt"
	"math/big"
	"math/rand"
	"net"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/networkmatch"
)

const (
	diffSeed  = 0x5b0b2026
	diffIters = 20000
)

// ---------------------------------------------------------------------------
// IP oracle
// ---------------------------------------------------------------------------

// ipToInt returns the numeric value of ip and its address width in bits
// (32 for IPv4, 128 for IPv6). IPv4-mapped IPv6 is normalised to IPv4.
func ipToInt(ip net.IP) (*big.Int, int, bool) {
	if v4 := ip.To4(); v4 != nil {
		return new(big.Int).SetBytes(v4), 32, true
	}
	if v16 := ip.To16(); v16 != nil {
		return new(big.Int).SetBytes(v16), 128, true
	}
	return nil, 0, false
}

// ipOracleCIDR independently decides whether obs is inside "ip/prefix" by
// comparing the top `prefix` bits via a right shift — no net.IPNet.Contains.
func ipOracleCIDR(entry string, obs net.IP) bool {
	slash := strings.IndexByte(entry, '/')
	if slash < 0 {
		return false
	}
	base := net.ParseIP(entry[:slash])
	if base == nil {
		return false
	}
	prefix, err := strconv.Atoi(entry[slash+1:])
	if err != nil {
		return false
	}
	bInt, bBits, ok := ipToInt(base)
	if !ok {
		return false
	}
	oInt, oBits, ok := ipToInt(obs)
	if !ok || oBits != bBits { // different address families never match
		return false
	}
	if prefix < 0 || prefix > bBits {
		return false
	}
	shift := uint(bBits - prefix)
	return new(big.Int).Rsh(bInt, shift).Cmp(new(big.Int).Rsh(oInt, shift)) == 0
}

func ipOracle(entries []string, observed string) bool {
	obs := net.ParseIP(observed)
	if obs == nil { // admission always requires a real address
		return false
	}
	for _, e := range entries {
		switch {
		case e == "":
			continue
		case e == networkmatch.AnyIPSentinel:
			return true // obs already validated above
		case strings.Contains(e, "/"):
			if ipOracleCIDR(e, obs) {
				return true
			}
		default:
			if lit := net.ParseIP(e); lit != nil && lit.Equal(obs) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// DNS oracle
// ---------------------------------------------------------------------------

// dnsCanonLabels lowercases, strips a single trailing dot, splits on "." and
// rejects empty inner labels — same canonicalisation the matcher applies to
// the observed name.
func dnsCanonLabels(name string) ([]string, bool) {
	canon := strings.ToLower(strings.TrimSuffix(name, "."))
	if canon == "" {
		return nil, false
	}
	labels := strings.Split(canon, ".")
	for _, l := range labels {
		if l == "" {
			return nil, false
		}
	}
	return labels, true
}

// dnsPatternRegexp translates one profile entry into an anchored regexp over
// the dot-joined observed label string, mirroring compileDNSPattern's validity
// rules and matchDNSPattern's per-token semantics. ok=false for entries the
// matcher would silently drop.
func dnsPatternRegexp(entry string) (*regexp.Regexp, bool) {
	canon := strings.ToLower(strings.TrimSuffix(entry, "."))
	if canon == "" {
		return nil, false
	}
	labels := strings.Split(canon, ".")
	for _, l := range labels {
		if l == "" || l == "**" { // empty inner label or reserved "**"
			return nil, false
		}
	}
	// A lone leading "*" (single label) is degenerate and rejected.
	if len(labels) == 1 && labels[0] == networkmatch.DNSWildcardLabel {
		return nil, false
	}

	var sb strings.Builder
	sb.WriteString("^")
	for i, l := range labels {
		isFirst := i == 0
		isLast := i == len(labels)-1
		if i > 0 {
			sb.WriteString(`\.`)
		}
		switch {
		case l == networkmatch.DNSWildcardLabel && isLast && !isFirst:
			// trailing "*": one or more labels (spec §5.8 row 3)
			sb.WriteString(`[^.]+(?:\.[^.]+)*`)
		case l == networkmatch.DNSWildcardLabel:
			// leading or mid "*": exactly one label (RFC 4592 / defensive)
			sb.WriteString(`[^.]+`)
		case l == networkmatch.DNSDynamicLabel:
			// "⋯": exactly one label
			sb.WriteString(`[^.]+`)
		default:
			sb.WriteString(regexp.QuoteMeta(l))
		}
	}
	sb.WriteString("$")

	re, err := regexp.Compile(sb.String())
	if err != nil {
		return nil, false
	}
	return re, true
}

func dnsOracle(entries []string, observed string) bool {
	obs, ok := dnsCanonLabels(observed)
	if !ok {
		return false
	}
	joined := strings.Join(obs, ".")
	for _, e := range entries {
		if re, ok := dnsPatternRegexp(e); ok && re.MatchString(joined) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// generators
// ---------------------------------------------------------------------------

func randLabel(r *rand.Rand) string {
	const alpha = "abcdefghijklmnopqrstuvwxyz0123456789-"
	n := 1 + r.Intn(6)
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

func randIPv4(r *rand.Rand) string {
	return fmt.Sprintf("%d.%d.%d.%d", r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256))
}

func randIPv6(r *rand.Rand) string {
	h := make([]string, 8)
	for i := range h {
		h[i] = fmt.Sprintf("%x", r.Intn(0x10000))
	}
	return strings.Join(h, ":")
}

func randIPEntry(r *rand.Rand) string {
	switch r.Intn(8) {
	case 0:
		return networkmatch.AnyIPSentinel
	case 1:
		return randIPv4(r) + "/" + strconv.Itoa(r.Intn(33))
	case 2:
		return randIPv6(r) + "/" + strconv.Itoa(r.Intn(129))
	case 3:
		return "garbage-" + randLabel(r) // malformed, must be dropped
	case 4:
		return randIPv6(r)
	default:
		return randIPv4(r)
	}
}

func randIPObserved(r *rand.Rand, entries []string) string {
	// Half the time, derive an in-range observation from an entry for hit
	// coverage (network/base address of a CIDR, or a literal verbatim).
	if r.Intn(2) == 0 {
		for _, off := range r.Perm(len(entries)) {
			e := entries[off]
			if e == "" || e == networkmatch.AnyIPSentinel {
				continue
			}
			if i := strings.IndexByte(e, '/'); i >= 0 {
				if base := net.ParseIP(e[:i]); base != nil {
					return base.String()
				}
				continue
			}
			if net.ParseIP(e) != nil {
				return e
			}
		}
	}
	switch r.Intn(6) {
	case 0:
		return ""
	case 1:
		return "not-an-ip"
	case 2:
		return randIPv6(r)
	default:
		return randIPv4(r)
	}
}

func randDNSEntry(r *rand.Rand) string {
	n := 1 + r.Intn(4)
	parts := make([]string, n)
	for i := range parts {
		switch r.Intn(7) {
		case 0:
			parts[i] = networkmatch.DNSWildcardLabel
		case 1:
			parts[i] = networkmatch.DNSDynamicLabel
		case 2:
			parts[i] = "**" // malformed, must be dropped
		default:
			parts[i] = randLabel(r)
		}
	}
	s := strings.Join(parts, ".")
	if r.Intn(4) == 0 {
		s += "." // FQDN trailing dot
	}
	return s
}

// deriveDNSObserved expands one entry's wildcards into concrete labels so the
// result is intended to match. Only a generator — correctness is checked by
// the oracle, not assumed here.
func deriveDNSObserved(r *rand.Rand, entry string) (string, bool) {
	canon := strings.ToLower(strings.TrimSuffix(entry, "."))
	if canon == "" {
		return "", false
	}
	labels := strings.Split(canon, ".")
	var out []string
	for i, l := range labels {
		isLast := i == len(labels)-1
		isFirst := i == 0
		switch {
		case l == "" || l == "**":
			return "", false
		case l == networkmatch.DNSWildcardLabel && isLast && !isFirst:
			for k := 0; k < 1+r.Intn(3); k++ { // one or more
				out = append(out, randLabel(r))
			}
		case l == networkmatch.DNSWildcardLabel, l == networkmatch.DNSDynamicLabel:
			out = append(out, randLabel(r)) // exactly one
		default:
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return "", false
	}
	s := strings.Join(out, ".")
	if r.Intn(4) == 0 {
		s += "."
	}
	return s, true
}

func randDNSObserved(r *rand.Rand, entries []string) string {
	if r.Intn(2) == 0 {
		for _, off := range r.Perm(len(entries)) {
			if s, ok := deriveDNSObserved(r, entries[off]); ok {
				return s
			}
		}
	}
	switch r.Intn(8) {
	case 0:
		return ""
	case 1:
		return "a..b" // empty inner label
	default:
		n := 1 + r.Intn(5)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = randLabel(r)
		}
		s := strings.Join(parts, ".")
		if r.Intn(4) == 0 {
			s += "."
		}
		return s
	}
}

// ---------------------------------------------------------------------------
// differential tests
// ---------------------------------------------------------------------------

func TestDifferential_MatchIP(t *testing.T) {
	r := rand.New(rand.NewSource(diffSeed))
	matched := 0
	for i := 0; i < diffIters; i++ {
		entries := make([]string, 1+r.Intn(4))
		for j := range entries {
			entries[j] = randIPEntry(r)
		}
		obs := randIPObserved(r, entries)

		got := networkmatch.MatchIP(entries, obs)
		want := ipOracle(entries, obs)
		if got != want {
			t.Fatalf("MatchIP divergence (seed=%#x iter=%d):\n  entries  = %q\n  observed = %q\n  matcher  = %v\n  oracle   = %v",
				diffSeed, i, entries, obs, got, want)
		}
		if got {
			matched++
		}
	}
	t.Logf("MatchIP: %d/%d matched", matched, diffIters)
	// Guard against the corpus degenerating to all-false (which would make
	// the agreement trivially true and hide matcher bugs on the hit path).
	if matched < diffIters/100 || matched > diffIters-diffIters/100 {
		t.Fatalf("degenerate corpus: %d/%d matched — both paths must be exercised", matched, diffIters)
	}
}

func TestDifferential_MatchDNS(t *testing.T) {
	r := rand.New(rand.NewSource(diffSeed))
	matched := 0
	for i := 0; i < diffIters; i++ {
		entries := make([]string, 1+r.Intn(4))
		for j := range entries {
			entries[j] = randDNSEntry(r)
		}
		obs := randDNSObserved(r, entries)

		got := networkmatch.MatchDNS(entries, obs)
		want := dnsOracle(entries, obs)
		if got != want {
			t.Fatalf("MatchDNS divergence (seed=%#x iter=%d):\n  entries  = %q\n  observed = %q\n  matcher  = %v\n  oracle   = %v",
				diffSeed, i, entries, obs, got, want)
		}
		if got {
			matched++
		}
	}
	t.Logf("MatchDNS: %d/%d matched", matched, diffIters)
	if matched < diffIters/100 || matched > diffIters-diffIters/100 {
		t.Fatalf("degenerate corpus: %d/%d matched — both paths must be exercised", matched, diffIters)
	}
}
