// Command perfab is the statistics half of the Tier B paired A/B driver
// (hack/perf-ab.sh; design: .omc/plans/raw-write-bypass-elimination.md A.13.4).
//
//	perfab probe   -thresholds F -rounds a.json,b.json,c.json
//	perfab verdict -thresholds F -pairs N -schedule S -base b1.json,... -head h1.json,... [-noisy]
//
// probe checks the A A A early-abort: the CV of every headline metric over the
// probe rounds must stay under probe_cv_max_pct.
//
// verdict decides from PAIRS interleaved (base, head) rounds. Per ratio
// metric the primary statistic is a paired t-test on the per-round log-ratios
// d_i = ln(head_i / base_i); Mann-Whitney U on the two samples is the second
// opinion; REGRESSION needs the mean ratio past the threshold AND either test
// significant. Noise is measured from the pairs themselves, post hoc: the
// paired CV is the SD of d_i, and the MDE uses t_{N-1} quantiles. Exit codes:
// 0 PASS, 1 REGRESSION, 2 CONFIG MISMATCH, 3 INCONCLUSIVE, 4 UNDERPOWERED.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

const (
	exitPass           = 0
	exitRegression     = 1
	exitConfigMismatch = 2
	exitInconclusive   = 3
	exitUnderpowered   = 4
)

type thresholds struct {
	Alpha          float64 `json:"alpha"`
	Power          float64 `json:"power"`
	PairedCVMaxPct float64 `json:"paired_cv_max_pct"`
	ProbeCVMaxPct  float64 `json:"probe_cv_max_pct"`
	Metrics        []metricSpec
	Hard           []string     `json:"hard"`
	MetricsRaw     []metricSpec `json:"metrics"`
}

type metricSpec struct {
	Name            string  `json:"name"`
	Kind            string  `json:"kind"` // ratio | count | points | info
	Direction       string  `json:"direction"`
	ThresholdPct    float64 `json:"threshold_pct"`
	ThresholdPoints float64 `json:"threshold_points"`
	Headline        bool    `json:"headline"`
}

