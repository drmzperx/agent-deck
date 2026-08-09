package tmux

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pollSubcommands are the read-only tmux queries agent-deck fires on a
// cadence. Every one of them was observed spinning at ~100% CPU in the
// 2026-07-21 incident: a tmux 3.0a client leaks an epoll fd per event-loop
// iteration, and once it hits RLIMIT_NOFILE (1024) epoll_create returns
// EMFILE forever. The client retries without backoff, so it burns a core in
// pure system time (utime=0, stime=30298 on a 22-minute-old capture-pane)
// and can never exit on its own.
//
// The server is NOT wedged when this happens — a fresh `tmux ls` returns in
// milliseconds. Only clients that have already leaked their fd table are
// stuck, which is why the poller kept spawning replacements that each got
// stuck in turn: 14 live spinners, ~590% CPU, load average 28 on 8 cores.
//
// A deadline is the only defense that works, because the stuck client is
// unreachable by any in-band means. exec.CommandContext SIGKILLs it at
// tmuxPollTimeout, capping the damage at 3 core-seconds per leak.
var pollSubcommands = map[string]struct{}{
	"capture-pane":     {},
	"display-message":  {},
	"has-session":      {},
	"list-clients":     {},
	"list-panes":       {},
	"list-sessions":    {},
	"show-environment": {},
	"show-option":      {},
}

// mutationSubcommands are the state-CHANGING tmux commands agent-deck runs.
// The first round of fixes bounded only reads, on the reasoning that a
// half-applied mutation has murkier semantics than a re-runnable query. That
// reasoning was wrong about the risk: the fd leak is a property of the tmux
// CLIENT, not of the subcommand it carries, so a `kill-session` client wedges
// exactly like a `capture-pane` one — and these run on the same cadences (the
// notification-bar switch/detach sweep fires once per attached client,
// kill-session on every session teardown).
//
// What the murkier semantics actually demand is that each call site tolerate a
// timeout, not that it stay unbounded:
//   - kill-session — Session.Kill and KillAndWait already re-probe Exists() and
//     treat "gone" as success, so a client SIGKILLed mid-exchange resolves to
//     the same answer on the next probe.
//   - switch-client — SwitchAttachedClients returns switched=false and the
//     caller falls back to a focus_request.
//   - detach-client — DetachClientsOnSockets returns detached=false; the TUI
//     stays attached where it already was.
//
// All three are re-issued on the next user action, so a timeout degrades to a
// no-op rather than a corrupted half-state.
// The status-bar and key-binding commands join them for the same reason, one
// round later: the notification sweep (syncNotificationsBackground) drives
// SetStatusLeft / ClearStatusLeftGlobal / RefreshStatusBarImmediate and the
// Ctrl+b N rebinds on a timer, and refresh-client spawns one client per
// attached client per bar change. They are change-gated, but flapping
// running/waiting statuses fire them repeatedly — a cadence by any other name.
// Leaving them out let the lint imply coverage the campaign did not have.
var mutationSubcommands = map[string]struct{}{
	"bind-key":       {},
	"detach-client":  {},
	"kill-session":   {},
	"refresh-client": {},
	"set-option":     {},
	"switch-client":  {},
	"unbind-key":     {},
}

// requiresDeadline reports whether a tmux subcommand must be spawned through a
// deadline-carrying factory. Both sets qualify; they are kept separate only
// because their justifications differ (see each var's doc).
func requiresDeadline(sub string) bool {
	if _, ok := pollSubcommands[sub]; ok {
		return true
	}
	_, ok := mutationSubcommands[sub]
	return ok
}

