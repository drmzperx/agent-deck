package ui

import (
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestRoleOf(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  highlightRole
	}{
		{"empty", "", highlightRoleNone},
		{"purple", "purple", "purple"},
		{"green", "green", "green"},
		{"red", "red", "red"},
		{"hex_is_not_a_role", "#ff00aa", highlightRoleNone},
		{"ansi_is_not_a_role", "203", highlightRoleNone},
		{"unknown_name", "magenta", highlightRoleNone},
		{"blue_is_excluded", "blue", highlightRoleNone},
		{"cyan_is_excluded", "cyan", highlightRoleNone},
		{"uppercase_not_accepted", "GREEN", highlightRoleNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := roleOf(tc.input); got != tc.want {
				t.Errorf("roleOf(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestIsLegacyColor(t *testing.T) {
	legacy := []string{"#ff00aa", "#FF00AA", "#000000", "0", "203", "255"}
	notLegacy := []string{"", "green", "red", "magenta", "cyan", "256", "#12"}
	for _, v := range legacy {
		if !isLegacyColor(v) {
			t.Errorf("isLegacyColor(%q) = false, want true", v)
		}
	}
	for _, v := range notLegacy {
		if isLegacyColor(v) {
			t.Errorf("isLegacyColor(%q) = true, want false", v)
		}
	}
}

func TestHighlightRolesOrderAndSize(t *testing.T) {
	want := []highlightRole{"purple", "green", "yellow", "orange", "red"}
	if len(highlightRoles) != len(want) {
		t.Fatalf("len(highlightRoles) = %d, want %d", len(highlightRoles), len(want))
	}
	for i := range want {
		if highlightRoles[i] != want[i] {
			t.Errorf("highlightRoles[%d] = %q, want %q", i, highlightRoles[i], want[i])
		}
	}
}

func TestRoleColorResolvesPerTheme(t *testing.T) {
	InitTheme("dark")
	darkGreen := roleColor("green")
	if darkGreen != ColorGreen {
		t.Errorf("roleColor(green) = %q, want ColorGreen %q", darkGreen, ColorGreen)
	}
	InitTheme("light")
	lightGreen := roleColor("green")
	if lightGreen != ColorGreen {
		t.Errorf("after light theme, roleColor(green) = %q, want ColorGreen %q", lightGreen, ColorGreen)
	}
	if darkGreen == lightGreen {
		t.Errorf("dark and light green resolved to the same value %q — theme switch had no effect", darkGreen)
	}
	InitTheme("dark")
}

func TestRoleColorNeverMatchesCursorAccent(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		InitTheme(theme)
		for _, role := range highlightRoles {
			if roleColor(role) == ColorAccent {
				t.Errorf("theme %s: role %q resolves to ColorAccent, which is the cursor color", theme, role)
			}
		}
	}
	InitTheme("dark")
}

func TestRoleAtCounter_WalksThePaletteInOrder(t *testing.T) {
	want := []highlightRole{"purple", "green", "yellow", "orange", "red"}
	for i, w := range want {
		if got := roleAtCounter(i); got != w {
			t.Errorf("roleAtCounter(%d) = %q, want %q", i, got, w)
		}
	}
}

func TestRoleAtCounter_WrapsAtTheEnd(t *testing.T) {
	if got := roleAtCounter(len(highlightRoles)); got != "purple" {
		t.Errorf("roleAtCounter(%d) = %q, want purple — the counter must wrap", len(highlightRoles), got)
	}
	if got := roleAtCounter(len(highlightRoles) + 1); got != "green" {
		t.Errorf("roleAtCounter(%d) = %q, want green", len(highlightRoles)+1, got)
	}
}

// A counter that ran negative (int overflow after an absurd number of presses)
// must still index the palette rather than panic.
func TestRoleAtCounter_HandlesNegative(t *testing.T) {
	for _, n := range []int{-1, -6, -7, math.MinInt} {
		got := roleAtCounter(n)
		found := false
		for _, r := range highlightRoles {
			if got == r {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("roleAtCounter(%d) = %q, which is not a palette role", n, got)
		}
	}
}

// Assigning advances the counter; clearing does not — otherwise clearing a row
// would silently burn a colour and the next highlight would skip it.
func TestNextColorValue_AssignAdvancesClearDoesNot(t *testing.T) {
	value, next := nextColorValue("", 0)
	if value != "purple" || next != 1 {
		t.Errorf("assign from counter 0 = (%q, %d), want (purple, 1)", value, next)
	}

	value, next = nextColorValue("green", 4)
	if value != "" || next != 4 {
		t.Errorf("clear at counter 4 = (%q, %d), want (\"\", 4) — clearing must not advance", value, next)
	}
}

// The whole point of round-robin: a cleared colour is NOT handed back to the
// next highlight, unlike the first-unused rule this replaced.
func TestNextColorValue_DoesNotReuseAClearedColour(t *testing.T) {
	counter := 0
	assigned := []string{}
	for i := 0; i < 3; i++ {
		var v string
		v, counter = nextColorValue("", counter)
		assigned = append(assigned, v)
	}
	// Clear the second one; the counter must not rewind.
	_, counter = nextColorValue(assigned[1], counter)

	v, _ := nextColorValue("", counter)
	if v == assigned[1] {
		t.Errorf("next assignment returned %q, the colour just cleared — round-robin must move on", v)
	}
	if v != "orange" {
		t.Errorf("next assignment = %q, want orange (4th slot)", v)
	}
}

func TestNextColorValue_ClearsAnyNonEmptyValue(t *testing.T) {
	for _, current := range []string{"green", "#ff00aa", "203", "magenta"} {
		v, next := nextColorValue(current, 2)
		if v != "" {
			t.Errorf("nextColorValue(%q) = %q, want empty", current, v)
		}
		if next != 2 {
			t.Errorf("nextColorValue(%q) advanced the counter to %d, want 2", current, next)
		}
	}
}

func TestSegmentStyles_UnselectedPaintsRoleBackground(t *testing.T) {
	InitTheme("dark")
	got := segmentStyles("green", false)
	if got.Title.GetBackground() != ColorGreen {
		t.Errorf("title background = %v, want ColorGreen %v", got.Title.GetBackground(), ColorGreen)
	}
	if got.Title.GetForeground() != ColorBg {
		t.Errorf("title foreground = %v, want ColorBg %v", got.Title.GetForeground(), ColorBg)
	}
	if got.Connector.GetBackground() != ColorGreen {
		t.Errorf("connector background = %v, want ColorGreen", got.Connector.GetBackground())
	}
	// The status glyph is no longer part of highlightStyles — it keeps its own
	// colour and is built by highlightStatusStyle. Covered by
	// TestHighlightStatusStyle_* below.
	if gotStatus := highlightStatusStyle("green", ColorTextDim); gotStatus.GetBackground() != ColorGreen {
		t.Errorf("status background = %v, want ColorGreen", gotStatus.GetBackground())
	}
}

func TestSegmentStyles_SelectedInvertsTitle(t *testing.T) {
	InitTheme("dark")
	got := segmentStyles("green", true)
	// Connector and status keep the band...
	if got.Connector.GetBackground() != ColorGreen {
		t.Errorf("connector background = %v, want ColorGreen", got.Connector.GetBackground())
	}
	// ...while the title punches out of it in reverse video.
	if got.Title.GetBackground() != ColorBg {
		t.Errorf("selected title background = %v, want ColorBg %v", got.Title.GetBackground(), ColorBg)
	}
	if got.Title.GetForeground() != ColorGreen {
		t.Errorf("selected title foreground = %v, want ColorGreen %v", got.Title.GetForeground(), ColorGreen)
	}
	if !got.Title.GetBold() {
		t.Error("selected title is not bold")
	}
	if got.Prefix.GetForeground() != ColorGreen {
		t.Errorf("prefix foreground = %v, want ColorGreen", got.Prefix.GetForeground())
	}
	if !got.Prefix.GetBold() {
		t.Error("selected prefix is not bold")
	}
	// The status glyph keeps its own colour on a selected row too, not just an
	// unselected one — the cursor must not reclaim it.
	if st := highlightStatusStyle("green", ColorTextDim); st.GetForeground() != ColorTextDim || st.GetBackground() != ColorGreen {
		t.Errorf("selected status = fg %v / bg %v, want fg ColorTextDim / bg ColorGreen", st.GetForeground(), st.GetBackground())
	}
}

func TestSegmentStyles_SelectedAndUnselectedTitlesDiffer(t *testing.T) {
	InitTheme("dark")
	sel := segmentStyles("orange", true)
	unsel := segmentStyles("orange", false)
	if sel.Title.GetForeground() == unsel.Title.GetForeground() &&
		sel.Title.GetBackground() == unsel.Title.GetBackground() {
		t.Error("selected and unselected titles are identical — the inversion does not invert")
	}
}

func TestHighlightRowHotkeyRegistered(t *testing.T) {
	key, ok := defaultHotkeyBindings[hotkeyHighlightRow]
	if !ok {
		t.Fatal("hotkeyHighlightRow missing from defaultHotkeyBindings")
	}
	if key != "alt+h" {
		t.Errorf("default binding = %q, want alt+h", key)
	}
	if defaultDisabledHotkeys[hotkeyHighlightRow] {
		t.Error("highlight_row must ship enabled, not opt-in")
	}
	found := false
	for _, action := range hotkeyActionOrder {
		if action == hotkeyHighlightRow {
			found = true
			break
		}
	}
	if !found {
		t.Error("hotkeyHighlightRow missing from hotkeyActionOrder — it will not appear in settings/help")
	}
}

func TestHighlightRowHotkeyDoesNotCollide(t *testing.T) {
	bindings := resolveHotkeys(nil)
	mine := bindings[hotkeyHighlightRow]
	if mine == "" {
		t.Fatal("highlight_row resolved to an empty binding")
	}
	for action, key := range bindings {
		if action != hotkeyHighlightRow && key == mine {
			t.Errorf("highlight_row binding %q collides with %s", mine, action)
		}
	}
}

func TestHighlightRowHotkeyUnbindable(t *testing.T) {
	bindings := resolveHotkeys(map[string]string{"highlight_row": ""})
	if _, ok := bindings[hotkeyHighlightRow]; ok {
		t.Error("binding highlight_row to \"\" did not unbind it")
	}
}

func TestToggleRowHighlight_AssignsThenClears(t *testing.T) {
	h := &Home{}
	inst := &session.Instance{Color: ""}
	h.instances = []*session.Instance{inst}

	if err := h.toggleRowHighlight(inst); err != nil {
		t.Fatalf("first toggle returned error: %v", err)
	}
	if inst.Color != "purple" {
		t.Errorf("after first toggle Color = %q, want purple", inst.Color)
	}

	if err := h.toggleRowHighlight(inst); err != nil {
		t.Fatalf("second toggle returned error: %v", err)
	}
	if inst.Color != "" {
		t.Errorf("after second toggle Color = %q, want empty", inst.Color)
	}
}

func TestToggleRowHighlight_SecondSessionGetsDifferentColor(t *testing.T) {
	h := &Home{}
	a := &session.Instance{}
	b := &session.Instance{}
	h.instances = []*session.Instance{a, b}

	if err := h.toggleRowHighlight(a); err != nil {
		t.Fatalf("toggle a: %v", err)
	}
	if err := h.toggleRowHighlight(b); err != nil {
		t.Fatalf("toggle b: %v", err)
	}
	if a.Color == b.Color {
		t.Errorf("both sessions got %q — each new highlight must get a distinct color", a.Color)
	}
}

func TestHighlightTargetFor(t *testing.T) {
	inst := &session.Instance{}
	cases := []struct {
		name string
		item session.Item
		want *session.Instance
	}{
		{"session", session.Item{Type: session.ItemTypeSession, Session: inst}, inst},
		{"group", session.Item{Type: session.ItemTypeGroup}, nil},
		{"window", session.Item{Type: session.ItemTypeWindow, Session: inst}, nil},
		{"remote_session", session.Item{Type: session.ItemTypeRemoteSession, Session: inst}, nil},
		{"divider", session.Item{Type: session.ItemTypeDivider}, nil},
		{"remote_group", session.Item{Type: session.ItemTypeRemoteGroup}, nil},
		{"nil_session", session.Item{Type: session.ItemTypeSession}, nil},
		{"creating_placeholder", session.Item{Type: session.ItemTypeSession, Session: inst, CreatingID: "abc"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := highlightTargetFor(tc.item); got != tc.want {
				t.Errorf("highlightTargetFor(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// The palette (internal/ui) and the validator (internal/session) define the
// role names separately, because internal/session must not import internal/ui.
// This is the only thing stopping them drifting, and it must check BOTH
// directions: a role here but not there makes a working keypress fail with
// "invalid color"; a role there but not here validates and then renders as a
// plain row. Either way CI stays green without this test.
func TestPaletteAndValidatorAgreeExactly(t *testing.T) {
	want := make([]string, 0, len(highlightRoles))
	for _, r := range highlightRoles {
		want = append(want, string(r))
	}
	sort.Strings(want)

	got := session.HighlightRoleNames()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("palette and validator have drifted:\n  internal/ui:      %v\n  internal/session: %v", want, got)
	}
}

// TestEveryRoleIsAcceptedByTheValidator only catches a role added to this
// package's list but missing from session's — it says nothing about the
// reverse (a role added to session's highlightRoleNames but never added
// here). highlightRoleNames is unexported in another package, so this test
// cannot enumerate it directly to compare set sizes.
//
// Instead this pins the size on both sides so a mismatch cannot pass
// silently: highlightRoles must stay at exactly 6 entries, and every one of
// them must classify as a real (non-none) role. The matching pin on the
// session side is session.TestIsValidSessionColor_Exported
// (internal/session/mutators_test.go), whose `valid` slice lists exactly
// these six role names and whose `invalid` slice explicitly asserts "blue"
// and case variants are rejected — so that test already fails if
// highlightRoleNames grows a 7th entry or loses one of these six. Between the
// two tests, neither list can drift without a test breaking somewhere.
func TestEveryRoleRoundTripsThroughRoleOf(t *testing.T) {
	for _, r := range highlightRoles {
		if roleOf(string(r)) != r {
			t.Errorf("role %q does not round-trip through roleOf as itself", r)
		}
	}
}

// The rendering split: role names paint a background, legacy #391 values keep
// their title-foreground tint. renderSessionItem branches on exactly this, so
// a regression here means one of the two rendering modes silently vanished.
func TestRenderingSplit_RoleVersusLegacy(t *testing.T) {
	InitTheme("dark")
	cases := []struct {
		name       string
		color      string
		wantRole   bool
		wantLegacy bool
	}{
		{"role", "green", true, false},
		{"hex", "#ff00aa", false, true},
		{"ansi", "203", false, true},
		{"empty", "", false, false},
		{"unrecognised", "magenta", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotRole := roleOf(tc.color) != highlightRoleNone
			gotLegacy := isLegacyColor(tc.color)
			if gotRole != tc.wantRole {
				t.Errorf("roleOf(%q) non-none = %v, want %v", tc.color, gotRole, tc.wantRole)
			}
			if gotLegacy != tc.wantLegacy {
				t.Errorf("isLegacyColor(%q) = %v, want %v", tc.color, gotLegacy, tc.wantLegacy)
			}
			if gotRole && gotLegacy {
				t.Errorf("%q classified as BOTH role and legacy — the two modes must be disjoint", tc.color)
			}
		})
	}
}

// The status glyph must keep its own colour on a highlighted row. Painting it
// ColorBg (as the cursor style does) destroys the running/waiting/error signal,
// which is the whole point of the glyph.
func TestHighlightStatusStyle_KeepsStatusColour(t *testing.T) {
	InitTheme("dark")
	got := highlightStatusStyle("purple", ColorGreen)
	if got.GetForeground() != ColorGreen {
		t.Errorf("foreground = %v, want ColorGreen %v — the status colour was destroyed", got.GetForeground(), ColorGreen)
	}
	if got.GetBackground() != ColorPurple {
		t.Errorf("background = %v, want ColorPurple %v", got.GetBackground(), ColorPurple)
	}
}

// The palette's green/yellow/red ARE the running/waiting/error colours, so
// those three combinations would render the glyph invisible against its own
// background. Fall back to ColorBg there — unreadable is worse than uncoloured.
func TestHighlightStatusStyle_FallsBackOnCollision(t *testing.T) {
	InitTheme("dark")
	cases := []struct {
		name     string
		role     highlightRole
		statusFg lipgloss.TerminalColor
	}{
		{"green_role_running_glyph", "green", ColorGreen},
		{"yellow_role_waiting_glyph", "yellow", ColorYellow},
		{"red_role_error_glyph", "red", ColorRed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := highlightStatusStyle(tc.role, tc.statusFg)
			if got.GetForeground() == got.GetBackground() {
				t.Errorf("foreground == background (%v) — the glyph is invisible", got.GetForeground())
			}
			if got.GetForeground() != ColorBg {
				t.Errorf("foreground = %v, want ColorBg %v on collision", got.GetForeground(), ColorBg)
			}
		})
	}
}

// A style with no explicit foreground must not produce an unreadable glyph.
func TestHighlightStatusStyle_UnsetForegroundFallsBack(t *testing.T) {
	InitTheme("dark")
	got := highlightStatusStyle("purple", lipgloss.NewStyle().GetForeground())
	if got.GetForeground() != ColorBg {
		t.Errorf("foreground = %v, want ColorBg for an unset status colour", got.GetForeground())
	}
}

// Non-colliding status colours survive on every role.
func TestHighlightStatusStyle_AllRolesKeepNonCollidingColour(t *testing.T) {
	InitTheme("dark")
	for _, role := range highlightRoles {
		got := highlightStatusStyle(role, ColorTextDim)
		if got.GetForeground() != ColorTextDim {
			t.Errorf("role %q: foreground = %v, want ColorTextDim %v", role, got.GetForeground(), ColorTextDim)
		}
	}
}

// The user-visible point of round-robin, at the Home level: clearing a
// highlight must not hand that colour to the next session highlighted.
func TestToggleRowHighlight_ClearedColourIsNotReused(t *testing.T) {
	h := &Home{}
	a := &session.Instance{}
	b := &session.Instance{}
	c := &session.Instance{}
	h.instances = []*session.Instance{a, b, c}

	for _, inst := range []*session.Instance{a, b} {
		if err := h.toggleRowHighlight(inst); err != nil {
			t.Fatalf("assign: %v", err)
		}
	}
	if a.Color != "purple" || b.Color != "green" {
		t.Fatalf("got a=%q b=%q, want purple/green", a.Color, b.Color)
	}

	// Clear b, freeing green.
	if err := h.toggleRowHighlight(b); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if b.Color != "" {
		t.Fatalf("b.Color = %q after clear, want empty", b.Color)
	}

	// The next assignment must move on, not reclaim green.
	if err := h.toggleRowHighlight(c); err != nil {
		t.Fatalf("assign after clear: %v", err)
	}
	if c.Color == "green" {
		t.Error("c reclaimed the cleared colour green — round-robin must move on")
	}
	if c.Color != "yellow" {
		t.Errorf("c.Color = %q, want yellow (3rd slot)", c.Color)
	}
}

// A failed write must not burn a palette slot.
func TestToggleRowHighlight_CounterSurvivesSixWraps(t *testing.T) {
	h := &Home{}
	seen := []string{}
	for i := 0; i < len(highlightRoles)+1; i++ {
		inst := &session.Instance{}
		h.instances = append(h.instances, inst)
		if err := h.toggleRowHighlight(inst); err != nil {
			t.Fatalf("assign %d: %v", i, err)
		}
		seen = append(seen, inst.Color)
	}
	if seen[0] != seen[len(seen)-1] {
		t.Errorf("after a full wrap got %q then %q, want the palette to repeat", seen[0], seen[len(seen)-1])
	}
}
