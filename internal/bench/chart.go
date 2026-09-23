package bench

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type theme struct {
	name    string
	surface string
	ink     string
	ink2    string
	muted   string
	grid    string
	axis    string
	on      string
	off     string
}

var themes = []theme{
	{"light", "#fcfcfb", "#0b0b0b", "#52514e", "#898781", "#e1e0d9", "#c3c2b7", "#2a78d6", "#eb6834"},
	{"dark", "#1a1a19", "#ffffff", "#c3c2b7", "#898781", "#2c2c2a", "#383835", "#3987e5", "#d95926"},
}

var modelNames = map[string]string{
	"gpt-6-astra": "GPT-6 Astra", "gpt-5.6-sol": "GPT-5.6 Sol", "gpt-5.6-luna": "GPT-5.6 Luna", "gpt-5.6-terra": "GPT-5.6 Terra",
	"claude-fable-5-1": "Fable 5.1", "claude-opus-5": "Opus 5", "claude-sonnet-5": "Sonnet 5",
}

func modelOf(run RunRecord) string {
	if run.AgentModel != "" {
		return run.AgentModel
	}
	for _, model := range run.Models {
		if !strings.Contains(model, "review") {
			return model
		}
	}
	return run.Agent
}

func groupLabel(run RunRecord) string {
	name := modelOf(run)
	if pretty, ok := modelNames[name]; ok {
		name = pretty
	}
	return name + " · " + strings.TrimPrefix(run.Task, "chess-")
}

func themeColor(t theme, mode string) string {
	if mode == "on" {
		return t.on
	}
	return t.off
}

type panel struct {
	title   string
	abs     bool
	measure func(RunRecord) float64
	format  func(float64) string
}

func panels() []panel {
	return []panel{
		{"Input tokens per run", false, func(r RunRecord) float64 { return float64(r.Input) }, func(v float64) string {
			if v >= 1e6 {
				return strconv.FormatFloat(v/1e6, 'f', 2, 64) + "M"
			}
			return strconv.Itoa(int(math.Round(v/1e3))) + "k"
		}},
		{"Output tokens per run", false, func(r RunRecord) float64 { return float64(r.Output) }, func(v float64) string {
			return commaInt(int(math.Round(v)))
		}},
		{"LLM requests per run", false, func(r RunRecord) float64 { return float64(r.Requests) }, func(v float64) string {
			if v == math.Trunc(v) {
				return strconv.Itoa(int(v))
			}
			return strconv.FormatFloat(v, 'f', 1, 64)
		}},
		{"Wall-clock seconds per run", false, func(r RunRecord) float64 { return r.Seconds }, func(v float64) string {
			return strconv.Itoa(int(math.Round(v))) + " s"
		}},
		{"Hidden checks passed", true, func(r RunRecord) float64 { return 100 * r.Score }, func(v float64) string {
			return strconv.Itoa(int(math.Round(v))) + "%"
		}},
	}
}

func niceCeiling(value float64) float64 {
	if value <= 0 {
		value = 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(value)))
	for _, step := range []float64{1, 2, 2.5, 5, 10} {
		if step*mag+1e-9 >= value {
			return step * mag
		}
	}
	return 10 * mag
}

func medianOr(values []float64, fallback float64) float64 {
	m := median(values)
	if m == nil || *m == 0 {
		return fallback
	}
	return *m
}

func xml(s string) string {
	s = strings.ReplaceAll(s, "&", "&")
	s = strings.ReplaceAll(s, "<", "<")
	return s
}

