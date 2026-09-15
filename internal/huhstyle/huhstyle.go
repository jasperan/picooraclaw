// Package huhstyle adapts charmbracelet/huh forms to the project-wide
// "Premium Dark-Tech" design tokens (Catppuccin Mocha).
//
// Canonical source: docs/huh/huhstyle.go
// Token spec:       docs/tui-design-tokens.md
// Enforced by:      scripts/tui-shot/check_palette.py
//
// The 14 hex literals below are the only approved colors, and they are the
// ONLY hex literals allowed in this file -- check_palette.py scans comments
// too, so never write an auxiliary shade down here.
//
// huh's built-in ThemeCatppuccin() is NOT compliant: it paints with the
// auxiliary Catppuccin shades subtext1, overlay1, pink and rosewater, none of
// which are approved tokens. Never use it; always pass Theme via WithTheme.
package huhstyle

import (
	"os"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// Approved design tokens. Do not add, rename or re-value.
const (
	hexBG        = "#1e1e2e" // bg
	hexSurface   = "#181825" // surface
	hexElevated  = "#313244" // elevated
	hexHighest   = "#45475a" // highest
	hexText      = "#cdd6f4" // text
	hexSubtext   = "#a6adc8" // subtext
	hexMuted     = "#6c7086" // muted
	hexDim       = "#585b70" // dim
	hexPrimary   = "#89b4fa" // primary
	hexSecondary = "#cba6f7" // secondary
	hexInfo      = "#89dceb" // info
	hexSuccess   = "#a6e3a1" // success
	hexWarning   = "#f9e2af" // warning
	hexError     = "#f38ba8" // error
)

var (
	colBG       = lipgloss.Color(hexBG)
	colElevated = lipgloss.Color(hexElevated)
	colText     = lipgloss.Color(hexText)
	colSubtext  = lipgloss.Color(hexSubtext)
	colMuted    = lipgloss.Color(hexMuted)
	colDim      = lipgloss.Color(hexDim)
	colPrimary  = lipgloss.Color(hexPrimary)
	colInfo     = lipgloss.Color(hexInfo)
	colError    = lipgloss.Color(hexError)
	_           = hexSurface
	_           = hexHighest
	_           = hexSecondary
	_           = hexSuccess
	_           = hexWarning
)

// Accessible reports whether the user requested screen-reader mode.
//
// Wire it into every form: huh then swaps the TUI for plain prompts, which is
// the only sane rendering when a screen reader is driving the terminal.
func Accessible() bool { return os.Getenv("ACCESSIBLE") != "" }

// Interactive reports whether stdin is a terminal, i.e. whether a form can
// actually be driven by a human.
//
// Callers must route around a prompt when this is false rather than into it:
// a huh form attached to a pipe or a cron job can never be answered.
//
// This must use term.IsTerminal, NOT an os.ModeCharDevice probe: /dev/null IS a
// character device, so a char-device check reports "interactive" for exactly the
// case it is supposed to reject, and a form would then hang forever on /dev/null.
func Interactive() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// NOTE ON ACCESSIBILITY, measured against huh v2.0.3:
//
// `WithAccessible` is only consulted inside Form.RunWithContext (form.go:676).
// Form.Init/Update/View never read the flag, so an *embedded* form (driven as a
// Bubble Tea component) renders identically with and without it -- screen-reader
// support is real only for a form run standalone via Form.Run().
//
// Pass WithAccessible(huhstyle.Accessible()) anyway (it is the correct intent and
// costs nothing), but never claim accessible support for an embedded form. If a
// flow genuinely must be accessible, run that form standalone on the
// Accessible() path instead of embedding it.

// Styles builds token-compliant form styles.
//
// Design rules applied:
//  1. rounded borders everywhere
//  2. focus is a border *color* change, never a thickness change
//  4. semantic colors only where the content is genuinely semantic
//  6. selection is inverted (accent background, dark text, bold)
//  8. titles are bold and primary/text coloured
func Styles() *huh.Styles {
	t := huh.ThemeBase(true)

	// Focused field: rounded box, primary border (rule 1 + 2).
	t.Focused.Base = lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colPrimary)
	t.Focused.Card = t.Focused.Base

	// Titles are the focal hierarchy (rule 8).
	t.Focused.Title = t.Focused.Title.Bold(true).Foreground(colPrimary)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Bold(true).Foreground(colPrimary)
	t.Focused.Description = t.Focused.Description.Foreground(colSubtext)
	t.Focused.Directory = t.Focused.Directory.Foreground(colInfo)
	t.Focused.File = t.Focused.File.Foreground(colText)

	// Errors are the one place a semantic color is mandatory (rule 4).
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(colError)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(colError)

	// Select: the cursor row is an inverted selection (rule 6).
	t.Focused.SelectSelector = t.Focused.SelectSelector.
		Foreground(colBG).
		Background(colPrimary).
		Bold(true)
	t.Focused.Option = t.Focused.Option.Foreground(colText)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(colPrimary)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(colPrimary)

	// Multi-select: chosen items are accent-coloured, not success-coloured
	// (selection is not a success event).
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(colPrimary)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(colPrimary).Bold(true)
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(colPrimary).Bold(true)
	t.Focused.UnselectedPrefix = t.Focused.UnselectedPrefix.Foreground(colMuted)
	t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(colText)

	// Confirm buttons: inverted accent for the focused choice (rule 6). A blurred
	// confirm must NOT keep the primary fill, or two fields would both read as
	// focused and rule 2 (focus is a colour change) would be broken.
	t.Focused.FocusedButton = t.Focused.FocusedButton.
		Foreground(colBG).
		Background(colPrimary).
		Bold(true)
	t.Focused.BlurredButton = t.Focused.BlurredButton.
		Foreground(colText).
		Background(colElevated)

	// Inputs.
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(colInfo)
	t.Focused.TextInput.CursorText = t.Focused.TextInput.CursorText.
		Foreground(colBG).
		Background(colInfo)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(colMuted)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(colPrimary)
	t.Focused.TextInput.Text = t.Focused.TextInput.Text.Foreground(colText)

	// Blurred fields keep their structure but lose the accent (rule 2).
	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderForeground(colDim)
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.FocusedButton = t.Blurred.BlurredButton
	t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	// Help footer: keys readable, descriptions muted.
	t.Help.Ellipsis = t.Help.Ellipsis.Foreground(colMuted)
	t.Help.ShortKey = t.Help.ShortKey.Foreground(colSubtext)
	t.Help.ShortDesc = t.Help.ShortDesc.Foreground(colMuted)
	t.Help.ShortSeparator = t.Help.ShortSeparator.Foreground(colDim)
	t.Help.FullKey = t.Help.FullKey.Foreground(colSubtext)
	t.Help.FullDesc = t.Help.FullDesc.Foreground(colMuted)
	t.Help.FullSeparator = t.Help.FullSeparator.Foreground(colDim)

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}

// Theme adapts Styles to huh's Theme interface so it can be passed to
// Form.WithTheme. The isDark argument is ignored: this project ships a single
// dark palette by design.
func Theme(bool) *huh.Styles { return Styles() }
