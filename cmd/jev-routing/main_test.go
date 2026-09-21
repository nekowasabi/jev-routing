package main

import (
	"net"
	"testing"

	"github.com/nekowasabi/jev-routing/internal/host"
)

func TestHTTPServerOmitsReadTimeout(t *testing.T) {
	if got := newHTTPServer(nil).ReadTimeout; got != 0 {
		t.Fatalf("ReadTimeout = %s, want 0 (unlimited body for bidi streams)", got)
	}
}

func TestListenForRunFallsBackWhenDefaultPortIsBusy(t *testing.T) {
	t.Setenv("JEV_LISTEN", "")
	busy, err := net.Listen("tcp", "127.0.0.1:8787")
	if err == nil {
		defer busy.Close()
	}

	ln, err := listenForRun()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln.Addr().String() == "127.0.0.1:8787" {
		t.Fatal("expected an available port when 8787 is busy")
	}
}

func TestServeRejectsMissingRequiredJevKey(t *testing.T) {
	t.Setenv("JEV_SELECTION_MODE", "jev")
	t.Setenv("JEV_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	if code := serve(host.Grok, "127.0.0.1:0"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