// TestPollCommandsAreBounded fails the build if any cadence tmux query is
// spawned through an unbounded factory (tmuxExec / s.tmuxCmd / tmux.Exec)
// instead of a deadline-carrying one (runBoundedOutput / runBoundedRun /
// OutputBounded / tmuxExecContext / s.tmuxCmdContext).
//
// This is the companion to TestNoRawTmuxExec_OutsideAllowlist: that lint
// enforces WHICH SERVER a command talks to, this one enforces that the
// command is guaranteed to TERMINATE. v1.7.56 ("Part A") bounded some of
// these sites by hand and the rest silently stayed unbounded — hence a lint
// rather than a comment.
//
// Adding a new call site? Use the bounded helper. If a command genuinely
// must run unbounded (an interactive attach, a blocking pipe-pane), add it
// to allowedUnbounded with a one-line reason.
func TestPollCommandsAreBounded(t *testing.T) {
	root := moduleRoot(t)

	// Module-relative file path -> reason the file's poll calls may be
	// unbounded. Keep sorted for diffability.
	allowedUnbounded := map[string]string{
		// The bounded helpers themselves call the unbounded factory — that is
		// the whole point of the indirection.
		"internal/tmux/socket.go": "defines the bounded helpers; its factory calls are the sanctioned unbounded layer",
	}

	violations := scanForUnboundedPolls(t, root)

	var unallowed []string
	for _, v := range violations {
		rel, err := filepath.Rel(root, v.file)
		if err != nil {
			rel = v.file
		}
		rel = filepath.ToSlash(rel)

		if _, ok := allowedUnbounded[rel]; ok {
			continue
		}
		unallowed = append(unallowed, rel+":"+itoa(v.line)+": "+v.callee+"(… \""+v.sub+"\" …)")
	}

	sort.Strings(unallowed)

	if len(unallowed) > 0 {
		t.Fatalf(
			"%d unbounded cadence tmux poll(s) — a tmux client that leaks its fd table "+
				"spins at 100%% CPU forever and cannot be reaped in-band. Use "+
				"runBoundedOutput / runBoundedRun (in-package), OutputBounded (external), "+
				"or thread a context via tmuxExecContext / s.tmuxCmdContext:\n  %s",
			len(unallowed), strings.Join(unallowed, "\n  "))
	}
}

// unboundedPollSite is one cadence query spawned without a deadline.
type unboundedPollSite struct {
	file   string
	line   int
	callee string // "tmuxExec", "s.tmuxCmd", "tmux.Exec"
	sub    string // the tmux subcommand literal, or the "<non-literal>" sentinel when it cannot be statically proven
}

func scanForUnboundedPolls(t *testing.T, root string) []unboundedPollSite {
	t.Helper()

	var sites []unboundedPollSite
	err := walkGoFiles(root, func(path string) error {
		if strings.HasSuffix(filepath.Base(path), "_test.go") {
			// Tests drive tmux directly and are torn down by the harness.
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		slash := filepath.ToSlash(rel)
		if strings.HasPrefix(slash, ".worktrees/") ||
			strings.HasPrefix(slash, "vendor/") ||
			strings.HasPrefix(slash, "tests/") ||
			strings.HasPrefix(slash, "internal/testutil/") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil
		}

		sites = append(sites, collectSites(fset, f, path)...)
		return nil
	})
	if err != nil {
		t.Fatalf("walk module: %v", err)
	}
	return sites
}

// exemptionMarker suppresses an unprovable-argv report. Place it on the call
// line or the line immediately above, with a reason.
const exemptionMarker = "tmux-unbounded-ok:"

// scanSource runs the unbounded-poll scan over one in-memory file. Extracted
// so the lint's own rules can be unit-tested without touching the tree.
func scanSource(t *testing.T, path, src string) []unboundedPollSite {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return collectSites(fset, f, path)
}

// exemptedLines returns the set of line numbers suppressed by a marker comment,
// covering both the comment's own line and the line after it.
func exemptedLines(fset *token.FileSet, f *ast.File) map[int]bool {
	out := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if !strings.Contains(c.Text, exemptionMarker) {
				continue
			}
			line := fset.Position(c.Pos()).Line
			out[line] = true
			out[line+1] = true
		}
	}
	return out
}

// collectSites walks one parsed file and reports every unbounded cadence
// tmux call. A subcommand that cannot be proven safe statically — a variadic
// spread or a computed argument — is reported as "<non-literal>" rather than
// silently skipped; that inversion is the fix for the blind spot that let
// four unbounded set-option batches pass this lint for months.
func collectSites(fset *token.FileSet, f *ast.File, path string) []unboundedPollSite {
	exempt := exemptedLines(fset, f)
	var sites []unboundedPollSite

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		callee, subIdx, ok := unboundedTmuxCallee(call.Fun)
		if !ok || len(call.Args) <= subIdx {
			return true
		}
		// exec.Command and its execCommand seam are generic: only flag them
		// when argv[0] is literally "tmux".
		if callee == "exec.Command" || callee == "execCommand" {
			if bin, isLit := stringLiteral(call.Args[0]); !isLit || bin != "tmux" {
				return true
			}
		}

		line := fset.Position(call.Pos()).Line

		sub, isLit := stringLiteral(call.Args[subIdx])
		if !isLit || call.Ellipsis.IsValid() {
			// Cannot prove this is safe. A variadic spread or a computed arg
			// hides the subcommand from static reading — which is exactly how
			// four unbounded set-option batches survived this lint.
			//
			// The exemption marker is checked only in this branch: it waives
			// an unprovable-argv report, not a statically-provable violation
			// below. A marker above a literal `s.tmuxCmd("capture-pane", ...)`
			// must not silence that report.
			if exempt[line] {
				return true
			}
			sites = append(sites, unboundedPollSite{
				file: path, line: line, callee: callee, sub: "<non-literal>",
			})
			return true
		}
		if !requiresDeadline(sub) {
			return true
		}
		sites = append(sites, unboundedPollSite{
			file: path, line: line, callee: callee, sub: sub,
		})
		return true
	})
	return sites
}

