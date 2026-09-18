package host

import (
	"reflect"
	"testing"
)

func TestChildArgs(t *testing.T) {
	listen := "127.0.0.1:45678"
	want := []string{
		"--config", `model_provider="openai"`,
		"--config", `openai_base_url="http://127.0.0.1:45678/v1"`,
	}
	if got := ChildArgs(Codex, listen); !reflect.DeepEqual(got, want) {
		t.Fatalf("ChildArgs(Codex) = %#v, want %#v", got, want)
	}
	if got := ChildArgs(Claude, listen); got != nil {
		t.Fatalf("ChildArgs(Claude) = %#v, want nil", got)
	}
}
