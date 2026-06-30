package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// claudeSessionMeta is the subset of ~/.claude/sessions/<PID>.json that
// agent-deck reads for title sync (issue #572).
type claudeSessionMeta struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	// NameSource records how Claude chose the name. "derived" means Claude
	// auto-generated it from the working-directory basename (e.g.
	// "workspace-75"); any other value (e.g. "user") means the user chose it via
	// `claude --name` / `/rename`. Absent on older Claude versions that predate
	// the field. See ClaudeSessionNameIn for how it gates the title sync.
	NameSource string `json:"nameSource"`
	UpdatedAt  *int64 `json:"updatedAt"` // unix ms; nil when absent
}

// ClaudeSessionNameIn scans claudeDir/sessions/*.json and returns the trimmed
// `name` of the entry whose sessionId matches. Empty string when there's no
// match, no name, or the sessions dir is unreadable.
//
// The files are per-PID, so a resumed session can match several entries — the
// live process plus stale files left by earlier runs. The freshest entry (by
// updatedAt, falling back to file mtime) is authoritative, even when its name
// is empty: returning a stale file's old name would re-sync a title the user
// has since changed or cleared.
//
// Issue #572: Claude Code writes per-process metadata here when the user starts
// with `claude --name X` or runs `/rename X` mid-session. claudeDir is an
// explicit parameter so tests can point it at a temp dir.
func ClaudeSessionNameIn(claudeDir, sessionID string) string {
	claudeDir = strings.TrimSpace(claudeDir)
	sessionID = strings.TrimSpace(sessionID)
	if claudeDir == "" || sessionID == "" {
		return ""
	}
	entries, err := os.ReadDir(filepath.Join(claudeDir, "sessions"))
	if err != nil {
		return ""
	}
	bestName := ""
	bestSource := ""
	bestTime := int64(-1)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(claudeDir, "sessions", entry.Name()))
		if err != nil {
			continue
		}
		var meta claudeSessionMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			continue
		}
		if meta.SessionID != sessionID {
			continue
		}
		var ts int64
		if meta.UpdatedAt != nil {
			ts = *meta.UpdatedAt
		} else if info, err := entry.Info(); err == nil {
			ts = info.ModTime().UnixMilli()
		}
		if ts > bestTime {
			bestTime = ts
			bestName = strings.TrimSpace(meta.Name)
			bestSource = strings.TrimSpace(meta.NameSource)
		}
	}
	// A "derived" name is Claude's auto-generated <cwd-basename>-<id> handle
	// (e.g. "workspace-75"), not a name the user chose. Syncing it would clobber
	// the title the user set in agent-deck — the "sessions randomly renamed to
	// the folder name + a number" bug. Treat it as no syncable name so the
	// caller keeps the existing title. User-chosen names (`claude --name` /
	// `/rename`, which carry a non-"derived" nameSource) and legacy entries with
	// no nameSource field still flow through, preserving the #572 sync behavior.
	if strings.EqualFold(bestSource, "derived") {
		return ""
	}
	return bestName
}

// ClaudeSessionName resolves the user's ~/.claude and returns the Claude
// session name for sessionID. Empty string on any error or no match.
func ClaudeSessionName(sessionID string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return ClaudeSessionNameIn(filepath.Join(home, ".claude"), sessionID)
}

// ReconcileTitleFromClaude refreshes i.Title from the agent's current Claude
// session name. It is the shared core behind both the hook-event sync (#572)
// and the on-attach reconcile (#1114 follow-up): Claude's /rename fires no
// agent-deck hook, so an idle session's title and iTerm2 badge stay stale until
// the next turn boundary — reconciling on attach makes detach/reattach a
// reliable manual refresh.
//
// Honors the global sync_title switch and the per-session TitleLocked flag (so
// conductor titles like "SCRUM-351" survive Claude's own /rename). On a real
// change it mutates the in-memory instance (Title + tmux display name) and
// drops the iTerm2 badge-update signal so the attach-side WatchBadgeUpdates
// catch-up re-emits the fresh name instead of clobbering it with the old one.
//
// Returns the new name and true iff the title changed; the CALLER is
// responsible for persisting the instance to storage.
func (i *Instance) ReconcileTitleFromClaude(sessionID string) (string, bool) {
	if i == nil || i.TitleLocked {
		return "", false
	}
	if cfg, err := LoadUserConfig(); err == nil && cfg != nil && !cfg.GetSyncTitle() {
		return "", false
	}
	name := ClaudeSessionName(sessionID)
	if name == "" || name == i.Title {
		return "", false
	}
	i.Title = name
	i.SyncTmuxDisplayName()
	if tmuxSess := i.GetTmuxSession(); tmuxSess != nil && tmuxSess.Name != "" {
		_ = tmux.WriteBadgeUpdate(tmuxSess.Name, name)
	}
	return name, true
}