type round struct {
	file      string
	Effective map[string]any     `json:"effective"`
	Series    map[string]float64 `json:"series"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "probe":
		os.Exit(runProbe(os.Args[2:]))
	case "verdict":
		os.Exit(runVerdict(os.Args[2:]))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: perfab probe|verdict [flags]")
	os.Exit(64)
}

func loadThresholds(path string) thresholds {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal("read thresholds: %v", err)
	}
	var t thresholds
	if err := json.Unmarshal(data, &t); err != nil {
		fatal("parse thresholds: %v", err)
	}
	t.Metrics = t.MetricsRaw
	if t.Alpha == 0 {
		t.Alpha = 0.05
	}
	if t.Power == 0 {
		t.Power = 0.8
	}
	return t
}

func loadRounds(list string) []round {
	var out []round
	for _, f := range strings.Split(list, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			fatal("read round %s: %v", f, err)
		}
		var r round
		if err := json.Unmarshal(data, &r); err != nil {
			fatal("parse round %s: %v", f, err)
		}
		r.file = f
		out = append(out, r)
	}
	return out
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "perfab: "+format+"\n", args...)
	os.Exit(64)
}

// ---- probe ----

func runProbe(args []string) int {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	thrPath := fs.String("thresholds", "", "thresholds JSON")
	roundsList := fs.String("rounds", "", "comma-separated round JSON files")
	_ = fs.Parse(args)
	thr := loadThresholds(*thrPath)
	rounds := loadRounds(*roundsList)
	if len(rounds) < 2 {
		fatal("probe needs at least 2 rounds")
	}
	worst := 0.0
	worstName := ""
	fmt.Printf("%-26s %10s %10s %8s\n", "probe metric", "mean", "sd", "CV%")
	for _, m := range thr.Metrics {
		if !m.Headline {
			continue
		}
		xs := series(rounds, m.Name)
		cv := 100 * sd(xs) / mean(xs)
		if math.IsNaN(cv) {
			cv = 0
		}
		fmt.Printf("%-26s %10.3f %10.3f %7.1f%%\n", m.Name, mean(xs), sd(xs), cv)
		if cv > worst {
			worst, worstName = cv, m.Name
		}
	}
	if worst > thr.ProbeCVMaxPct {
		fmt.Printf("PROBE ABORT: %s CV %.1f%% over %d probe rounds exceeds %.0f%% (machine too noisy to spend the pairs)\n",
			worstName, worst, len(rounds), thr.ProbeCVMaxPct)
		return exitInconclusive
	}
	fmt.Printf("PROBE OK: worst headline CV %.1f%% (%s) over %d rounds, under %.0f%%\n", worst, worstName, len(rounds), thr.ProbeCVMaxPct)
	return exitPass
}

// ---- verdict ----

type row struct {
	name      string
	kind      string
	base      float64 // mean of base samples
	head      float64
	effect    string // ratio % or difference
	pT        float64
	pMW       float64
	split     bool
	sdPct     float64 // paired CV (SD of log-ratios), %
	baseCVPct float64
	n         int
	mdePct    float64
	nPrime    int
	thr       string
	verdict   string // PASS | REGRESSION | UNDERPOWERED | HARD | n/a
	breach    bool
	sig       bool
	underpow  bool
}

func runVerdict(args []string) int {
	fs := flag.NewFlagSet("verdict", flag.ExitOnError)
	thrPath := fs.String("thresholds", "", "thresholds JSON")
	pairs := fs.Int("pairs", 0, "number of interleaved pairs")
	schedulePath := fs.String("schedule", "", "schedule.txt written by hack/perf-ab.sh")
	baseList := fs.String("base", "", "comma-separated base round JSON files, in round order")
	headList := fs.String("head", "", "comma-separated head round JSON files, in round order")
	noisy := fs.Bool("noisy", false, "the start-of-run load check was overridden (PERF_AB_ALLOW_NOISY=1)")
	_ = fs.Parse(args)

	thr := loadThresholds(*thrPath)
	base := loadRounds(*baseList)
	head := loadRounds(*headList)
	if len(base) == 0 || len(base) != len(head) {
		fatal("need the same non-zero number of base and head rounds (got %d and %d)", len(base), len(head))
	}
	n := len(base)
	if *pairs == 0 {
		*pairs = n
	}
	suffix := ""
	if *noisy {
		suffix = " (noisy)"
	}

	// 1. Effective-config echo: every round must have run the same workload.
	if field, a, b, file := effectiveMismatch(base, head); field != "" {
		fmt.Printf("CONFIG MISMATCH %s: %v (base %s) vs %v (%s)%s\n", field, a, base[0].file, b, file, suffix)
		return exitConfigMismatch
	}

	// 2. Load marks: more than ceil(PAIRS/3) marked pairs -> inconclusive.
	marked, markedList := markedPairs(*schedulePath)
	maxMarked := int(math.Ceil(float64(*pairs) / 3))

	// 3. Per-metric rows.
	var rows []row
	for _, m := range thr.Metrics {
		rows = append(rows, evalMetric(m, base, head, thr))
	}
	var hardRows []row
	for _, h := range thr.Hard {
		hardRows = append(hardRows, evalHard(h, base, head))
	}

	printTable(rows, hardRows, n)

	// 4. Verdict, in precedence order (see the package comment).
	if marked > maxMarked {
		fmt.Printf("INCONCLUSIVE load-marked=%d/%d (pairs %s; more than ceil(%d/3)=%d)%s\n", marked, *pairs, markedList, *pairs, maxMarked, suffix)
		return exitInconclusive
	}
	for _, h := range hardRows {
		if h.breach {
			fmt.Printf("REGRESSION %s head=%g base=%g (hard row: any on HEAD when BASE has none)%s\n", h.name, h.head, h.base, suffix)
			return exitRegression
		}
	}
	for _, r := range rows {
		if r.kind == "ratio" && isHeadline(thr, r.name) && r.sdPct > thr.PairedCVMaxPct {
			fmt.Printf("INCONCLUSIVE paired-CV=%.1f%% on %s (limit %.0f%%)%s\n", r.sdPct, r.name, thr.PairedCVMaxPct, suffix)
			return exitInconclusive
		}
	}
	for _, r := range rows {
		if r.breach && r.sig {
			fmt.Printf("REGRESSION %s %s t=%.3f mw=%.3f%s\n", r.name, r.effect, r.pT, r.pMW, suffix)
			return exitRegression
		}
	}
	maxNPrime := 0
	worst := ""
	for _, r := range rows {
		if r.underpow && r.nPrime > maxNPrime {
			maxNPrime, worst = r.nPrime, r.name
		}
	}
	if maxNPrime > n {
		fmt.Printf("UNDERPOWERED N'=%d (%s: MDE exceeds its threshold at N=%d)%s\n", maxNPrime, worst, n, suffix)
		return exitUnderpowered
	}
	fmt.Printf("PASS%s\n", suffix)
	return exitPass
}

func isHeadline(thr thresholds, name string) bool {
	for _, m := range thr.Metrics {
		if m.Name == name {
			return m.Headline
		}
	}
	return false
}

func series(rounds []round, name string) []float64 {
	out := make([]float64, 0, len(rounds))
	for _, r := range rounds {
		v, ok := r.Series[name]
		if !ok {
			fatal("round %s has no series %q", r.file, name)
		}
		out = append(out, v)
	}
	return out
}

func evalMetric(m metricSpec, base, head []round, thr thresholds) row {
	b := series(base, m.Name)
	h := series(head, m.Name)
	n := len(b)
	r := row{name: m.Name, kind: m.Kind, base: mean(b), head: mean(h), n: n, baseCVPct: 100 * sd(b) / mean(b)}
	if math.IsNaN(r.baseCVPct) {
		r.baseCVPct = 0
	}
	worseIfHigher := m.Direction != "higher_is_better"
	r.pMW = mannWhitneyP(b, h)

	switch m.Kind {
	case "ratio":
		d := make([]float64, n)
		positive := true
		for i := range b {
			if b[i] <= 0 || h[i] <= 0 {
				positive = false
				break
			}
			d[i] = math.Log(h[i] / b[i])
		}
		if !positive {
			// A zero sample makes the log-ratio undefined; fall back to the
			// paired difference and report it as such.
			for i := range b {
				d[i] = h[i] - b[i]
			}
			r.effect = fmt.Sprintf("diff=%+.3f", mean(d))
			r.pT = pairedTP(d)
			r.sig = r.pT < thr.Alpha || r.pMW < thr.Alpha
			r.split = (r.pT < thr.Alpha) != (r.pMW < thr.Alpha)
			r.thr = fmt.Sprintf("%.0f%%", m.ThresholdPct)
			r.verdict = "n/a(zero)"
			return r
		}
		md := mean(d)
		s := sd(d)
		r.sdPct = 100 * s
		r.pT = pairedTP(d)
		ratioPct := 100 * (math.Exp(md) - 1)
		r.effect = fmt.Sprintf("%+.1f%%", ratioPct)
		thrLog := math.Log1p(m.ThresholdPct / 100)
		if worseIfHigher {
			r.breach = md > thrLog
			r.thr = fmt.Sprintf(">+%.0f%%", m.ThresholdPct)
		} else {
			r.breach = md < -thrLog
			r.thr = fmt.Sprintf("<-%.0f%%", m.ThresholdPct)
		}
		r.sig = r.pT < thr.Alpha || r.pMW < thr.Alpha
		r.split = (r.pT < thr.Alpha) != (r.pMW < thr.Alpha)
		// MDE = (t_{N-1,1-alpha/2} + t_{N-1,power}) * s_d / sqrt(N), in log space.
		df := float64(n - 1)
		tsum := tQuantile(1-thr.Alpha/2, df) + tQuantile(thr.Power, df)
		mdeLog := tsum * s / math.Sqrt(float64(n))
		r.mdePct = 100 * (math.Exp(mdeLog) - 1)
		if mdeLog > thrLog && s > 0 {
			r.underpow = true
			// N' = ceil(((t+t) * s_d / thr)^2), iterated once so the quantiles
			// are taken at N'-1 rather than at the current N-1.
			nPrime := int(math.Ceil(math.Pow(tsum*s/thrLog, 2)))
			if nPrime > 1 {
				tsum2 := tQuantile(1-thr.Alpha/2, float64(nPrime-1)) + tQuantile(thr.Power, float64(nPrime-1))
				nPrime = int(math.Ceil(math.Pow(tsum2*s/thrLog, 2)))
			}
			r.nPrime = max(nPrime, n+1)
		}
	case "count":
		d := make([]float64, n)
		for i := range b {
			d[i] = h[i] - b[i]
		}
		r.pT = pairedTP(d)
		r.effect = fmt.Sprintf("sum %g->%g", sum(b), sum(h))
		r.breach = sum(h) > sum(b)
		r.thr = "head>base"
		r.sig = r.pT < thr.Alpha || r.pMW < thr.Alpha
		r.split = (r.pT < thr.Alpha) != (r.pMW < thr.Alpha)
	case "info":
		d := make([]float64, n)
		for i := range b {
			if b[i] > 0 && h[i] > 0 {
				d[i] = math.Log(h[i] / b[i])
			}
		}
		r.pT = pairedTP(d)
		r.sdPct = 100 * sd(d)
		r.effect = fmt.Sprintf("%+.1f%%", 100*(math.Exp(mean(d))-1))
		r.thr = "none"
		r.verdict = "info"
		return r
	case "points":
		d := make([]float64, n)
		for i := range b {
			d[i] = h[i] - b[i]
		}
		r.pT = pairedTP(d)
		r.effect = fmt.Sprintf("%+.2fpp", mean(d))
		r.breach = mean(d) > m.ThresholdPoints
		r.thr = fmt.Sprintf(">+%.0fpp", m.ThresholdPoints)
		r.sig = r.pT < thr.Alpha || r.pMW < thr.Alpha
		r.split = (r.pT < thr.Alpha) != (r.pMW < thr.Alpha)
	default:
		fatal("metric %s: unknown kind %q", m.Name, m.Kind)
	}
	switch {
	case r.breach && r.sig:
		r.verdict = "REGRESSION"
	case r.underpow:
		r.verdict = "UNDERPOWERED"
	case r.breach:
		r.verdict = "breach,n.s."
	default:
		r.verdict = "PASS"
	}
	return r
}

func evalHard(name string, base, head []round) row {
	b := series(base, name)
	h := series(head, name)
	r := row{name: name, kind: "hard", base: sum(b), head: sum(h), n: len(b), thr: "any"}
	r.breach = sum(b) == 0 && sum(h) > 0
	r.effect = fmt.Sprintf("sum %g->%g", sum(b), sum(h))
	if r.breach {
		r.verdict = "REGRESSION"
	} else {
		r.verdict = "PASS"
	}
	return r
}

func printTable(rows, hard []row, n int) {
	fmt.Printf("%-26s %10s %10s %10s %7s %7s %5s %7s %8s %3s %7s %8s %s\n",
		"metric", "base", "head", "effect", "t-p", "mw-p", "SPLIT", "s_d%", "baseCV%", "N", "MDE%", "thr", "verdict")
	for _, r := range rows {
		split := ""
		if r.split {
			split = "SPLIT"
		}
		mde := "-"
		sdp := "-"
		if r.kind == "ratio" && r.verdict != "n/a(zero)" {
			mde = fmt.Sprintf("%.1f", r.mdePct)
			sdp = fmt.Sprintf("%.1f", r.sdPct)
		}
		verdict := r.verdict
		if r.underpow {
			verdict += fmt.Sprintf(" N'=%d", r.nPrime)
		}
		fmt.Printf("%-26s %10.3f %10.3f %10s %7.3f %7.3f %5s %7s %8.1f %3d %7s %8s %s\n",
			r.name, r.base, r.head, r.effect, r.pT, r.pMW, split, sdp, r.baseCVPct, r.n, mde, r.thr, verdict)
	}
	for _, r := range hard {
		fmt.Printf("%-26s %10.0f %10.0f %10s %7s %7s %5s %7s %8s %3d %7s %8s %s\n",
			r.name, r.base, r.head, r.effect, "-", "-", "", "-", "-", r.n, "-", r.thr, r.verdict+" (hard)")
	}
}

// effectiveMismatch compares every round's effective block against base[0]'s.
func effectiveMismatch(base, head []round) (field string, a, b any, file string) {
	ref := base[0].Effective
	check := func(r round) bool {
		keys := map[string]struct{}{}
		for k := range ref {
			keys[k] = struct{}{}
		}
		for k := range r.Effective {
			keys[k] = struct{}{}
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			av, aok := ref[k]
			bv, bok := r.Effective[k]
			if !aok || !bok || fmt.Sprint(av) != fmt.Sprint(bv) {
				field, a, b, file = k, av, bv, r.file
				return true
			}
		}
		return false
	}
	for _, r := range base[1:] {
		if check(r) {
			return
		}
	}
	for _, r := range head {
		if check(r) {
			return
		}
	}
	return "", nil, nil, ""
}

// markedPairs reads schedule.txt ("pair=<i> arm=<base|head> ... marked=<0|1>")
// and counts pairs with at least one marked arm.
func markedPairs(path string) (int, string) {
	if path == "" {
		return 0, "none"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, "none"
		}
		fatal("read schedule: %v", err)
	}
	marked := map[int]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := map[string]string{}
		for _, kv := range strings.Fields(line) {
			if i := strings.IndexByte(kv, '='); i > 0 {
				fields[kv[:i]] = kv[i+1:]
			}
		}
		if fields["marked"] == "1" {
			if p, err := strconv.Atoi(fields["pair"]); err == nil && p > 0 {
				marked[p] = true
			}
		}
	}
	list := make([]string, 0, len(marked))
	for p := range marked {
		list = append(list, strconv.Itoa(p))
	}
	sort.Slice(list, func(i, j int) bool { a, _ := strconv.Atoi(list[i]); b, _ := strconv.Atoi(list[j]); return a < b })
	if len(list) == 0 {
		return 0, "none"
	}
	return len(marked), strings.Join(list, ",")
}

// ---- statistics ----

func sum(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s
}

func mean(x []float64) float64 {
	if len(x) == 0 {
		return math.NaN()
	}
	return sum(x) / float64(len(x))
}

// sd is the sample standard deviation (n-1).
func sd(x []float64) float64 {
	if len(x) < 2 {
		return 0
	}
	m := mean(x)
	ss := 0.0
	for _, v := range x {
		ss += (v - m) * (v - m)
	}
	return math.Sqrt(ss / float64(len(x)-1))
}

// pairedTP is the two-sided p of a one-sample t-test on the differences d.
func pairedTP(d []float64) float64 {
	n := len(d)
	if n < 2 {
		return 1
	}
	s := sd(d)
	if s == 0 {
		if mean(d) == 0 {
			return 1
		}
		return 0
	}
	t := mean(d) / (s / math.Sqrt(float64(n)))
	return 2 * tUpperTail(math.Abs(t), float64(n-1))
}

// tUpperTail is P(T > t) for Student's t with df degrees of freedom.
func tUpperTail(t, df float64) float64 {
	if t <= 0 {
		return 0.5
	}
	x := df / (df + t*t)
	return 0.5 * regIncBeta(df/2, 0.5, x)
}

// tQuantile inverts the t CDF by bisection.
func tQuantile(p, df float64) float64 {
	if p <= 0.5 {
		return 0
	}
	lo, hi := 0.0, 1000.0
	for i := 0; i < 200; i++ {
		mid := (lo + hi) / 2
		if 1-tUpperTail(mid, df) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// regIncBeta is the regularized incomplete beta function I_x(a,b) via the
// continued fraction (Numerical Recipes betacf, Lentz's method).
func regIncBeta(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lbeta := lgamma(a+b) - lgamma(a) - lgamma(b) + a*math.Log(x) + b*math.Log(1-x)
	front := math.Exp(lbeta)
	if x < (a+1)/(a+b+2) {
		return front * betacf(a, b, x) / a
	}
	return 1 - front*betacf(b, a, 1-x)/b
}

func lgamma(x float64) float64 {
	v, _ := math.Lgamma(x)
	return v
}

func betacf(a, b, x float64) float64 {
	const (
		maxIter = 300
		eps     = 3e-14
		fpmin   = 1e-300
	)
	qab := a + b
	qap := a + 1
	qam := a - 1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < fpmin {
		d = fpmin
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIter; m++ {
		fm := float64(m)
		m2 := 2 * fm
		aa := fm * (b - fm) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		h *= d * c
		aa = -(a + fm) * (qab + fm) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < fpmin {
			d = fpmin
		}
		c = 1 + aa/c
		if math.Abs(c) < fpmin {
			c = fpmin
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// mannWhitneyP is the two-sided Mann-Whitney U test with average ranks for
// ties, tie-corrected variance and a continuity correction (the normal
// approximation benchstat also uses for these sample sizes).
func mannWhitneyP(a, b []float64) float64 {
	n1, n2 := len(a), len(b)
	if n1 == 0 || n2 == 0 {
		return 1
	}
	type obs struct {
		v    float64
		from int
	}
	all := make([]obs, 0, n1+n2)
	for _, v := range a {
		all = append(all, obs{v, 0})
	}
	for _, v := range b {
		all = append(all, obs{v, 1})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v < all[j].v })
	ranks := make([]float64, len(all))
	tieTerm := 0.0
	for i := 0; i < len(all); {
		j := i
		for j+1 < len(all) && all[j+1].v == all[i].v {
			j++
		}
		r := float64(i+j+2) / 2 // average of 1-based ranks i+1..j+1
		for k := i; k <= j; k++ {
			ranks[k] = r
		}
		t := float64(j - i + 1)
		if t > 1 {
			tieTerm += t*t*t - t
		}
		i = j + 1
	}
	r1 := 0.0
	for k, o := range all {
		if o.from == 0 {
			r1 += ranks[k]
		}
	}
	fn1, fn2 := float64(n1), float64(n2)
	u1 := r1 - fn1*(fn1+1)/2
	u2 := fn1*fn2 - u1
	u := math.Min(u1, u2)
	mu := fn1 * fn2 / 2
	nt := fn1 + fn2
	variance := fn1 * fn2 / 12 * ((nt + 1) - tieTerm/(nt*(nt-1)))
	if variance <= 0 {
		return 1
	}
	z := (u - mu + 0.5) / math.Sqrt(variance)
	if u == mu {
		return 1
	}
	return 2 * normalUpperTail(math.Abs(z))
}

func normalUpperTail(z float64) float64 {
	return 0.5 * math.Erfc(z/math.Sqrt2)
}
