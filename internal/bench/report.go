package bench

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Prices are USD per million tokens. Cache reads and cache writes are billed separately from the rest of input.
type Prices struct {
	Input      float64
	Cached     float64
	Output     float64
	CacheWrite float64
	// CacheWriteGiven is true when --prices named the cache-write price instead of defaulting it.
	CacheWriteGiven bool
}

func costOf(run RunRecord, prices Prices) float64 {
	return (float64(uncachedInput(run))*prices.Input+float64(run.Cached)*prices.Cached+float64(cacheWrite(run))*prices.CacheWrite+float64(run.Output)*prices.Output)/1e6 + (float64(run.JevInput) * 0.042 / 1e6)
}

func median(values []float64) *float64 {
	var sorted []float64
	for _, v := range values {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			sorted = append(sorted, v)
		}
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.Float64s(sorted)
	mid := len(sorted) >> 1
	var v float64
	if len(sorted)%2 == 1 {
		v = sorted[mid]
	} else {
		v = (sorted[mid-1] + sorted[mid]) / 2
	}
	return &v
}

func commaInt(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return sign + s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	return sign + strings.Join(parts, ",")
}

func fmtInt(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return commaInt(int(math.Round(*v)))
}

func fmtPercent(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.Itoa(int(math.Round(100*(*v)))) + "%"
}

func fmtFixed1(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*v, 'f', 1, 64)
}

func fmtFixed4(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return strconv.FormatFloat(*v, 'f', 4, 64)
}

func change(with, without *float64) string {
	if with == nil || without == nil || *without == 0 {
		return ""
	}
	pct := math.Round((100 * (*with - *without)) / *without)
	sign := ""
	if pct >= 0 {
		sign = "+"
	}
	return fmt.Sprintf(" (%s%.0f%%)", sign, pct)
}

type metric struct {
	label      string
	measure    func([]RunRecord) *float64
	format     func(*float64) string
	comparable bool
}

func measured(r RunRecord) bool {
	return r.MeterError == "" && r.Metered == r.Requests
}

func measuredRuns(g []RunRecord) bool {
	for _, r := range g {
		if !measured(r) {
			return false
		}
	}
	return true
}