// unboundedTmuxCallee reports whether fun is one of the deadline-free tmux
// command factories, and at which argument index the subcommand literal sits
// (the package-level factories take a socket name first; the Session method
// does not).
//
// The *Context variants (tmuxExecContext, s.tmuxCmdContext, tmux.ExecContext,
// exec.CommandContext, tmuxCommandContext) are all treated as bounded. That is
// a deliberate simplification: a context argument is assumed to carry a
// deadline. This lint does not verify that — a context.Background() passed
// straight through would slip by. Every such call site was audited by hand when
// this lint was written; if you add one, give it a real timeout.
func unboundedTmuxCallee(fun ast.Expr) (name string, subIdx int, ok bool) {
	switch fn := fun.(type) {
	case *ast.Ident:
		switch fn.Name {
		case "tmuxExec":
			// In-package: tmuxExec(socket, "list-sessions", …)
			return "tmuxExec", 1, true
		case "tmuxCommand":
			// internal/web's private socket-aware wrapper, which the
			// raw-exec lint allowlists wholesale:
			//   tmuxCommand(socketName, "has-session", …)
			return "tmuxCommand", 1, true
		case "execCommand":
			// The package-level swappable seam (tmux.go: `var execCommand =
			// exec.Command`) that the launcher-fallback tests override. It is
			// exec.Command by another name, so it inherits exec.Command's
			// deadline-free semantics — and the argv[0]=="tmux" filter below.
			return "execCommand", 1, true
		}
	case *ast.SelectorExpr:
		switch fn.Sel.Name {
		case "tmuxCmd":
			// Per-Session: s.tmuxCmd("capture-pane", …)
			return "s.tmuxCmd", 0, true
		case "Exec":
			// External packages: tmux.Exec(socketName, "list-panes", …)
			if pkg, isIdent := fn.X.(*ast.Ident); isIdent && pkg.Name == "tmux" {
				return "tmux.Exec", 1, true
			}
		case "Command":
			// Raw exec.Command("tmux", "display-message", …). These bypass the
			// argv factory entirely — TestNoRawTmuxExec_OutsideAllowlist
			// permits a handful for socket-routing reasons, and that allowance
			// silently exempted them from the deadline rule too. It must not:
			// the fd-leak spin is a property of the tmux CLIENT, so a plain-argv
			// client wedges exactly like a -L one.
			if pkg, isIdent := fn.X.(*ast.Ident); isIdent && pkg.Name == "exec" {
				return "exec.Command", 1, true
			}
		}
	}
	return "", 0, false
}

// TestUnprovableArgvIsRejected pins the inverted default: an unbounded tmux
// factory whose subcommand cannot be read statically must be reported, not
// skipped. Before 2026-08-08 the scanner returned early on a non-literal arg,
// which is how four unbounded set-option batches passed this lint for months.
func TestUnprovableArgvIsRejected(t *testing.T) {
	src := `package p
func f(s *Session, args []string) {
	_ = s.tmuxCmd(args...).Run()
}
`
	sites := scanSource(t, "internal/tmux/fake.go", src)
	if len(sites) != 1 {
		t.Fatalf("want 1 unprovable site, got %d: %+v", len(sites), sites)
	}
	if sites[0].sub != "<non-literal>" {
		t.Fatalf("want sub %q, got %q", "<non-literal>", sites[0].sub)
	}
}

func TestExemptionCommentSuppresses(t *testing.T) {
	src := `package p
func f(s *Session, args []string) {
	// tmux-unbounded-ok: user-initiated, not a cadence command
	_ = s.tmuxCmd(args...).Run()
}
`
	sites := scanSource(t, "internal/tmux/fake.go", src)
	if len(sites) != 0 {
		t.Fatalf("exemption comment ignored, got %+v", sites)
	}
}
