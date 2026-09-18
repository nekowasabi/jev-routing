package host

import (
	"reflect"
	"testing"
)

func TestChildArgs(t *testing.T) {
	listen := "127.0.0.1:45678"
	want := []string{
		"--config", `model_provider="jev"`,
		"--config", `model_providers.jev.name="jev-routing"`,
		"--config", `model_providers.jev.base_url="http://127.0.0.1:45678/v1"`,
		"--config", `model_providers.jev.wire_api="responses"`,
		"--config", `model_providers.jev.requires_openai_auth=true`,
	}
	if got := ChildArgs(Codex, listen); !reflect.DeepEqual(got, want) {
		t.Fatalf("ChildArgs(Codex) = %#v, want %#v", got, want)
	}
	if got := ChildArgs(Claude, listen); got != nil {
		t.Fatalf("ChildArgs(Claude) = %#v, want nil", got)
	}
}
