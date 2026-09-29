//go:build linux
// +build linux

package tea

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type readyMsg struct{}

type readyModel struct{ ready chan struct{} }

func (m readyModel) Init() Cmd { return func() Msg { return readyMsg{} } }

func (m readyModel) Update(msg Msg) (Model, Cmd) {
	if _, ok := msg.(readyMsg); ok {
		close(m.ready)
	}
	return m, nil
}

func (m readyModel) View() string { return "" }

func countEpollFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("cannot inspect descriptors: %v", err)
	}
	n := 0
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name()))
		if err == nil && target == "anon_inode:[eventpoll]" {
			n++
		}
	}
	return n
}

// Every ReleaseTerminal/RestoreTerminal cycle (tea.Exec, suspend) replaces the
// input cancelreader. The replaced reader must be closed, or its epoll
// descriptor leaks: its pipe *os.Files are reclaimed by finalizers, the raw
// epoll int never is.
func TestReleaseRestoreDoesNotLeakCancelReader(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	m := readyModel{ready: make(chan struct{})}
	var out bytes.Buffer
	p := NewProgram(m, WithInput(r), WithOutput(&out))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.Run()
	}()
	select {
	case <-m.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("program did not start")
	}

	before := countEpollFDs(t)
	const cycles = 20
	for i := 0; i < cycles; i++ {
		if err := p.ReleaseTerminal(); err != nil {
			t.Fatalf("release %d: %v", i, err)
		}
		if err := p.RestoreTerminal(); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
	}
	after := countEpollFDs(t)

	p.Quit()
	<-done

	if after > before {
		t.Fatalf("epoll descriptors grew by %d over %d release/restore cycles (before=%d after=%d)", after-before, cycles, before, after)
	}
}
