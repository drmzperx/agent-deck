package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// highlightRole is one slot of the row-highlight palette. It is stored
// verbatim in Instance.Color, so the string values are a persisted format —
// renaming one silently drops every session already carrying it.
type highlightRole string

// highlightRoleNone means "this session is not role-highlighted". It covers
// an empty Color, a legacy hex/ANSI tint, and any value the validator would
// reject (a hand-edited tool_data blob).
const highlightRoleNone highlightRole = ""

// highlightRoles is the palette, in assignment order. nextFreeRole walks it
// front to back, so the order is user-visible: the first session you
// highlight gets purple, the second green, and so on.
//
// Two hues are deliberately absent. "blue" resolves to ColorAccent, the
// background the cursor row already paints (styles.go SessionTitleSelStyle),
// so a blue highlight is indistinguishable from plain cursor focus. "cyan" is
// GroupNameStyle — the colour of every group row — and also the codex tool
// badge, so a cyan-highlighted session reads as structure rather than as a
// mark the user placed.
var highlightRoles = []highlightRole{
	"purple",
	"green",
	"yellow",
	"orange",
	"red",
}

// roleOf classifies a stored Color value. Anything that is not exactly one of
// the six role names — empty, hex, ANSI index, or an unrecognised word — is
// highlightRoleNone. Matching is exact and case-sensitive: the CLI validator
// accepts only lowercase, so an uppercase value can only come from a
// hand-edited database and is treated as unrecognised rather than repaired.
func roleOf(value string) highlightRole {
	for _, role := range highlightRoles {
		if highlightRole(value) == role {
			return role
		}
	}
	return highlightRoleNone
}

// isLegacyColor reports whether value is a pre-existing issue #391 tint —
// "#RRGGBB" or an ANSI index "0".."255". Those keep rendering as a title
// foreground tint and never occupy a palette slot.
//
// It delegates the literal parsing to session.IsValidSessionColor rather than
// re-implementing it, and subtracts the role names first. Task 3 widens that
// validator to also accept role names, which is why the roleOf check must come
// first — afterwards, "green" would pass the validator too.
func isLegacyColor(value string) bool {
	if value == "" {
		return false
	}
	if roleOf(value) != highlightRoleNone {
		return false
	}
	return session.IsValidSessionColor(value)
}

// roleColor resolves a role against the active theme. Resolution happens at
// render time rather than at assignment time, so a live dark/light switch
// through theme_watcher.go recolors highlighted rows instead of stranding
// dark-theme hexes on a light background.
func roleColor(role highlightRole) lipgloss.Color {
	switch role {
	case "purple":
		return ColorPurple
	case "green":
		return ColorGreen
	case "yellow":
		return ColorYellow
	case "orange":
		return ColorOrange
	case "red":
		return ColorRed
	default:
		return ColorText
	}
}

// roleAtCounter maps a round-robin counter to a palette role, wrapping at the
// end. The double modulo keeps a counter that has overflowed to negative from
// indexing out of range — cheap insurance for a total function.
func roleAtCounter(n int) highlightRole {
	size := len(highlightRoles)
	return highlightRoles[((n%size)+size)%size]
}

// nextColorValue is the complete keypress semantics, expressed as a pure
// function so the press behavior is testable without a Bubble Tea harness. It
// returns the new Color value and the counter to store back.
//
// Colors are assigned ROUND-ROBIN: each new highlight takes the next palette
// slot and never looks back, so clearing a row does not hand its color to the
// next one. An earlier version picked the first slot nobody was using, which
// kept the on-screen colors distinct but meant clear-then-rehighlight returned
// the same color — the opposite of "every new highlight gets a new color".
//
// The trade-off that buys: with more than six highlights, or after a restart
// (the counter lives in memory on Home), two rows can share a color.
//
// Clearing does NOT advance the counter. Only assignments do — otherwise
// clearing a row would silently burn a slot and the next highlight would skip
// a color for no visible reason.
//
// Any non-empty Color counts as highlighted, including a legacy hex tint. That
// means the first press on a session someone colored via the CLI CLEARS it
// rather than silently overwriting it — losing a hand-set color invisibly
// would be worse than an extra keypress.
func nextColorValue(current string, counter int) (value string, nextCounter int) {
	if current != "" {
		return "", counter
	}
	return string(roleAtCounter(counter)), counter + 1
}

