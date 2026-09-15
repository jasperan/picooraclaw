package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"charm.land/huh/v2"

	"github.com/jasperan/picooraclaw/internal/huhstyle"
	"github.com/jasperan/picooraclaw/pkg/config"
)

// This file covers the accessible (screen-reader) path of the onboard wizard.
//
// Why it exists: huh's accessible prompts run a field's validator on the raw line and only
// afterwards substitute the field's default, and they never print that default. A pre-filled
// field whose validator rejects "" therefore re-prompts on every bare Enter, so a
// screen-reader user cannot accept a value they cannot see.
//
// Note on scope: huh's Form.runAccessible ignores the error returned by Field.RunAccessible,
// so accessible mode does not enforce validation -- the validator's only effect there is
// whether the prompt accepts the answer or asks again. That is exactly what these tests pin.

var errBlank = errors.New("blank answer rejected")

// wizardStep titles, in wizardSteps order. The accessible runner walks them one at a time.
const (
	stepBaseURL  = 2 // Base URL: the one page whose field has NO seed
	stepADB      = 6 // Autonomous Database: DSN + wallet
	stepWorkpace = 7 // Workspace: workspace + restrict
)

// runStepAccessible drives one wizard page through huh's real accessible path with scripted
// input, returning everything it wrote plus the error Run returned.
func runStepAccessible(t *testing.T, g *huh.Group, input string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	f := huh.NewForm(g).
		WithTheme(huh.ThemeFunc(huhstyle.Theme)).
		WithAccessible(true).
		WithInput(strings.NewReader(input)).
		WithOutput(&out)

	done := make(chan error, 1)
	go func() { done <- f.Run() }()

	select {
	case err := <-done:
		return out.String(), err
	case <-time.After(10 * time.Second):
		t.Fatalf("accessible page did not finish within 10s; output so far:\n%s", out.String())
		return "", nil
	}
}

func stepByIndex(t *testing.T, cfg *config.Config, i int) (*huh.Group, *wizardState) {
	t.Helper()
	st := newWizardState(cfg)
	steps := wizardSteps(cfg, st)
	if i < 0 || i >= len(steps) {
		t.Fatalf("step %d out of range (%d steps)", i, len(steps))
	}
	return steps[i].group, st
}

// TestValidateDefaultedAcceptsBlank is the unit half of the fix. The contract is narrow:
// blank is accepted, everything else stays the inner validator's business.
func TestValidateDefaultedAcceptsBlank(t *testing.T) {
	inner := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errBlank
		}
		if s == "bad" {
			return errors.New("not usable")
		}
		return nil
	}
	wrapped := ValidateDefaulted(inner)

	for _, in := range []string{"", "   ", "\t"} {
		if err := wrapped(in); err != nil {
			t.Errorf("ValidateDefaulted(inner)(%q) = %v, want nil", in, err)
		}
	}
	if err := wrapped("bad"); err == nil {
		t.Error(`ValidateDefaulted(inner)("bad") = nil, want the inner validator's error`)
	}
	if err := wrapped("localhost"); err != nil {
		t.Errorf(`ValidateDefaulted(inner)("localhost") = %v, want nil`, err)
	}
}

// TestValidateDefaultedValueOnlyRelaxesWhenSomethingIsKept is the guard: the wizard is seeded
// from a config the user may have hand-edited, so a field with no seed must still reject blank.
func TestValidateDefaultedValueOnlyRelaxesWhenSomethingIsKept(t *testing.T) {
	inner := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errBlank
		}
		return nil
	}

	if err := ValidateDefaultedValue("", inner)(""); err == nil {
		t.Error("empty seed accepted a blank answer; a required field was weakened")
	}
	if err := ValidateDefaultedValue("   ", inner)(""); err == nil {
		t.Error("whitespace-only seed accepted a blank answer")
	}
	if err := ValidateDefaultedValue("localhost", inner)(""); err != nil {
		t.Errorf("seeded field rejected blank: %v", err)
	}
}

