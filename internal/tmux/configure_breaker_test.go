package tmux

import (
	"errors"
	"testing"
	"time"
)

func TestBreaker_TripsAfterThresholdAndRecovers(t *testing.T) {
	const sock = "test-breaker-sock"
	resetConfigureBreaker(sock)

	if !configureAllowed(sock) {
		t.Fatal("breaker should start closed (allowing)")
	}

	for i := 0; i < configureBreakerThreshold; i++ {
		recordConfigureResult(sock, true)
	}
	if configureAllowed(sock) {
		t.Fatalf("breaker should trip after %d consecutive timeouts", configureBreakerThreshold)
	}

	// A success resets the counter and closes the breaker.
	resetConfigureBreaker(sock)
	recordConfigureResult(sock, true)
	recordConfigureResult(sock, false)
	for i := 0; i < configureBreakerThreshold-1; i++ {
		recordConfigureResult(sock, true)
	}
	if !configureAllowed(sock) {
		t.Fatal("a success must reset the consecutive-timeout count")
	}
}

func TestBreaker_IsolatedPerSocket(t *testing.T) {
	resetConfigureBreaker("sock-a")
	resetConfigureBreaker("sock-b")

	for i := 0; i < configureBreakerThreshold; i++ {
		recordConfigureResult("sock-a", true)
	}
	if configureAllowed("sock-a") {
		t.Fatal("sock-a should be tripped")
	}
	if !configureAllowed("sock-b") {
		t.Fatal("sock-b must be unaffected by sock-a")
	}
}

// TestRunBoundedConfigure_FeedsBreakerOnDeadline drives the real
// runBoundedConfigure seam against a wedged tmux, rather than calling
// recordConfigureResult directly like the other breaker tests. Every other
// test in this file would pass unchanged against the pre-fix
// annotateDeadline(ctx.Err(), tmuxExecContext(...).Run()) defect, because
// ctx.Err() is evaluated before Run() blocks and is therefore always nil —
// the breaker is never fed and this is the only test that would have caught
// it.
//
// Budget: each call burns a full tmuxPollTimeout (3s); configureBreakerThreshold
// calls happen here.
func TestRunBoundedConfigure_FeedsBreakerOnDeadline(t *testing.T) {
	fakeWedgedTmux(t)

	const sock = "test-runboundedconfigure-sock"
	resetConfigureBreaker(sock)

	s := &Session{Name: "agentdeck_test", DisplayName: "test", SocketName: sock}

	if got := configureTimeoutCount(sock); got != 0 {
		t.Fatalf("configureTimeoutCount = %d before any call, want 0", got)
	}

	err := s.runBoundedConfigure("set-option", "-t", s.Name, "foo", "bar")
	if !errors.Is(err, errTmuxTimeout) {
		t.Fatalf("runBoundedConfigure err = %v, want errTmuxTimeout", err)
	}
	if got := configureTimeoutCount(sock); got != 1 {
		t.Fatalf("configureTimeoutCount = %d after one wedged call, want 1 — the breaker was not fed", got)
	}

	// Drive it to the threshold and confirm the breaker actually trips.
	for i := 1; i < configureBreakerThreshold; i++ {
		if err := s.runBoundedConfigure("set-option", "-t", s.Name, "foo", "bar"); !errors.Is(err, errTmuxTimeout) {
			t.Fatalf("runBoundedConfigure err = %v, want errTmuxTimeout", err)
		}
	}
	if got := configureTimeoutCount(sock); got != configureBreakerThreshold {
		t.Fatalf("configureTimeoutCount = %d after %d calls, want %d", got, configureBreakerThreshold, configureBreakerThreshold)
	}
	if configureAllowed(sock) {
		t.Fatalf("breaker should be tripped after %d consecutive timeouts", configureBreakerThreshold)
	}
}

func TestBreaker_ReclosesAfterCooldown(t *testing.T) {
	const sock = "test-cooldown-sock"
	resetConfigureBreaker(sock)
	for i := 0; i < configureBreakerThreshold; i++ {
		recordConfigureResult(sock, true)
	}
	if configureAllowed(sock) {
		t.Fatal("should be tripped")
	}
	// Backdate the trip past the cooldown.
	backdateConfigureBreaker(sock, configureBreakerCooldown+time.Second)
	if !configureAllowed(sock) {
		t.Fatal("breaker should reclose after the cooldown elapses")
	}
}
