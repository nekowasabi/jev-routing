package jev

import "testing"

func TestResolveConnect(t *testing.T) {
	live := &Client{APIKey: "k"}
	cases := []struct {
		mode  string
		c     *Client
		asked bool
		err   error
		want  string
	}{
		{"local", live, false, nil, StatusDisabledByConfig},
		{"local", nil, false, nil, StatusDisabledByConfig},
		{"hybrid", nil, false, nil, StatusKeyMissing},
		{"hybrid", &Client{}, false, nil, StatusKeyMissing},
		{"jev", nil, true, nil, StatusKeyMissing},
		{"hybrid", live, true, errForTest(), StatusCallFailed},
		{"hybrid", live, false, nil, StatusAbstained},
		{"hybrid", live, true, nil, StatusLive},
		{"jev", live, true, nil, StatusLive},
	}
	for _, tc := range cases {
		got := ResolveConnect(tc.mode, tc.c, tc.asked, tc.err)
		if got != tc.want {
			t.Fatalf("mode=%s live=%v asked=%v err=%v got=%s want=%s", tc.mode, tc.c != nil && tc.c.Live(), tc.asked, tc.err != nil, got, tc.want)
		}
	}
}

func TestStartupStatus(t *testing.T) {
	line, err := StartupStatus(nil, "hybrid")
	if err != nil || line != "jev-routing: jev=key_missing selection=hybrid (degraded)" {
		t.Fatalf("optional missing: %q %v", line, err)
	}
	line, err = StartupStatus(nil, "jev")
	if err == nil || line != "jev-routing: jev=key_missing selection=jev" {
		t.Fatalf("required missing: %q %v", line, err)
	}
	line, err = StartupStatus(&Client{APIKey: "k"}, "hybrid")
	if err != nil || line != "jev-routing: jev=live selection=hybrid" {
		t.Fatalf("live: %q %v", line, err)
	}
	line, err = StartupStatus(&Client{APIKey: "k"}, "local")
	if err != nil || line != "jev-routing: jev=disabled_by_config selection=local (degraded)" {
		t.Fatalf("disabled: %q %v", line, err)
	}
	if line, _ = StartupStatus(&Client{APIKey: "secret-value"}, "hybrid"); containsSecret(line, "secret-value") {
		t.Fatalf("key leaked: %q", line)
	}
}

type testErr string

func (e testErr) Error() string { return string(e) }

func errForTest() error { return testErr("boom") }

func containsSecret(s, secret string) bool {
	return len(secret) > 0 && (s == secret || (len(s) >= len(secret) && (func() bool {
		for i := 0; i+len(secret) <= len(s); i++ {
			if s[i:i+len(secret)] == secret {
				return true
			}
		}
		return false
	})()))
}