func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// Chart writes comparison-light.svg and comparison-dark.svg. Contaminated runs are omitted.
func Chart(runs []RunRecord, outDir string) error {
	var clean []RunRecord
	for _, run := range runs {
		if run.Isolation != nil && run.Isolation.Contaminated {
			continue
		}
		clean = append(clean, run)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, t := range themes {
		body := draw(clean, t)
		path := filepath.Join(outDir, "comparison-"+t.name+".svg")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
		fmt.Println(path)
	}
	return nil
}

func draw(runs []RunRecord, t theme) string {
	taskOrder := map[string]int{}
	var tasks []string
	for _, run := range runs {
		if _, ok := taskOrder[run.Task]; !ok {
			taskOrder[run.Task] = len(tasks)
			tasks = append(tasks, run.Task)
		}
	}
	sorted := append([]RunRecord(nil), runs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return taskOrder[sorted[i].Task] < taskOrder[sorted[j].Task]
	})
	var groups []string
	seen := map[string]bool{}
	for _, run := range sorted {
		label := groupLabel(run)
		if !seen[label] {
			seen[label] = true
			groups = append(groups, label)
		}
	}
	const (
		W, PAD, COLS, GAP = 1200.0, 32.0, 2.0, 40.0
		labelW, valueW    = 150.0, 96.0
		rowH, groupGap    = 16.0, 14.0
		header            = 124.0
	)
	ps := panels()
	panelW := (W - 2*PAD - (COLS-1)*GAP) / COLS
	plotW := panelW - labelW - valueW
	panelH := 34 + float64(len(groups))*(2*rowH+groupGap) + 22
	if alt := 60 + float64(len(groups))*38; alt > panelH {
		panelH = alt
	}
	rows := math.Ceil(float64(len(ps)+1) / COLS)
	H := header + rows*(panelH+28) + 40
	var out []string
	text := func(x, y float64, content string, size int, fill, anchor string, weight int) {
		out = append(out, fmt.Sprintf(`<text x="%s" y="%s" font-size="%d" fill="%s" text-anchor="%s" font-weight="%d">%s</text>`, num(x), num(y), size, fill, anchor, weight, xml(content)))
	}
	out = append(out, fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %s %s" width="%s" height="%s" font-family="system-ui, -apple-system, 'Segoe UI', Helvetica, Arial, sans-serif" role="img" aria-label="Benchmark results with Jev routing on and off">`, num(W), num(H), num(W), num(H)))
	out = append(out, fmt.Sprintf(`<rect width="%s" height="%s" fill="%s"/>`, num(W), num(H), t.surface))
	text(PAD, 40, "Same task, same model: Jev routing on vs. off", 20, t.ink, "start", 650)
	perCell := math.MaxInt
	for _, group := range groups {
		for _, mode := range []string{"on", "off"} {
			n := 0
			for _, run := range runs {
				if groupLabel(run) == group && run.Mode == mode {
					n++
				}
			}
			if n < perCell {
				perCell = n
			}
		}
	}
	if len(groups) == 0 {
		perCell = 0
	}
	cellNote := strconv.Itoa(perCell) + " to 5"
	if perCell == 5 {
		cellNote = "5"
	}
	text(PAD, 62, "Bars are medians, dots are the individual runs ("+cellNote+" per mode), drawn relative to each pair's baseline median.", 13, t.ink2, "start", 400)
	text(PAD, 80, "Lower is better, except for checks passed. GPT models ran in Codex, Claude models in Claude Code, both without MCP servers or plugins.", 13, t.ink2, "start", 400)
	legendX := PAD
	for _, mode := range []struct{ id, label string }{{"on", "Jev routing on"}, {"off", "Routing off (baseline)"}} {
		out = append(out, fmt.Sprintf(`<rect x="%s" y="94" width="14" height="8" rx="4" fill="%s"/>`, num(legendX), themeColor(t, mode.id)))
		text(legendX+20, 102, mode.label, 12, t.ink2, "start", 400)
		legendX += 40 + float64(len(mode.label))*6.6
	}
	for i, p := range ps {
		x0 := PAD + float64(i%int(COLS))*(panelW+GAP)
		y0 := header + float64(i/int(COLS))*(panelH+28)
		baseline := func(group string) float64 {
			var vals []float64
			for _, run := range runs {
				if groupLabel(run) == group && run.Mode == "off" {
					vals = append(vals, p.measure(run))
				}
			}
			return medianOr(vals, 1)
		}
		relative := func(run RunRecord) float64 {
			if p.abs {
				return p.measure(run)
			}
			return (100 * p.measure(run)) / baseline(groupLabel(run))
		}
		maxRel := 0.0
		for _, run := range runs {
			if r := relative(run); r > maxRel {
				maxRel = r
			}
		}
		max := 100.0
		if !p.abs {
			max = math.Min(250, math.Max(150, niceCeiling(maxRel)))
		}
		scale := func(value float64) float64 { return x0 + labelW + (plotW*value)/max }
		text(x0, y0+14, p.title, 14, t.ink, "start", 600)
		top := y0 + 34
		bottom := top + float64(len(groups))*(2*rowH+groupGap) - groupGap
		step := 50.0
		if p.abs {
			step = 25
		}
		for value := 0.0; value <= max+1e-6; value += step {
			x := scale(value)
			reference := value == 0 || (!p.abs && value == 100)
			stroke := t.grid
			if reference {
				stroke = t.axis
			}
			out = append(out, fmt.Sprintf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1"/>`, num(x), num(top-4), num(x), num(bottom+4), stroke))
			label := strconv.Itoa(int(value)) + "%"
			if !p.abs && value == 100 {
				label = "baseline"
			}
			text(x, bottom+18, label, 11, t.muted, "middle", 400)
		}
		for gi, group := range groups {
			gy := top + float64(gi)*(2*rowH+groupGap)
			text(x0+labelW-12, gy+rowH+4, group, 12, t.ink2, "end", 400)
			medians := map[string]float64{}
			for mi, mode := range []string{"on", "off"} {
				var members []RunRecord
				for _, run := range runs {
					if groupLabel(run) == group && run.Mode == mode {
						members = append(members, run)
					}
				}
				if len(members) == 0 {
					continue
				}
				cell := make([]float64, len(members))
				raw := make([]float64, len(members))
				for i, run := range members {
					cell[i] = relative(run)
					raw[i] = p.measure(run)
				}
				cy := gy + float64(mi)*rowH + rowH/2
				med := medianOr(raw, 0)
				medians[mode] = med
				barEnd := math.Max(scale(medianOr(cell, 0)), scale(0)+4)
				out = append(out, fmt.Sprintf(`<path d="M%s,%s H%s a4,4 0 0 1 0,8 H%s Z" fill="%s" opacity="0.55"/>`, num(scale(0)), num(cy-4), num(barEnd-4), num(scale(0)), themeColor(t, mode)))
				for i, run := range members {
					if cell[i] <= max {
						out = append(out, fmt.Sprintf(`<circle cx="%s" cy="%s" r="4" fill="%s" stroke="%s" stroke-width="2"/>`, num(scale(cell[i])), num(cy), themeColor(t, mode), t.surface))
						continue
					}
					out = append(out, fmt.Sprintf(`<circle cx="%s" cy="%s" r="3.5" fill="%s" stroke="%s" stroke-width="2"/>`, num(scale(max)), num(cy), t.surface, themeColor(t, mode)))
					text(scale(max)-8, cy+4, p.format(p.measure(run))+" →", 10, t.ink2, "end", 400)
					_ = run
				}
			}
			for mi, mode := range []string{"on", "off"} {
				med, ok := medians[mode]
				if !ok {
					continue
				}
				label := p.format(med)
				if mode == "on" {
					if off, has := medians["off"]; has && off != 0 {
						delta := int(math.Round((100 * (med - off)) / off))
						if delta != 0 {
							sign := "+"
							if delta < 0 {
								sign = ""
							}
							label += fmt.Sprintf("  %s%d%%", sign, delta)
						}
					}
				}
				weight := 400
				fill := t.ink2
				if mode == "on" {
					weight = 600
					fill = t.ink
				}
				text(x0+labelW+plotW+10, gy+float64(mi)*rowH+rowH/2+4, label, 11, fill, "start", weight)
			}
		}
	}
	x0 := PAD + float64(len(ps)%int(COLS))*(panelW+GAP)
	y0 := header + float64(len(ps)/int(COLS))*(panelH+28)
	text(x0, y0+14, "Solved runs and what Jev itself cost", 14, t.ink, "start", 600)
	for i, group := range groups {
		y := y0 + 44 + float64(i)*38
		cell := func(mode string) []RunRecord {
			var out []RunRecord
			for _, run := range runs {
				if groupLabel(run) == group && run.Mode == mode {
					out = append(out, run)
				}
			}
			return out
		}
		solved := func(mode string) string {
			c := cell(mode)
			n := 0
			for _, run := range c {
				if run.Solved {
					n++
				}
			}
			return fmt.Sprintf("%d/%d", n, len(c))
		}
		on := cell("on")
		jevTokens := 0
		steered := 0
		requests := 0
		for _, run := range on {
			jevTokens += run.JevInput
			pass := 0
			if run.Modes != nil {
				pass = run.Modes["passthrough"]
			}
			steered += run.Requests - pass
			requests += run.Requests
		}
		text(x0, y, group, 12, t.ink, "start", 600)
		text(x0, y+16, fmt.Sprintf("Solved %s with routing, %s without. Jev steered %d of %d requests for $%.3f.", solved("on"), solved("off"), steered, requests, float64(jevTokens)*0.042/1e6), 12, t.ink2, "start", 400)
	}
	out = append(out, "</svg>")
	return strings.Join(out, "\n")
}
