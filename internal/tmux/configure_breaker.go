package tmux

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// The deferred-configuration path (EnsureConfigured) issues up to three tmux
// commands per session — status bar, terminal title, mouse mode. Each is
// deadline-bounded, but on a wedged tmux 3.0a server every one of them burns
// its full deadline in kernel time (~11s total), and internal/ui/home.go
// starts one configure per status tick. Bounded-but-overlapping is still
// enough to saturate the machine, so stop trying once the server has proven
// itself wedged.
//
// Keyed by socket: a wedge is a property of the tmux SERVER, so a timeout on
// one session predicts a timeout on every other session sharing that socket.
const (
	configureBreakerThreshold = 3
	configureBreakerCooldown  = 60 * time.Second
)

type configureBreakerState struct {
	consecutiveTimeouts int
	trippedAt           time.Time
	totalTimeouts       uint64
}

var (
	configureBreakerMu sync.Mutex
	configureBreakers  = map[string]*configureBreakerState{}
)

func configureBreakerFor(socket string) *configureBreakerState {
	st, ok := configureBreakers[socket]
	if !ok {
		st = &configureBreakerState{}
		configureBreakers[socket] = st
	}
	return st
}

// configureAllowed reports whether deferred configuration may run against this
// socket right now.
func configureAllowed(socket string) bool {
	configureBreakerMu.Lock()
	defer configureBreakerMu.Unlock()

	st := configureBreakerFor(socket)
	if st.trippedAt.IsZero() {
		return true
	}
	if time.Since(st.trippedAt) >= configureBreakerCooldown {
		st.trippedAt = time.Time{}
		st.consecutiveTimeouts = 0
		return true
	}
	return false
}

// recordConfigureResult feeds one configure attempt's outcome to the breaker.
func recordConfigureResult(socket string, timedOut bool) {
	configureBreakerMu.Lock()
	defer configureBreakerMu.Unlock()

	st := configureBreakerFor(socket)
	if !timedOut {
		st.consecutiveTimeouts = 0
		st.trippedAt = time.Time{}
		return
	}
	st.totalTimeouts++
	st.consecutiveTimeouts++
	if st.consecutiveTimeouts >= configureBreakerThreshold && st.trippedAt.IsZero() {
		st.trippedAt = time.Now()
		statusLog.Warn("configure_breaker_tripped",
			slog.String("socket", socket),
			slog.Int("consecutive_timeouts", st.consecutiveTimeouts))
	}
}

// resetConfigureBreaker clears a socket's breaker. Test seam.
func resetConfigureBreaker(socket string) {
	configureBreakerMu.Lock()
	defer configureBreakerMu.Unlock()
	delete(configureBreakers, socket)
}

// backdateConfigureBreaker ages a trip by d. Test seam.
func backdateConfigureBreaker(socket string, d time.Duration) {
	configureBreakerMu.Lock()
	defer configureBreakerMu.Unlock()
	st := configureBreakerFor(socket)
	if !st.trippedAt.IsZero() {
		st.trippedAt = st.trippedAt.Add(-d)
	}
}

// configureTimeoutCount returns the socket's cumulative deadline count. Test
// seam: EnsureConfigured no longer consults this (see the comment there for
// why a per-session retry hatch driven by a per-socket counter was removed),
// but it stays so tests can assert that runBoundedConfigure actually fed the
// breaker on a deadline, rather than only exercising recordConfigureResult
// directly.
func configureTimeoutCount(socket string) uint64 {
	configureBreakerMu.Lock()
	defer configureBreakerMu.Unlock()
	return configureBreakerFor(socket).totalTimeouts
}

// runBoundedConfigure is runBoundedRun plus deadline annotation and breaker
// bookkeeping. Every command on the deferred-configuration path must go through
// it, otherwise the breaker is fed by only a subset of the commands that can
// wedge — EnableMouseMode's error, for instance, is non-nil only when
// [tmux].mouse is enabled AND the mouse mutation itself fails, so its enhance
// batch (tmux.go:2826) could hang on every session without the breaker ever
// noticing.
func (s *Session) runBoundedConfigure(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxPollTimeout)
	defer cancel()
	// Run() must complete (or be killed at the deadline) before ctx.Err() is
	// read. Go evaluates call arguments left-to-right, so inlining
	// annotateDeadline(ctx.Err(), tmuxExecContext(...).Run()) reads ctx.Err()
	// BEFORE Run() blocks — it is always nil, and the breaker never fires.
	// Matches runBoundedMutation (socket.go).
	runErr := tmuxExecContext(ctx, s.SocketName, args...).Run()
	err := annotateDeadline(ctx.Err(), runErr)
	recordConfigureResult(s.SocketName, errors.Is(err, errTmuxTimeout))
	return err
}
