package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasperan/picooraclaw/pkg/config"
)

// Every offered backend must map onto a name providers.CreateProvider
// dispatches on, otherwise onboard would write a provider picooraclaw cannot
// construct. This list is the switch in pkg/providers/http_provider.go.
var createProviderNames = map[string]bool{
	"groq":           true,
	"openai":         true,
	"anthropic":      true,
	"openrouter":     true,
	"gemini":         true,
	"ollama":         true,
	"vllm":           true,
	"claude-cli":     true,
	"codex-cli":      true,
	"deepseek":       true,
	"github_copilot": true,
}

func TestProviderChoicesAreConstructibleAndUnique(t *testing.T) {
	seenID := map[string]bool{}
	openAIChoices := 0

	for _, c := range providerChoices() {
		if c.id == "" {
			t.Fatalf("choice %q has an empty id", c.label)
		}
		if seenID[c.id] {
			t.Errorf("duplicate choice id %q", c.id)
		}
		seenID[c.id] = true

		if !createProviderNames[c.provider] {
			t.Errorf("choice %q uses provider %q which CreateProvider does not dispatch", c.id, c.provider)
		}
		if c.label == "" {
			t.Errorf("choice %q has an empty label", c.id)
		}
		if c.description == "" {
			t.Errorf("choice %q has an empty description", c.id)
		}
		if c.provider == "openai" {
			openAIChoices++
		}

		// A backend must be explicit about how it authenticates, otherwise the
		// credential group hides itself and the user is never asked.
		if c.presetKey == "" && c.keyHint == "" && !c.needAPIBase {
			switch c.provider {
			case "ollama", "claude-cli", "codex-cli", "github_copilot":
			default:
				t.Errorf("choice %q has no credential strategy", c.id)
			}
		}
	}

	if openAIChoices < 2 {
		t.Errorf("expected the bundled OCI proxy and plain OpenAI to share the openai provider, got %d", openAIChoices)
	}

	if !seenID["oci-genai"] {
		t.Error("the bundled OCI proxy option is missing")
	}
}

func TestProviderConfigForCoversEveryOfferedProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	for _, c := range providerChoices() {
		pc := providerConfigFor(cfg, c.provider)
		switch c.provider {
		case "claude-cli", "codex-cli":
			if pc != nil {
				t.Errorf("%s is CLI-backed and must not expose a provider config", c.id)
			}
		default:
			if pc == nil {
				t.Errorf("%s has no provider config to write into", c.id)
			}
		}
	}
	if pc := providerConfigFor(cfg, "moonshot"); pc != nil {
		t.Error("moonshot has no CreateProvider case and must not be writable")
	}
}

func TestChoiceByIDRoundTrips(t *testing.T) {
	for _, c := range providerChoices() {
		got, ok := choiceByID(c.id)
		if !ok || got.id != c.id {
			t.Errorf("choiceByID(%q) = %v, %v", c.id, got.id, ok)
		}
	}
	if _, ok := choiceByID("nope"); ok {
		t.Error("choiceByID accepted an unknown id")
	}
}

