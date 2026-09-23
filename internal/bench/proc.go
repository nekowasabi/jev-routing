package bench

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type procResult struct {
	ExitCode int
	TimedOut bool
	Seconds  float64
}

// runProc runs a process until it exits, the timeout fires, or ctx is canceled.
// The timeout and cancel kill the process group so agent children do not linger.
func runProc(ctx context.Context, file string, args []string, dir string, env []string, logFile string, timeout time.Duration, onLine func(string)) procResult {
	started := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.Command(file, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdin = nil
	setProcessGroup(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return procResult{ExitCode: 127, Seconds: time.Since(started).Seconds()}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return procResult{ExitCode: 127, Seconds: time.Since(started).Seconds()}
	}
	var log io.Writer
	if logFile != "" {
		f, ferr := os.Create(logFile)
		if ferr == nil {
			defer f.Close()
			log = &lockWriter{w: f}
		}
	}
	if err := cmd.Start(); err != nil {
		return procResult{ExitCode: 127, Seconds: time.Since(started).Seconds()}
	}
	doneCopy := make(chan struct{})
	go func() {
		defer close(doneCopy)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			scanLines(stdout, log, onLine)
		}()
		go func() {
			defer wg.Done()
			if log != nil {
				_, _ = io.Copy(log, stderr)
			} else {
				_, _ = io.Copy(io.Discard, stderr)
			}
		}()
		wg.Wait()
	}()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timedOut := false
	var waitErr error
	select {
	case waitErr = <-wait:
	case <-timer.C:
		timedOut = true
		killProcessGroup(cmd.Process.Pid)
		waitErr = <-wait
	case <-ctx.Done():
		timedOut = true
		killProcessGroup(cmd.Process.Pid)
		waitErr = <-wait
	}
	<-doneCopy
	code := 0
	if waitErr != nil {
		code = 1
		if ee, ok := waitErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return procResult{ExitCode: code, TimedOut: timedOut, Seconds: time.Since(started).Seconds()}
}

type lockWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func scanLines(r io.Reader, log io.Writer, onLine func(string)) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if log != nil {
			_, _ = io.WriteString(log, line+"\n")
		}
		if onLine != nil {
			onLine(line)
		}
	}
}
