package plan

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const modelLogLimit = 200

type ModelDecision struct {
	TS              time.Time `json:"ts"`
	Host            string    `json:"host"`
	Role            string    `json:"role,omitempty"`
	Source          string    `json:"source"`
	ReasonCode      string    `json:"reason_code"`
	PairID          string    `json:"pair_id,omitempty"`
	RequestedModel  string    `json:"requested_model,omitempty"`
	AppliedModel    string    `json:"applied_model"`
	RequestedEffort string    `json:"requested_effort,omitempty"`
	AppliedEffort   string    `json:"applied_effort"`
	Asked           bool      `json:"asked"`
}

func ModelLogPath() string {
	if p := strings.TrimSpace(os.Getenv("JEV_MODEL_LOG")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "jev-routing", "model-routes.jsonl")
}

func RecordModelDecision(d ModelDecision) {
	path := ModelLogPath()
	if path == "" {
		return
	}
	if d.TS.IsZero() {
		d.TS = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	_ = enc.Encode(d)
}

func LoadModelDecisions() []ModelDecision {
	path := ModelLogPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []ModelDecision
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var d ModelDecision
		if json.Unmarshal([]byte(line), &d) != nil {
			continue
		}
		all = append(all, d)
	}
	if len(all) > modelLogLimit {
		all = all[len(all)-modelLogLimit:]
	}
	return all
}