// TestAccessibleBlankAnswerKeepsTheSeededValue is the regression test proper: a page whose
// field carries a seed must accept a bare Enter.
func TestAccessibleBlankAnswerKeepsTheSeededValue(t *testing.T) {
	t.Run("workspace", func(t *testing.T) {
		cfg := config.DefaultConfig()
		g, st := stepByIndex(t, cfg, stepWorkpace)
		if st.workspace == "" {
			t.Fatal("workspace is not seeded; the test would prove nothing")
		}
		seeded := st.workspace

		out, err := runStepAccessible(t, g, strings.Repeat("\n", 6))
		if err != nil {
			t.Fatalf("accessible run failed: %v\noutput:\n%s", err, out)
		}
		if strings.Contains(out, "workspace is required") {
			t.Errorf("a blank answer was rejected, so a screen-reader user cannot keep the "+
				"seeded workspace.\noutput:\n%s", out)
		}
		if st.workspace != seeded {
			t.Errorf("workspace = %q after a blank answer, want the seeded %q", st.workspace, seeded)
		}
	})

	t.Run("oracle dsn", func(t *testing.T) {
		// A user who already configured Autonomous Database: the DSN is seeded, so blank
		// must mean "keep it".
		cfg := config.DefaultConfig()
		cfg.Oracle.DSN = "adb.example.oraclecloud.com:1522/mydb_high"
		cfg.Oracle.WalletPath = "/tmp/wallet"
		g, st := stepByIndex(t, cfg, stepADB)
		seeded := st.oracleDSN
		if seeded == "" {
			t.Fatal("DSN is not seeded; the test would prove nothing")
		}

		out, err := runStepAccessible(t, g, strings.Repeat("\n", 6))
		if err != nil {
			t.Fatalf("accessible run failed: %v\noutput:\n%s", err, out)
		}
		if strings.Contains(out, "a DSN is required") {
			t.Errorf("a blank answer was rejected, so a screen-reader user cannot keep the "+
				"seeded DSN.\noutput:\n%s", out)
		}
		if st.oracleDSN != seeded {
			t.Errorf("DSN = %q after a blank answer, want the seeded %q", st.oracleDSN, seeded)
		}
	})
}

// TestAccessibleStillRejectsBlankWithoutASeed is the negative half, exercised on a real page:
// the Base URL field is only shown for backends with no preset and is never seeded, so a
// blank answer must still be refused. If this ever passes silently, the wrapper has been
// applied too broadly.
func TestAccessibleStillRejectsBlankWithoutASeed(t *testing.T) {
	cfg := config.DefaultConfig()
	g, st := stepByIndex(t, cfg, stepBaseURL)
	if st.apiBase != "" {
		t.Fatalf("Base URL is seeded with %q; the test assumes it has no default", st.apiBase)
	}

	out, err := runStepAccessible(t, g, strings.Repeat("\n", 3))
	if err != nil {
		t.Fatalf("accessible run failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "a base URL is required") {
		t.Errorf("a blank answer was accepted on an unseeded required field, so the blank-pass "+
			"wrapper leaked beyond seeded fields.\noutput:\n%s", out)
	}

	// The same guard, on the DSN, with the seed removed.
	cfg2 := config.DefaultConfig()
	g2, st2 := stepByIndex(t, cfg2, stepADB)
	if st2.oracleDSN != "" {
		t.Fatalf("DSN is seeded with %q; the test assumes it has no default", st2.oracleDSN)
	}
	out2, err := runStepAccessible(t, g2, strings.Repeat("\n", 3))
	if err != nil {
		t.Fatalf("accessible run failed: %v\noutput:\n%s", err, out2)
	}
	if !strings.Contains(out2, "a DSN is required") {
		t.Errorf("an unseeded DSN accepted a blank answer.\noutput:\n%s", out2)
	}
}
