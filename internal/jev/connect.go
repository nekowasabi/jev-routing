package jev

import (
	"fmt"
	"strings"
)

const (
	StatusLive             = "live"
	StatusKeyMissing       = "key_missing"
	StatusDisabledByConfig = "disabled_by_config"
	StatusCallFailed       = "call_failed"
	StatusAbstained        = "abstained"
)

const selectionJev = "jev"
const selectionLocal = "local"

// ResolveConnect classifies why Jev was or was not used for one decision.
// selectionMode is the process option (local|jev|hybrid). asked is true only
// when a live Ask was attempted. callErr is the Ask error, if any.
func ResolveConnect(selectionMode string, c *Client, asked bool, callErr error) string {
	if strings.TrimSpace(selectionMode) == selectionLocal {
		return StatusDisabledByConfig
	}
	if c == nil || !c.Live() {
		return StatusKeyMissing
	}
	if callErr != nil {
		return StatusCallFailed
	}
	if !asked {
		return StatusAbstained
	}
	return StatusLive
}

// StartupStatus is the one-line process connect state. The API key value is
// never included. Jev-only selection treats a missing key as a startup error.
func StartupStatus(c *Client, selectionMode string) (string, error) {
	mode := strings.TrimSpace(selectionMode)
	if mode == "" {
		mode = "hybrid"
	}
	status := ResolveConnect(mode, c, false, nil)
	if mode != selectionLocal && c != nil && c.Live() {
		status = StatusLive
	}
	line := fmt.Sprintf("jev-routing: jev=%s selection=%s", status, mode)
	if status == StatusKeyMissing && mode == selectionJev {
		return line, fmt.Errorf("%s (required)", line)
	}
	if status != StatusLive {
		line += " (degraded)"
	}
	return line, nil
}
