package main

import (
	"net"
	"testing"
	"time"
)

func TestHTTPServerReadTimeout(t *testing.T) {
	if got, want := newHTTPServer(nil).ReadTimeout, 30*time.Second; got != want {
		t.Fatalf("ReadTimeout = %s, want %s", got, want)
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