// highlightStyles are the per-segment styles a highlighted row renders with.
// Rows are assembled segment by segment (the existing cursor style works the
// same way), so there is no single row-level wrapper to hang a background on.
// The status glyph is deliberately absent: it keeps its own status colour and
// is built by highlightStatusStyle, which needs the glyph's original colour as
// input and so cannot be precomputed here.
type highlightStyles struct {
	Connector lipgloss.Style
	Title     lipgloss.Style
	Prefix    lipgloss.Style
}

// segmentStyles builds the styles for a highlighted row.
//
// Unselected: connector, status glyph and title all sit on the role color.
// Selected: the connector and status keep the band, but the title inverts —
// role-colored text on the normal background. That keeps cursor focus
// unmistakable while leaving the assigned color visible on the very row it was
// just assigned to; if the cursor style simply overrode the highlight, pressing
// the key would look like it did nothing until the user moved away.
//
// The status glyph is NOT built here — see highlightStatusStyle.
func segmentStyles(role highlightRole, selected bool) highlightStyles {
	// Named for the role's hue, not for one of its uses: it serves as the band
	// BACKGROUND on the connector and title, and as the FOREGROUND on both the
	// inverted selected title and the cursor prefix.
	accent := roleColor(role)

	band := lipgloss.NewStyle().Foreground(ColorBg).Background(accent)

	title := band
	if selected {
		title = lipgloss.NewStyle().Foreground(accent).Background(ColorBg).Bold(true)
	}

	return highlightStyles{
		Connector: band,
		Title:     title,
		Prefix:    lipgloss.NewStyle().Foreground(accent).Bold(true),
	}
}

// highlightStatusStyle puts the status glyph on the role band while keeping the
// glyph's own colour, so running/waiting/error stay readable at a glance on a
// highlighted row. An earlier version painted it ColorBg like the cursor style
// does, which destroyed the status signal — the glyph shape alone turned out
// not to carry it well enough in practice.
//
// statusFg is the glyph's ORIGINAL colour, captured before the selection and
// highlight blocks overwrite the row's status style.
//
// Collision fallback: the palette's green/yellow/red are the very same globals
// as the running/waiting/error glyph colours, so those three pairings would
// render the glyph invisible against its own background. There, and when the
// caller has no explicit colour to preserve, fall back to ColorBg — an
// uncoloured glyph beats an invisible one.
func highlightStatusStyle(role highlightRole, statusFg lipgloss.TerminalColor) lipgloss.Style {
	bg := roleColor(role)
	fg := lipgloss.TerminalColor(ColorBg)
	if c, ok := statusFg.(lipgloss.Color); ok && c != bg {
		fg = c
	}
	return lipgloss.NewStyle().Foreground(fg).Background(bg)
}

// highlightTargetFor returns the session a highlight keypress should act on, or
// nil when the focused row is not eligible. Groups, windows, dividers, remote
// mirrors and creating-placeholders all return nil. Extracted from the dispatch
// case purely so this rule is testable without a live Home.
func highlightTargetFor(item session.Item) *session.Instance {
	if item.Type != session.ItemTypeSession {
		return nil
	}
	if item.Session == nil || item.CreatingID != "" {
		return nil
	}
	return item.Session
}

// toggleRowHighlight flips the focused session's highlight. It mutates through
// session.SetField — the same entry point the CLI and web handlers use — so
// validation and the change signal stay shared rather than forked into the TUI.
//
// It deliberately does NOT persist: the caller owns that, because persistence
// must go through forceSaveInstances and this function needs to stay testable
// without a live database.
func (h *Home) toggleRowHighlight(inst *session.Instance) error {
	if inst == nil {
		return nil
	}
	h.instancesMu.Lock()
	next, nextCounter := nextColorValue(inst.Color, h.highlightCounter)
	_, _, err := session.SetField(inst, session.FieldColor, next, nil)
	if err == nil {
		// Only advance on a successful write, or a rejected value would burn a
		// palette slot and the next highlight would skip a color.
		h.highlightCounter = nextCounter
	}
	h.instancesMu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to set highlight: %w", err)
	}
	return nil
}