// Summarize compares routing-on and routing-off runs. Contaminated runs are named and left out of every statistic.
func Summarize(all []RunRecord, prices *Prices) string {
	var runs []RunRecord
	var excluded []RunRecord
	for _, run := range all {
		if run.Isolation != nil && run.Isolation.Contaminated {
			excluded = append(excluded, run)
			continue
		}
		runs = append(runs, run)
	}
	seen := map[string]bool{}
	var tasks []string
	for _, run := range runs {
		if !seen[run.Task] {
			seen[run.Task] = true
			tasks = append(tasks, run.Task)
		}
	}
	metrics := []metric{
		{"Runs", func(g []RunRecord) *float64 { v := float64(len(g)); return &v }, fmtInt, false},
		{"Usage complete runs", func(g []RunRecord) *float64 {
			n := 0
			for _, r := range g {
				if _, ok := taskTokenTotal(r); ok {
					n++
				}
			}
			v := float64(n)
			return &v
		}, fmtInt, false},
		{"Solved (every check passed)", func(g []RunRecord) *float64 {
			n := 0
			for _, run := range g {
				if run.Solved {
					n++
				}
			}
			v := float64(n) / float64(len(g))
			return &v
		}, fmtPercent, false},
		{"Checks passed, median", func(g []RunRecord) *float64 {
			vals := make([]float64, len(g))
			for i, run := range g {
				vals[i] = run.Score
			}
			return median(vals)
		}, fmtPercent, true},
		{"LLM requests, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.Requests) }))
		}, fmtInt, true},
		{"Input tokens incl. cache, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(totalInput(r)) }))
		}, fmtInt, true},
		{"…of which cached", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(inputShare(g, func(r RunRecord) int { return r.Cached }))
		}, fmtPercent, false},
		{"…uncached, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(uncachedInput(r)) }))
		}, fmtInt, true},
		{"…of which cache writes", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(inputShare(g, cacheWrite))
		}, fmtPercent, false},
		{"Output tokens, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.Output) }))
		}, fmtInt, true},
		{"…of which reasoning", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.Reasoning) }))
		}, fmtInt, false},
		{"Total tokens incl. Jev, median", func(g []RunRecord) *float64 {
			var totals []float64
			for _, r := range g {
				total, ok := taskTokenTotal(r)
				if !ok {
					return nil
				}
				totals = append(totals, float64(total))
			}
			return median(totals)
		}, fmtInt, true},
		{"Wall-clock seconds, median", func(g []RunRecord) *float64 {
			return median(floatField(g, func(r RunRecord) float64 { return r.Seconds }))
		}, fmtFixed1, true},
		{"Jev calls, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.JevCalls) }))
		}, fmtInt, true},
		{"Requests Jev steered", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			vals := make([]float64, 0, len(g))
			for _, run := range g {
				if run.Requests == 0 {
					continue
				}
				pass := 0
				if run.Modes != nil {
					pass = run.Modes["passthrough"]
				}
				vals = append(vals, 1-float64(pass)/float64(run.Requests))
			}
			return median(vals)
		}, fmtPercent, false},
		{"Requests compacted, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.CompactRequests) }))
		}, fmtInt, false},
		{"Tool entries dropped or truncated, median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.CompactDropped) }))
		}, fmtInt, false},
		{"Input tokens saved by compaction (est.), median", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			return median(floatField(g, func(r RunRecord) float64 { return float64(r.CompactSavedTokens) }))
		}, fmtInt, false},
		{"Failed LLM requests, total", func(g []RunRecord) *float64 {
			if !measuredRuns(g) {
				return nil
			}
			n := 0
			for _, run := range g {
				n += run.FailedRequests
			}
			v := float64(n)
			return &v
		}, fmtInt, false},
		{"Agent timeouts", func(g []RunRecord) *float64 {
			n := 0
			for _, run := range g {
				if run.TimedOut {
					n++
				}
			}
			v := float64(n)
			return &v
		}, fmtInt, false},
	}
	if prices != nil {
		p := *prices
		metrics = append(metrics,
			metric{"Cost per run, median (USD)", func(g []RunRecord) *float64 {
				if !measuredRuns(g) {
					return nil
				}
				vals := make([]float64, len(g))
				for i, run := range g {
					vals[i] = costOf(run, p)
				}
				return median(vals)
			}, fmtFixed4, true},
			metric{"Cost per solved task (USD)", func(g []RunRecord) *float64 {
				if !measuredRuns(g) {
					return nil
				}
				solved := 0
				total := 0.0
				for _, run := range g {
					total += costOf(run, p)
					if run.Solved {
						solved++
					}
				}
				if solved == 0 {
					return nil
				}
				v := total / float64(solved)
				return &v
			}, fmtFixed4, true},
		)
	}

	var lines []string
	var ignoredCacheWrite []string
	lines = append(lines, "# Benchmark summary", "")
	for _, task := range tasks {
		var with, without []RunRecord
		for _, run := range runs {
			if run.Task != task {
				continue
			}
			if run.Mode == "on" {
				with = append(with, run)
			} else if run.Mode == "off" {
				without = append(without, run)
			}
		}
		lines = append(lines, "## "+task, "", "| | Routing on | Routing off (baseline) |", "| --- | ---: | ---: |")
		var cacheWrites, reasoning bool
		for _, run := range append(with, without...) {
			spec := specOf(run.Agent)
			cacheWrites = cacheWrites || spec.hasCacheWrite
			reasoning = reasoning || spec.reportsReasoning
			if !spec.hasCacheWrite {
				ignoredCacheWrite = append(ignoredCacheWrite, run.Agent)
			}
		}
		for _, m := range metrics {
			if (m.label == "…of which cache writes" && !cacheWrites) || (m.label == "…of which reasoning" && !reasoning) {
				continue
			}
			var a, b *float64
			if len(with) > 0 {
				a = m.measure(with)
			}
			if len(without) > 0 {
				b = m.measure(without)
			}
			extra := ""
			if m.comparable {
				extra = change(a, b)
			}
			lines = append(lines, fmt.Sprintf("| %s | %s%s | %s |", m.label, m.format(a), extra, m.format(b)))
		}
		lines = append(lines, "")
	}
	if prices != nil && prices.CacheWriteGiven && len(ignoredCacheWrite) > 0 {
		sort.Strings(ignoredCacheWrite)
		lines = append(lines, "The cache-write price was ignored for "+strings.Join(slices.Compact(ignoredCacheWrite), ", ")+": those providers do not bill cache writes.", "")
	}
	if len(excluded) > 0 {
		names := make([]string, len(excluded))
		for i, run := range excluded {
			names[i] = fmt.Sprintf("%s.%s.%d", run.Task, run.Mode, run.Rep)
		}
		lines = append(lines, "Excluded as contaminated (the agent read files outside its sandbox that it had not created): "+strings.Join(names, ", ")+".", "")
	}
	smallest := math.MaxInt
	for _, task := range tasks {
		for _, mode := range []string{"on", "off"} {
			n := 0
			for _, run := range runs {
				if run.Task == task && run.Mode == mode {
					n++
				}
			}
			// A mode that was not run (e.g. --modes on) is not a small cell.
			if n > 0 && n < smallest {
				smallest = n
			}
		}
	}
	if smallest == math.MaxInt {
		smallest = 0
	}
	if smallest < 6 {
		plural := "s"
		if smallest == 1 {
			plural = ""
		}
		lines = append(lines, fmt.Sprintf("Only %d run%s per cell: agents vary a lot from one run to the next, so treat differences here as anecdotes, not measurements. An effect decision requires at least six comparable pairs.", smallest, plural))
	} else {
		lines = append(lines, "Percentages in brackets compare the routing-on median with the baseline median.")
	}
	return strings.Join(lines, "\n") + "\n"
}

func floatField(g []RunRecord, pick func(RunRecord) float64) []float64 {
	out := make([]float64, len(g))
	for i, run := range g {
		out[i] = pick(run)
	}
	return out
}

// inputShare is pick(r)/totalInput(r) per run, skipping runs with no input.
func inputShare(g []RunRecord, pick func(RunRecord) int) []float64 {
	out := make([]float64, 0, len(g))
	for _, run := range g {
		if total := totalInput(run); total > 0 {
			out = append(out, float64(pick(run))/float64(total))
		}
	}
	return out
}