func TestInitialChoiceIDUsesAPIBaseToDisambiguateOpenAI(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		apiBase  string
		want     string
	}{
		{"bundled proxy", "openai", "http://localhost:9999/v1", "oci-genai"},
		{"plain openai", "openai", "https://api.openai.com/v1", "openai"},
		{"no base recorded", "openai", "", "oci-genai"},
		{"ollama", "ollama", "http://localhost:11434/v1", "ollama"},
		{"case insensitive", "OpenRouter", "https://openrouter.ai/api/v1", "openrouter"},
		{"unknown provider falls back", "nvidia", "", "oci-genai"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Provider = tt.provider
			if pc := providerConfigFor(cfg, strings.ToLower(tt.provider)); pc != nil {
				pc.APIBase = tt.apiBase
			}
			if got := initialChoiceID(cfg); got != tt.want {
				t.Errorf("initialChoiceID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyWizardWritesPresetCredential(t *testing.T) {
	cfg := config.DefaultConfig()
	st := &wizardState{choiceID: "oci-genai", apiKey: "typed-by-user", model: "xai.grok-4"}

	applyWizard(cfg, st)

	if cfg.Agents.Defaults.Provider != "openai" {
		t.Errorf("provider = %q, want openai", cfg.Agents.Defaults.Provider)
	}
	if cfg.Providers.OpenAI.APIKey != "oci-genai" {
		t.Errorf("api key = %q, want the fixed proxy credential to win", cfg.Providers.OpenAI.APIKey)
	}
	if cfg.Providers.OpenAI.APIBase != "http://localhost:9999/v1" {
		t.Errorf("api base = %q", cfg.Providers.OpenAI.APIBase)
	}
}

func TestApplyWizardWritesTypedCredential(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.OpenRouter.APIKey = ""
	st := &wizardState{choiceID: "openrouter", apiKey: "  sk-or-secret  "}

	applyWizard(cfg, st)

	if cfg.Providers.OpenRouter.APIKey != "sk-or-secret" {
		t.Errorf("api key = %q, want the trimmed typed key", cfg.Providers.OpenRouter.APIKey)
	}
}

// A blank key must keep an existing one: the wizard never sees the stored value
// so it must not clear it.
func TestApplyWizardKeepsStoredCredentialWhenBlank(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.OpenRouter.APIKey = "sk-existing"
	st := &wizardState{choiceID: "openrouter", apiKey: ""}

	applyWizard(cfg, st)

	if cfg.Providers.OpenRouter.APIKey != "sk-existing" {
		t.Errorf("api key = %q, want the stored key preserved", cfg.Providers.OpenRouter.APIKey)
	}
}

// Switching backends must not copy the previous provider's credential across.
func TestApplyWizardDoesNotCarryCredentialAcrossProviders(t *testing.T) {
	cfg := config.DefaultConfig() // OpenAI.APIKey is "oci-genai"
	st := &wizardState{choiceID: "openrouter", apiKey: ""}

	applyWizard(cfg, st)

	if cfg.Providers.OpenRouter.APIKey != "" {
		t.Errorf("openrouter api key = %q, want empty (no carry-over)", cfg.Providers.OpenRouter.APIKey)
	}
	if cfg.Providers.OpenAI.APIKey != "oci-genai" {
		t.Errorf("the untouched provider's key was modified: %q", cfg.Providers.OpenAI.APIKey)
	}
}

func TestApplyWizardWritesSelfHostedBaseURL(t *testing.T) {
	cfg := config.DefaultConfig()
	st := &wizardState{choiceID: "vllm", apiBase: " http://gpu-box:8000/v1 "}

	applyWizard(cfg, st)

	if cfg.Providers.VLLM.APIBase != "http://gpu-box:8000/v1" {
		t.Errorf("api base = %q", cfg.Providers.VLLM.APIBase)
	}
}

func TestApplyWizardWritesOracleAndChannels(t *testing.T) {
	cfg := config.DefaultConfig()
	st := &wizardState{
		choiceID:       "ollama",
		model:          "gemma4",
		oracleEnabled:  true,
		oracleMode:     "adb",
		oracleHost:     "localhost",
		oraclePort:     "1522",
		oracleService:  "mydb_high",
		oracleUser:     "scott",
		oraclePassword: "tiger",
		oracleDSN:      "adb.example.com:1522/mydb_high",
		oracleWallet:   "/w",
		oracleONNX:     "ALL_MINILM_L12_V2",
		embeddings:     "onnx",
		workspace:      "  /tmp/ws  ",
		restrict:       false,
		channels:       []string{"telegram", "slack", "not-a-channel"},
		proactive:      false,
	}

	applyWizard(cfg, st)

	if !cfg.Oracle.Enabled || cfg.Oracle.Mode != "adb" {
		t.Errorf("oracle = %+v", cfg.Oracle)
	}
	if cfg.Oracle.Port != 1522 {
		t.Errorf("oracle port = %d", cfg.Oracle.Port)
	}
	if cfg.Oracle.DSN != "adb.example.com:1522/mydb_high" {
		t.Errorf("oracle dsn = %q", cfg.Oracle.DSN)
	}
	if cfg.Agents.Defaults.Workspace != "/tmp/ws" {
		t.Errorf("workspace = %q, want trimmed", cfg.Agents.Defaults.Workspace)
	}
	if cfg.Agents.Defaults.RestrictToWorkspace {
		t.Error("restrict = true, want false")
	}
	if cfg.Proactive.Enabled {
		t.Error("proactive = true, want false")
	}
	if !cfg.Channels.Telegram.Enabled || !cfg.Channels.Slack.Enabled {
		t.Error("selected channels were not enabled")
	}
	if cfg.Channels.Discord.Enabled {
		t.Error("an unselected channel was enabled")
	}
}

func TestEnableChannelIgnoresUnknownNames(t *testing.T) {
	cfg := config.DefaultConfig()
	before := cfg.Channels
	enableChannel(&cfg.Channels, "myspace")
	if !reflect.DeepEqual(cfg.Channels, before) {
		t.Error("enableChannel mutated the config for an unknown channel")
	}
}

// Every offered channel must be wired to a real ChannelsConfig toggle,
// otherwise MultiSelect would silently accept a no-op.
func TestChannelChoicesAreAllWired(t *testing.T) {
	for _, opt := range channelChoices() {
		cfg := config.DefaultConfig()
		enableChannel(&cfg.Channels, opt.Value)
		if reflect.DeepEqual(cfg.Channels, config.DefaultConfig().Channels) {
			t.Errorf("channel %q (%s) enabled nothing", opt.Value, opt.Key)
		}
	}
}

func TestParseOnboardArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantDefaults bool
		wantYes      bool
		wantErr      bool
	}{
		{"none", nil, false, false, false},
		{"defaults", []string{"--defaults"}, true, false, false},
		{"no-input alias", []string{"--no-input"}, true, false, false},
		{"yes short", []string{"-y"}, false, true, false},
		{"both", []string{"--yes", "--non-interactive"}, true, true, false},
		{"help", []string{"--help"}, false, false, true},
		{"unknown", []string{"--whatever"}, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseOnboardArgs(tt.args)
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.defaults != tt.wantDefaults {
				t.Errorf("defaults = %v, want %v", got.defaults, tt.wantDefaults)
			}
			if got.yes != tt.wantYes {
				t.Errorf("yes = %v, want %v", got.yes, tt.wantYes)
			}
		})
	}
}

