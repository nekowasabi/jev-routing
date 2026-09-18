package main

import (
	"net"
	"testing"
)

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
