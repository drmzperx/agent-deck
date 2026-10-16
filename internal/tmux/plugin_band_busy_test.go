package tmux

import (
	"os"
	"path/filepath"
	"testing"
)

// A Claude Code plugin may hide the engine's spinner and draw its own band in
// the spinner's slot, right above the composer, carrying the interrupt hint
// ("Folyamat ▰▰▱ · esc to interrupt …"). Plugin status lines drawn under the
// composer push that line out of the last-3-line window the interrupt hint is
// otherwise held to, so the slot above the composer is read too. Prose quoting
// the hint is indented (or behind "●"), so it never sits flush in that slot.
func TestHasBusyIndicator_PluginBandAboveComposer(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	cases := []struct {
		name, path string
		busy       bool
	}{
		{"band with hint above composer", filepath.Join("testdata", "status_corpus", "claude-plugin-band-busy-esc-cue.txt"), true},
		{"band left up after the turn, no hint", filepath.Join("testdata", "status_corpus", "claude-plugin-band-idle-no-cue.txt"), false},
		{"indented prose quoting the hint above composer", filepath.Join("testdata", "claude-plugin-band-idle-prose-esc.txt"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Session{DisplayName: "band", detectedTool: "claude"}
			s.mu.Lock()
			defer s.mu.Unlock()
			content := s.prepareFrame(StripANSI(read(c.path)))
			if got := s.hasBusyIndicator(content); got != c.busy {
				t.Fatalf("hasBusyIndicator = %v, want %v", got, c.busy)
			}
		})
	}
}
