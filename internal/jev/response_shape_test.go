package jev

import (
	"encoding/json"
	"testing"
)

func TestResponseRequiredNumericFields(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"type":"noul","noul":0}`, true},
		{`{"type":"noul","noul":0.95}`, true},
		{`{"type":"noul"}`, false},
		{`{"type":"noul","noul":null}`, false},
		{`{"type":"noul","noul":"0"}`, false},
		{`{"type":"noul","noul":-1}`, false},
	} {
		res := &Response{Answers: map[string]json.RawMessage{"q": json.RawMessage(tc.body)}}
		if _, ok := ParseNoul(res, "q"); ok != tc.valid {
			t.Errorf("%s: valid=%v", tc.body, ok)
		}
	}
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"type":"choice","choice":"exec","confidence":0}`, true},
		{`{"type":"choice","choice":"exec","confidence":0.9}`, true},
		{`{"type":"choice","choice":"exec"}`, false},
		{`{"type":"choice","choice":"exec","confidence":null}`, false},
	} {
		res := &Response{Answers: map[string]json.RawMessage{"q": json.RawMessage(tc.body)}}
		if _, ok := ParseChoice(res, "q"); ok != tc.valid {
			t.Errorf("%s: valid=%v", tc.body, ok)
		}
	}
}