func TestValidateOracleDSN(t *testing.T) {
	valid := []string{
		"adb.example.oraclecloud.com:1522/mydb_high",
		"localhost:1521/FREEPDB1",
		"(DESCRIPTION=(ADDRESS=(PROTOCOL=tcps)(HOST=h)(PORT=1522))(CONNECT_DATA=(SERVICE_NAME=s)))",
	}
	for _, v := range valid {
		if err := validateOracleDSN(v); err != nil {
			t.Errorf("validateOracleDSN(%q) = %v, want nil", v, err)
		}
	}

	invalid := []string{"", "   ", "no-colon-here", "(NOT_A_DESCRIPTOR=1)", "host:1521 /svc"}
	for _, v := range invalid {
		if err := validateOracleDSN(v); err == nil {
			t.Errorf("validateOracleDSN(%q) = nil, want an error", v)
		}
	}
}

func TestValidatePort(t *testing.T) {
	for _, v := range []string{"1521", "1", "65535"} {
		if err := validatePort(v); err != nil {
			t.Errorf("validatePort(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"", "abc", "0", "65536", "-1", "15 21"} {
		if err := validatePort(v); err == nil {
			t.Errorf("validatePort(%q) = nil, want an error", v)
		}
	}
}

func TestValidateModelAndURLAndNotEmpty(t *testing.T) {
	if err := validateModel("  "); err == nil {
		t.Error("validateModel accepted whitespace")
	}
	if err := validateModel("gpt-4o"); err != nil {
		t.Errorf("validateModel(gpt-4o) = %v", err)
	}

	for _, v := range []string{"", "ftp://x", "localhost:9999"} {
		if err := validateURL(v); err == nil {
			t.Errorf("validateURL(%q) = nil, want an error", v)
		}
	}
	for _, v := range []string{"http://localhost:9999/v1", "https://api.openai.com/v1"} {
		if err := validateURL(v); err != nil {
			t.Errorf("validateURL(%q) = %v, want nil", v, err)
		}
	}

	if err := validateNotEmpty("host")("  "); err == nil {
		t.Error("validateNotEmpty accepted whitespace")
	}
	if err := validateNotEmpty("host")("localhost"); err != nil {
		t.Errorf("validateNotEmpty(localhost) = %v", err)
	}
}

func TestProviderKeyIsSet(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		mutate   func(*config.Config)
		want     bool
	}{
		{"openai with key", "openai", func(c *config.Config) { c.Providers.OpenAI.APIKey = "sk-x" }, true},
		{"openai without key", "openai", func(c *config.Config) { c.Providers.OpenAI.APIKey = "" }, false},
		{"ollama needs no key", "ollama", func(c *config.Config) { c.Providers.Ollama.APIKey = "" }, true},
		{"vllm needs a base", "vllm", func(c *config.Config) { c.Providers.VLLM.APIBase = "" }, false},
		{"vllm with base", "vllm", func(c *config.Config) { c.Providers.VLLM.APIBase = "http://h/v1" }, true},
		{"claude-cli needs no key", "claude-cli", func(c *config.Config) {}, true},
		{"unknown provider", "moonshot", func(c *config.Config) {}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Provider = tt.provider
			tt.mutate(cfg)
			if got := providerKeyIsSet(cfg); got != tt.want {
				t.Errorf("providerKeyIsSet() = %v, want %v", got, tt.want)
			}
		})
	}
}

