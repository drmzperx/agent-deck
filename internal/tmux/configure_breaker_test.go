package tmux

import (
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
