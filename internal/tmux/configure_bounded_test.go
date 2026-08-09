package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// fakeWedgedTmux installs a `tmux` on PATH that never exits, simulating a
// tmux 3.0a client that has exhausted its fd table. The factory in socket.go
// resolves "tmux" via PATH, so this intercepts every tmux spawn.
//
// The body is a loop, not a bare `sleep`: sh exec-optimizes a single-command
// script into the command itself, which would change the process identity
// (see CLAUDE.md § Testing notes).
func fakeWedgedTmux(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "tmux")
	body := "#!/bin/sh\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// In the RED phase the call under test is still unbounded, so its child is
	// never SIGKILLed and outlives the test binary. Reap it explicitly —
	// otherwise each red run leaves a spinning `sh` behind, which is the very
	// thing this whole plan exists to stop.
	t.Cleanup(func() { _ = exec.Command("pkill", "-f", script).Run() })
}

func TestConfigureTerminalTitle_ReturnsWhenTmuxWedges(t *testing.T) {
	fakeWedgedTmux(t)

	s := &Session{Name: "agentdeck_test", DisplayName: "test", SocketName: "test-socket"}

	done := make(chan struct{})
	start := time.Now()
	go func() {
		s.ConfigureTerminalTitle()
		close(done)
	}()

	// tmuxPollTimeout (3s) + tmuxSubprocessWaitDelay (2s) + slack.
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed < tmuxPollTimeout {
			t.Fatalf("returned in %v, before the %v deadline — deadline not exercised", elapsed, tmuxPollTimeout)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("ConfigureTerminalTitle did not return: still unbounded")
	}
}

func TestEnableMouseModeAndEnhancements_ReturnWhenTmuxWedges(t *testing.T) {
	fakeWedgedTmux(t)

	s := &Session{Name: "agentdeck_test", DisplayName: "test", SocketName: "test-socket"}
	// Struct literal leaves s.mouse false, its zero value — but production
	// default is true (see SetMouse). Without this, the `if s.mouse` guard in
	// EnableMouseMode skips the `mouse on` runBoundedMutation entirely and the
	// test only ever exercises the enhanceArgs poll batch, silently failing to
	// cover the mutation path.
	s.SetMouse(true)

	done := make(chan struct{})
	start := time.Now()
	go func() {
		_ = s.EnableMouseMode()
		close(done)
	}()

	// tmuxMutationTimeout (5s) for `mouse on`, plus tmuxPollTimeout (3s) for
	// the enhancement batch, plus slack.
	select {
	case <-done:
		if elapsed := time.Since(start); elapsed < tmuxPollTimeout {
			t.Fatalf("returned in %v, before the %v deadline — EnableMouseMode became a no-op", elapsed, tmuxPollTimeout)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("EnableMouseMode did not return: an enhancement batch is still unbounded")
	}
}

// A wedged configure must not block a concurrent reader indefinitely. Before
// the deadline fix, EnsureConfigured held s.mu across an unbounded tmux client
// that never exited, so IsConfigured and the attach path blocked forever.
func TestEnsureConfigured_DoesNotBlockReadersForever(t *testing.T) {
	fakeWedgedTmux(t)

	s := &Session{Name: "agentdeck_test", DisplayName: "test", SocketName: "mutex-test-sock"}
	resetConfigureBreaker(s.SocketName)

	go s.EnsureConfigured()
	time.Sleep(200 * time.Millisecond) // let it take s.mu

	done := make(chan struct{})
	go func() {
		_ = s.IsConfigured()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("IsConfigured blocked behind a wedged EnsureConfigured")
	}
}