// wizardState.selected must never panic on a zero value, because the select is
// bound before the user touches it.
func TestWizardStateSelectedFallsBack(t *testing.T) {
	st := &wizardState{}
	if got := st.selected(); got.id != providerChoices()[0].id {
		t.Errorf("zero-value selected() = %q, want the first choice", got.id)
	}
	st.choiceID = "does-not-exist"
	if got := st.selected(); got.id != providerChoices()[0].id {
		t.Errorf("unknown id selected() = %q, want the first choice", got.id)
	}
}

func TestWizardStateVisibilityPredicates(t *testing.T) {
	st := &wizardState{choiceID: "oci-genai"}
	if st.needsKey() {
		t.Error("the bundled proxy presets its key and must not prompt")
	}
	if st.needsAPIBase() {
		t.Error("the bundled proxy presets its base URL and must not prompt")
	}

	st.choiceID = "openai"
	if !st.needsKey() || st.needsAPIBase() {
		t.Errorf("plain openai: needsKey=%v needsAPIBase=%v", st.needsKey(), st.needsAPIBase())
	}

	st.choiceID = "vllm"
	if st.needsKey() || !st.needsAPIBase() {
		t.Errorf("vllm: needsKey=%v needsAPIBase=%v", st.needsKey(), st.needsAPIBase())
	}

	// CLI-backed backends have no base URL at all and must never be asked.
	for _, id := range []string{"claude-cli", "codex-cli", "github_copilot"} {
		st.choiceID = id
		if st.needsAPIBase() || st.needsKey() {
			t.Errorf("%s must prompt for nothing: needsAPIBase=%v needsKey=%v", id, st.needsAPIBase(), st.needsKey())
		}
	}

	st = &wizardState{oracleEnabled: false, oracleMode: "freepdb"}
	if st.isFreePDB() || st.isADB() {
		t.Error("both Oracle groups must be hidden when Oracle is disabled")
	}
	st = &wizardState{oracleEnabled: true, oracleMode: "freepdb"}
	if !st.isFreePDB() || st.isADB() {
		t.Error("freePDB group must show and ADB must hide")
	}
	st = &wizardState{oracleEnabled: true, oracleMode: "adb"}
	if st.isFreePDB() || !st.isADB() {
		t.Error("ADB group must show and freePDB must hide")
	}
}

// The wizard shows one page per applicable step. huh ignores hidden groups in
// accessible mode, so the page set is what runWizardSteps walks and what the
// interactive form hides -- this table pins both down.
func TestWizardStepsVisibility(t *testing.T) {
	tests := []struct {
		name  string
		st    *wizardState
		pages int
	}{
		{
			"bundled proxy, no oracle",
			&wizardState{choiceID: "oci-genai"},
			4, // backend, storage, workspace, channels
		},
		{
			"ollama needs no credentials",
			&wizardState{choiceID: "ollama"},
			4,
		},
		{
			"plain openai asks for a key",
			&wizardState{choiceID: "openai"},
			5, // + API key
		},
		{
			"vllm asks for a base URL only",
			&wizardState{choiceID: "vllm"},
			5, // + Base URL
		},
		{
			"claude-cli needs nothing",
			&wizardState{choiceID: "claude-cli"},
			4,
		},
		{
			"openrouter with freePDB",
			&wizardState{choiceID: "openrouter", oracleEnabled: true, oracleMode: "freepdb"},
			8, // key, deployment, connection, embeddings added
		},
		{
			"openrouter with ADB",
			&wizardState{choiceID: "openrouter", oracleEnabled: true, oracleMode: "adb"},
			8, // ADB replaces the freePDB pages
		},
		{
			"oracle disabled hides every oracle page",
			&wizardState{choiceID: "groq", oracleEnabled: false, oracleMode: "adb"},
			5, // only the API key page is added
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := wizardSteps(config.DefaultConfig(), tt.st)
			shown := 0
			for _, s := range steps {
				if s.show() {
					shown++
				}
			}
			if shown != tt.pages {
				t.Errorf("visible pages = %d, want %d", shown, tt.pages)
			}
			if len(steps) != 10 {
				t.Errorf("step definitions = %d, want the full set of 10", len(steps))
			}
		})
	}
}

// runWizardSteps must skip inapplicable pages without touching stdin, which is
// what makes the accessible path usable when Oracle is disabled.
func TestRunWizardStepsSkipsHiddenPages(t *testing.T) {
	steps := []wizardStep{
		{group: nil, show: func() bool { return false }},
		{group: nil, show: func() bool { return false }},
	}
	if err := runWizardSteps(steps); err != nil {
		t.Fatalf("runWizardSteps() = %v, want nil", err)
	}
}

// newWizardState must not pre-fill credentials from a stored provider, or
// switching backends would carry the old key across. A backend with a fixed
// credential (the bundled proxy) is the one exception.
func TestNewWizardStateDoesNotPrefillStoredKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "openrouter"
	cfg.Providers.OpenRouter.APIKey = "sk-should-not-leak"
	cfg.Providers.OpenRouter.APIBase = "https://openrouter.ai/api/v1"

	st := newWizardState(cfg)
	if st.choiceID != "openrouter" {
		t.Fatalf("choiceID = %q, want openrouter", st.choiceID)
	}
	if st.apiKey != "" {
		t.Errorf("apiKey = %q, want empty (no carry-over from the stored key)", st.apiKey)
	}
	if st.apiBase != "" {
		t.Errorf("apiBase = %q, want empty", st.apiBase)
	}

	// The bundled proxy has a fixed credential, so it is seeded on purpose.
	proxy := newWizardState(config.DefaultConfig())
	if proxy.apiKey != "oci-genai" {
		t.Errorf("bundled proxy apiKey = %q, want the fixed preset", proxy.apiKey)
	}
}
