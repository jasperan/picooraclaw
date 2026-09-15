// Interactive onboarding for picooraclaw.
//
// `picooraclaw onboard` used to write a default config and then tell the user
// to hand-edit JSON to add an API key. This file turns that into a huh form
// over the real config surface in pkg/config, while keeping the old
// write-the-defaults behaviour available for unattended installs.

package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/jasperan/picooraclaw/internal/huhstyle"
	"github.com/jasperan/picooraclaw/pkg/config"
)

// providerChoice describes one selectable LLM backend. Every entry must
// correspond to a case in providers.CreateProvider, otherwise picooraclaw would
// accept a provider name it cannot actually construct.
type providerChoice struct {
	// id identifies the option. It is distinct from provider because several
	// options share a provider name (the bundled OCI proxy and plain OpenAI are
	// both "openai").
	id string
	// provider is written to agents.defaults.provider.
	provider string
	// label is the human-readable option text.
	label string
	// description explains the trade-off, shown under the title.
	description string
	// apiBase is the preset api_base. Empty means the user supplies one.
	apiBase string
	// presetKey is written straight into the provider config; the bundled proxy
	// and local backends have fixed credentials.
	presetKey string
	// keyHint points at the page where a key is issued. Empty means the backend
	// needs no key.
	keyHint string
	// needAPIBase prompts for api_base when no preset exists.
	needAPIBase bool
	// models are the model ids this repo documents for the backend.
	models []string
	// defaultModel pre-selects a model.
	defaultModel string
}

// providerChoices is the ordered list offered by the wizard. It mirrors the
// provider dispatch in pkg/providers/http_provider.go and the backends
// documented in README.md ("Step 2: Pick your LLM backend").
//
// Moonshot and NVIDIA have config structs but no CreateProvider case, so they
// are deliberately absent: offering them would produce an unusable config.
func providerChoices() []providerChoice {
	return []providerChoice{
		{
			id:           "oci-genai",
			provider:     "openai",
			label:        "OCI Generative AI (bundled proxy, default)",
			description:  "Oracle Cloud hosted models through the oci-genai proxy on localhost:9999. Needs ~/.oci/config, no API key.",
			apiBase:      "http://localhost:9999/v1",
			presetKey:    "oci-genai",
			models:       []string{"xai.grok-4", "xai.grok-3-mini", "meta.llama-3.3-70b-instruct", "cohere.command-r-plus"},
			defaultModel: "xai.grok-4",
		},
		{
			id:           "ollama",
			provider:     "ollama",
			label:        "Ollama (local open-weight models)",
			description:  "Runs on your own hardware. No API key, no cloud.",
			apiBase:      "http://localhost:11434/v1",
			models:       []string{"gemma4:26b", "gemma4"},
			defaultModel: "gemma4:26b",
		},
		{
			id:           "openai",
			provider:     "openai",
			label:        "OpenAI",
			description:  "api.openai.com.",
			apiBase:      "https://api.openai.com/v1",
			keyHint:      "https://platform.openai.com/api-keys",
			models:       []string{"gpt-4o", "gpt-4o-mini"},
			defaultModel: "gpt-4o",
		},
		{
			id:          "openrouter",
			provider:    "openrouter",
			label:       "OpenRouter",
			description: "Routes to many vendors behind one key.",
			apiBase:     "https://openrouter.ai/api/v1",
			keyHint:     "https://openrouter.ai/keys",
		},
		{
			id:          "anthropic",
			provider:    "anthropic",
			label:       "Anthropic (Claude)",
			description: "api.anthropic.com.",
			apiBase:     "https://api.anthropic.com/v1",
			keyHint:     "https://console.anthropic.com/settings/keys",
		},
		{
			id:          "groq",
			provider:    "groq",
			label:       "Groq",
			description: "api.groq.com.",
			apiBase:     "https://api.groq.com/openai/v1",
			keyHint:     "https://console.groq.com/keys",
		},
		{
			id:          "gemini",
			provider:    "gemini",
			label:       "Google Gemini",
			description: "generativelanguage.googleapis.com.",
			apiBase:     "https://generativelanguage.googleapis.com/v1beta",
			keyHint:     "https://aistudio.google.com/apikey",
		},
		{
			id:           "deepseek",
			provider:     "deepseek",
			label:        "DeepSeek",
			description:  "api.deepseek.com.",
			apiBase:      "https://api.deepseek.com/v1",
			keyHint:      "https://platform.deepseek.com/api_keys",
			models:       []string{"deepseek-chat", "deepseek-reasoner"},
			defaultModel: "deepseek-chat",
		},
		{
			id:          "vllm",
			provider:    "vllm",
			label:       "vLLM / self-hosted OpenAI-compatible",
			description: "Your own inference server. Requires the base URL.",
			needAPIBase: true,
		},
		{
			id:          "github_copilot",
			provider:    "github_copilot",
			label:       "GitHub Copilot",
			description: "Talks to a local Copilot bridge.",
		},
		{
			id:          "claude-cli",
			provider:    "claude-cli",
			label:       "Claude Code CLI",
			description: "Delegates to the `claude` binary already on PATH.",
		},
		{
			id:          "codex-cli",
			provider:    "codex-cli",
			label:       "Codex CLI",
			description: "Delegates to the `codex` binary already on PATH.",
		},
	}
}

// choiceByID resolves a select value to its wizard entry.
func choiceByID(id string) (providerChoice, bool) {
	for _, c := range providerChoices() {
		if c.id == id {
			return c, true
		}
	}
	return providerChoice{}, false
}

// initialChoiceID picks the starting option. Several options share a provider
// name (plain OpenAI and the bundled OCI proxy are both "openai"), so the
// stored api_base breaks the tie before falling back to the repo default.
func initialChoiceID(cfg *config.Config) string {
	stored := strings.ToLower(strings.TrimSpace(cfg.Agents.Defaults.Provider))

	base := ""
	if pc := providerConfigFor(cfg, stored); pc != nil {
		base = pc.APIBase
	}
	for _, c := range providerChoices() {
		if c.provider == stored && base != "" && c.apiBase == base {
			return c.id
		}
	}
	for _, c := range providerChoices() {
		if c.provider == stored {
			return c.id
		}
	}
	return providerChoices()[0].id
}

// providerConfigFor returns the providers.<name> sub-config a provider name
// writes into. Nil means the wizard must not write credentials for it.
func providerConfigFor(cfg *config.Config, name string) *config.ProviderConfig {
	switch name {
	case "anthropic":
		return &cfg.Providers.Anthropic
	case "openai":
		return &cfg.Providers.OpenAI
	case "openrouter":
		return &cfg.Providers.OpenRouter
	case "groq":
		return &cfg.Providers.Groq
	case "vllm":
		return &cfg.Providers.VLLM
	case "gemini":
		return &cfg.Providers.Gemini
	case "ollama":
		return &cfg.Providers.Ollama
	case "deepseek":
		return &cfg.Providers.DeepSeek
	case "github_copilot":
		return &cfg.Providers.GitHubCopilot
	default:
		return nil
	}
}

// channelChoices lists the channel toggles that map onto ChannelsConfig. Only
// channels with an implementation in pkg/channels are offered.
func channelChoices() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("Telegram", "telegram"),
		huh.NewOption("Discord", "discord"),
		huh.NewOption("Slack", "slack"),
		huh.NewOption("WhatsApp (bridge)", "whatsapp"),
		huh.NewOption("Feishu / Lark", "feishu"),
		huh.NewOption("DingTalk", "dingtalk"),
		huh.NewOption("QQ", "qq"),
		huh.NewOption("LINE", "line"),
		huh.NewOption("OneBot", "onebot"),
		huh.NewOption("MaixCam", "maixcam"),
	}
}

// enableChannel flips the Enabled flag for a channel name. Unknown names are
// ignored so a stale config value cannot panic the wizard.
func enableChannel(channels *config.ChannelsConfig, name string) {
	switch name {
	case "telegram":
		channels.Telegram.Enabled = true
	case "discord":
		channels.Discord.Enabled = true
	case "slack":
		channels.Slack.Enabled = true
	case "whatsapp":
		channels.WhatsApp.Enabled = true
	case "feishu":
		channels.Feishu.Enabled = true
	case "dingtalk":
		channels.DingTalk.Enabled = true
	case "qq":
		channels.QQ.Enabled = true
	case "line":
		channels.LINE.Enabled = true
	case "onebot":
		channels.OneBot.Enabled = true
	case "maixcam":
		channels.MaixCam.Enabled = true
	}
}

// onboardOptions holds the flags `picooraclaw onboard` accepts.
type onboardOptions struct {
	// defaults writes the stock config with no questions asked.
	defaults bool
	// yes answers the overwrite confirmation affirmatively.
	yes bool
}

// errOnboardHelp signals that usage was printed and onboarding should stop
// cleanly rather than be treated as a failure.
var errOnboardHelp = errors.New("onboard help requested")

// parseOnboardArgs reads onboard's flags. Unknown flags are rejected rather
// than ignored so a typo cannot silently skip a confirmation.
func parseOnboardArgs(args []string) (onboardOptions, error) {
	var opts onboardOptions
	for _, arg := range args {
		switch arg {
		case "--defaults", "--non-interactive", "--no-input":
			opts.defaults = true
		case "--yes", "-y":
			opts.yes = true
		case "--help", "-h":
			return opts, errOnboardHelp
		default:
			return opts, fmt.Errorf("unknown flag: %s", arg)
		}
	}
	return opts, nil
}

func onboardHelp() {
	fmt.Println()
	fmt.Println("Initialize picooraclaw configuration and workspace")
	fmt.Println()
	fmt.Println("Usage: picooraclaw onboard [options]")
	fmt.Println()
	fmt.Println("Options:")
	fmt.Println("  --defaults, --non-interactive   Write the default config without prompting")
	fmt.Println("  --yes, -y                       Overwrite an existing config without asking")
	fmt.Println()
	fmt.Println("Without a terminal, onboard writes the default config and exits.")
	fmt.Println("Run it in a terminal for guided setup.")
}

// wizardState holds the answers collected by runOnboardWizard. huh binds
// functions to these fields, so they must outlive the form.
type wizardState struct {
	choiceID string
	model    string
	apiKey   string
	apiBase  string

	oracleEnabled  bool
	oracleMode     string
	oracleHost     string
	oraclePort     string
	oracleService  string
	oracleUser     string
	oraclePassword string
	oracleDSN      string
	oracleWallet   string
	oracleONNX     string
	embeddings     string

	workspace string
	restrict  bool
	channels  []string
	proactive bool
}

// selected resolves the currently chosen provider choice. Every dynamic label
// and hidden-group predicate goes through it so the form reacts to the select.
func (s *wizardState) selected() providerChoice {
	if c, ok := choiceByID(s.choiceID); ok {
		return c
	}
	return providerChoices()[0]
}

// needsKey reports whether the selected backend authenticates with an API key.
func (s *wizardState) needsKey() bool { return s.selected().keyHint != "" }

// needsAPIBase reports whether the selected backend takes no base URL from this
// wizard and must therefore be asked for one.
//
// It keys off the explicit flag rather than an empty preset: CLI-backed
// backends have no base URL at all and must not be prompted for one.
func (s *wizardState) needsAPIBase() bool { return s.selected().needAPIBase }

// isFreePDB reports whether the Oracle connection group is for a local
// database rather than an Autonomous Database.
func (s *wizardState) isFreePDB() bool {
	return s.oracleEnabled && s.oracleMode != "adb"
}

// isADB reports whether the Autonomous Database group applies.
func (s *wizardState) isADB() bool { return s.oracleEnabled && s.oracleMode == "adb" }

// providerOptions builds the select options for the backend picker.
//
// It must be static rather than an OptionsFunc: huh's accessible mode reads the
// resolved option slice synchronously and never runs the async evaluation, so
// an OptionsFunc leaves the list empty and the prompt degenerates to
// "Enter a number between 1 and 0".
func providerOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(providerChoices()))
	for _, c := range providerChoices() {
		opts = append(opts, huh.NewOption(c.label, c.id))
	}
	return opts
}

// wizardStep is one page of the setup wizard. show reports whether the page
// applies to the answers gathered so far.
type wizardStep struct {
	group *huh.Group
	show  func() bool
}

// wizardSteps builds every page from the shared field definitions.
//
// The interactive path pools these into one paged form and hides the pages
// whose predicate is false. The accessible path runs them one at a time,
// because huh's accessible mode iterates every field of every group and
// ignores hidden groups -- pooling them would interrogate the user about
// Oracle fields they just declined.
func wizardSteps(cfg *config.Config, st *wizardState) []wizardStep {
	// The model-id suggestions follow the selected backend. All field values and
	// option lists stay static so accessible mode, which reads them
	// synchronously, sees the real text.
	modelSuggestions := func() []string { return st.selected().models }

	return []wizardStep{
		{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("Which LLM backend should picooraclaw use?").
					DescriptionFunc(func() string { return st.selected().description }, &st.choiceID).
					Options(providerOptions()...).
					Value(&st.choiceID),

				huh.NewInput().
					Title("Model").
					DescriptionFunc(func() string {
						if m := modelSuggestions(); len(m) > 0 {
							return "Documented ids: " + strings.Join(m, ", ")
						}
						return "Model id understood by the backend."
					}, &st.choiceID).
					SuggestionsFunc(modelSuggestions, &st.choiceID).
					Value(&st.model).
					Validate(ValidateDefaultedValue(st.model, validateModel)),
			).Title("Model backend"),
			show: func() bool { return true },
		},
		{
			group: huh.NewGroup(
				huh.NewInput().
					Title("API key").
					DescriptionFunc(func() string {
						c := st.selected()
						if pc := providerConfigFor(cfg, c.provider); pc != nil && pc.APIKey != "" {
							return "For " + c.label + ". A key is already stored - leave blank to keep it."
						}
						return "For " + c.label + ". Get one at " + c.keyHint +
							" - leave blank to add it later."
					}, &st.choiceID).
					EchoMode(huh.EchoModePassword).
					Value(&st.apiKey),
			).Title("API key"),
			show: func() bool { return st.needsKey() },
		},
		{
			group: huh.NewGroup(
				huh.NewInput().
					Title("Base URL").
					DescriptionFunc(func() string {
						c := st.selected()
						if pc := providerConfigFor(cfg, c.provider); pc != nil && pc.APIBase != "" {
							return "For " + c.label + ". Currently set to " + pc.APIBase + "."
						}
						return "For " + c.label + ". Where the OpenAI-compatible endpoint is served."
					}, &st.choiceID).
					Placeholder("http://host:port/v1").
					Value(&st.apiBase).
					// NOT wrapped in ValidateDefaultedValue: this field is only shown for
					// backends with no preset, and newWizardState never seeds st.apiBase, so
					// there is no default to keep and a blank answer is genuinely invalid.
					Validate(validateURL),
			).Title("Base URL"),
			show: func() bool { return st.needsAPIBase() },
		},
		{
			group: huh.NewGroup(
				huh.NewConfirm().
					Title("Store memories and code index in Oracle AI Database?").
					Description("Adds semantic recall over in-database ONNX embeddings. Off keeps picooraclaw file-only.").
					Affirmative("Yes, use Oracle").
					Negative("No, skip").
					Value(&st.oracleEnabled),
			).Title("Storage"),
			show: func() bool { return true },
		},
		{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("Oracle deployment").
					Options(
						huh.NewOption("FreePDB / local database (host, port, service)", "freepdb"),
						huh.NewOption("Autonomous Database (DSN or wallet)", "adb"),
					).
					Value(&st.oracleMode),
			).Title("Oracle deployment"),
			show: func() bool { return st.oracleEnabled },
		},
		{
			group: huh.NewGroup(
				huh.NewInput().
					Title("Host").
					Placeholder("localhost").
					Value(&st.oracleHost).
					Validate(ValidateDefaultedValue(st.oracleHost, validateNotEmpty("host"))),
				huh.NewInput().
					Title("Port").
					Placeholder("1521").
					Value(&st.oraclePort).
					Validate(ValidateDefaultedValue(st.oraclePort, validatePort)),
				huh.NewInput().
					Title("Service name").
					Placeholder("FREEPDB1").
					Value(&st.oracleService).
					Validate(ValidateDefaultedValue(st.oracleService, validateNotEmpty("service"))),
				huh.NewInput().
					Title("User").
					Placeholder("picooraclaw").
					Value(&st.oracleUser).
					Validate(ValidateDefaultedValue(st.oracleUser, validateNotEmpty("user"))),
				huh.NewInput().
					Title("Oracle password").
					Description("Stored in the config file; leave blank to add it later.").
					EchoMode(huh.EchoModePassword).
					Value(&st.oraclePassword),
			).Title("Oracle connection"),
			show: func() bool { return st.isFreePDB() },
		},
		{
			group: huh.NewGroup(
				huh.NewInput().
					Title("Connection DSN").
					Description("Either host:port/service or a full connect descriptor.").
					Placeholder("adb.example.oraclecloud.com:1522/mydb_high").
					Value(&st.oracleDSN).
					Validate(ValidateDefaultedValue(st.oracleDSN, validateOracleDSN)),
				huh.NewInput().
					Title("Wallet directory (optional)").
					Description("Set it for mTLS wallets; leave blank for wallet-less TLS.").
					Value(&st.oracleWallet),
			).Title("Autonomous Database"),
			show: func() bool { return st.isADB() },
		},
		{
			group: huh.NewGroup(
				huh.NewInput().
					Title("Workspace directory").
					Description("Where picooraclaw keeps memories, skills and cron state.").
					Placeholder("~/.picooraclaw/workspace").
					Value(&st.workspace).
					Validate(ValidateDefaultedValue(st.workspace, validateNotEmpty("workspace"))),
				huh.NewConfirm().
					Title("Restrict the agent to its workspace?").
					Description("Recommended: keeps file tools inside the workspace.").
					Affirmative("Yes, restrict").
					Negative("No, allow anywhere").
					Value(&st.restrict),
			).Title("Workspace"),
			show: func() bool { return true },
		},
		{
			group: huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Chat channels to enable").
					Description("Left unset means CLI-only. Tokens are added to the config afterwards.").
					Options(channelChoices()...).
					Value(&st.channels),
				huh.NewConfirm().
					Title("Enable proactive briefs?").
					Description("Adds the 08:00 morning brief and 18:00 end-of-day cron jobs.").
					Affirmative("Enable").
					Negative("Skip").
					Value(&st.proactive),
			).Title("Channels"),
			show: func() bool { return true },
		},
		{
			group: huh.NewGroup(
				huh.NewSelect[string]().
					Title("Embedding provider").
					Description("In-database ONNX needs no extra service. A REST API is only needed if your policies forbid the ONNX model.").
					Options(
						huh.NewOption("Oracle in-database ONNX model (recommended)", "onnx"),
						huh.NewOption("External embedding REST API", "api"),
					).
					Value(&st.embeddings),
				huh.NewInput().
					Title("ONNX model name").
					Placeholder("ALL_MINILM_L12_V2").
					Value(&st.oracleONNX).
					Validate(ValidateDefaultedValue(st.oracleONNX, validateNotEmpty("ONNX model"))),
			).Title("Embeddings"),
			show: func() bool { return st.oracleEnabled },
		},
	}
}

// newWizardState seeds the wizard from the configuration being edited.
func newWizardState(cfg *config.Config) *wizardState {
	st := &wizardState{
		choiceID:       initialChoiceID(cfg),
		model:          cfg.Agents.Defaults.Model,
		oracleEnabled:  cfg.Oracle.Enabled,
		oracleMode:     cfg.Oracle.Mode,
		oracleHost:     cfg.Oracle.Host,
		oraclePort:     strconv.Itoa(cfg.Oracle.Port),
		oracleService:  cfg.Oracle.Service,
		oracleUser:     cfg.Oracle.User,
		oraclePassword: cfg.Oracle.Password,
		oracleDSN:      cfg.Oracle.DSN,
		oracleWallet:   cfg.Oracle.WalletPath,
		oracleONNX:     cfg.Oracle.ONNXModel,
		embeddings:     cfg.Oracle.EmbeddingProvider,
		workspace:      cfg.Agents.Defaults.Workspace,
		restrict:       cfg.Agents.Defaults.RestrictToWorkspace,
		proactive:      cfg.Proactive.Enabled,
	}

	// Credential fields are deliberately NOT pre-filled from the stored config:
	// if the user switches backends, a value carried over from the previous
	// provider would be written for the new one. Stored credentials are instead
	// reported in the field description, and "leave blank" keeps whatever is
	// already on disk.
	if current, ok := choiceByID(st.choiceID); ok {
		st.apiKey = current.presetKey
		if current.defaultModel != "" && st.model == "" {
			st.model = current.defaultModel
		}
	}

	return st
}

// themeForm applies the design tokens and the accessibility switch to a form.
func themeForm(form *huh.Form) *huh.Form {
	return form.
		WithTheme(huh.ThemeFunc(huhstyle.Theme)).
		WithAccessible(huhstyle.Accessible())
}

// runOnboardWizard drives the interactive setup and returns the configuration
// to save. The passed cfg supplies every default so a partially answered form
// still produces a complete, valid config.
func runOnboardWizard(cfg *config.Config) (*config.Config, error) {
	st := newWizardState(cfg)
	steps := wizardSteps(cfg, st)

	if huhstyle.Accessible() {
		if err := runWizardSteps(steps); err != nil {
			return nil, err
		}
		return applyWizard(cfg, st), nil
	}

	groups := make([]*huh.Group, 0, len(steps))
	for _, step := range steps {
		show := step.show
		groups = append(groups, step.group.WithHideFunc(func() bool { return !show() }))
	}
	if err := themeForm(huh.NewForm(groups...)).Run(); err != nil {
		return nil, err
	}

	return applyWizard(cfg, st), nil
}

// runWizardSteps runs one form per applicable page. Accessible mode walks every
// field of every group in a form regardless of visibility, so the pages must be
// selected in Go rather than hidden by huh.
func runWizardSteps(steps []wizardStep) error {
	for _, step := range steps {
		if !step.show() {
			continue
		}
		if err := themeForm(huh.NewForm(step.group)).Run(); err != nil {
			return err
		}
	}
	return nil
}

// applyWizard folds the collected answers into cfg and returns it.
func applyWizard(cfg *config.Config, st *wizardState) *config.Config {
	choice := st.selected()

	cfg.Agents.Defaults.Provider = choice.provider
	cfg.Agents.Defaults.Model = strings.TrimSpace(st.model)
	cfg.Agents.Defaults.Workspace = strings.TrimSpace(st.workspace)
	cfg.Agents.Defaults.RestrictToWorkspace = st.restrict
	cfg.Proactive.Enabled = st.proactive

	// Writing the credential is the point of this wizard: without it the agent
	// cannot reach any hosted backend. A backend with a fixed credential always
	// wins; otherwise only a key the user actually typed is written, so leaving
	// the field blank preserves whatever is already on disk.
	if pc := providerConfigFor(cfg, choice.provider); pc != nil {
		switch {
		case choice.presetKey != "":
			pc.APIKey = choice.presetKey
		case strings.TrimSpace(st.apiKey) != "":
			pc.APIKey = strings.TrimSpace(st.apiKey)
		}
		switch {
		case choice.needAPIBase:
			pc.APIBase = strings.TrimSpace(st.apiBase)
		case choice.apiBase != "":
			pc.APIBase = choice.apiBase
		}
	}

	cfg.Oracle.Enabled = st.oracleEnabled
	cfg.Oracle.Mode = st.oracleMode
	cfg.Oracle.Host = strings.TrimSpace(st.oracleHost)
	if p, err := strconv.Atoi(strings.TrimSpace(st.oraclePort)); err == nil {
		cfg.Oracle.Port = p
	}
	cfg.Oracle.Service = strings.TrimSpace(st.oracleService)
	cfg.Oracle.User = strings.TrimSpace(st.oracleUser)
	cfg.Oracle.Password = st.oraclePassword
	cfg.Oracle.DSN = strings.TrimSpace(st.oracleDSN)
	cfg.Oracle.WalletPath = strings.TrimSpace(st.oracleWallet)
	cfg.Oracle.ONNXModel = strings.TrimSpace(st.oracleONNX)
	cfg.Oracle.EmbeddingProvider = st.embeddings

	for _, name := range st.channels {
		enableChannel(&cfg.Channels, name)
	}

	return cfg
}

// validateModel rejects an empty or whitespace-only model id.
func validateModel(v string) error {
	if strings.TrimSpace(v) == "" {
		return errors.New("a model id is required")
	}
	return nil
}

// validateNotEmpty builds a required-field validator for a named field.
func validateNotEmpty(name string) func(string) error {
	return func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("%s is required", name)
		}
		return nil
	}
}

// validateURL accepts only an http(s) base URL, matching what the
// OpenAI-compatible HTTP provider can dial.
func validateURL(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("a base URL is required")
	}
	if !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
		return errors.New("must start with http:// or https://")
	}
	return nil
}

// validatePort accepts the TCP port range only.
func validatePort(v string) error {
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return errors.New("must be a number")
	}
	if p < 1 || p > 65535 {
		return errors.New("must be between 1 and 65535")
	}
	return nil
}

// validateOracleDSN accepts the two shapes pkg/oracle hands to go-ora: a full
// connect descriptor, or host:port/service.
func validateOracleDSN(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("a DSN is required for Autonomous Database")
	}
	if strings.ContainsAny(v, " \t\n") {
		return errors.New("a DSN cannot contain whitespace")
	}
	if strings.HasPrefix(v, "(") {
		if !strings.Contains(strings.ToUpper(v), "DESCRIPTION") {
			return errors.New("a connect descriptor must contain DESCRIPTION")
		}
		return nil
	}
	if !strings.Contains(v, ":") {
		return errors.New("expected host:port/service or a (DESCRIPTION=...) descriptor")
	}
	return nil
}

// ValidateDefaulted accepts an empty answer as "keep the value already in the field".
//
// It exists for accessible mode. huh's screen-reader path runs a field's validator on the
// raw line and only afterwards substitutes the field's default
// (internal/accessibility/accessibility.go:PromptString returns
// cmp.Or(strings.TrimSpace(input), defaultValue)), and it never prints that default. A
// pre-filled field whose validator rejects "" therefore keeps re-prompting on a bare
// Enter, so a screen-reader user cannot accept a value they cannot see.
func ValidateDefaulted(inner func(string) error) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return inner(s)
	}
}

// ValidateDefaultedValue is ValidateDefaulted for a field whose seeded value may itself
// be empty, which is possible here because the wizard is seeded from a config the user
// may have hand-edited. Blank stays invalid when there is nothing to keep.
func ValidateDefaultedValue(prefilled string, inner func(string) error) func(string) error {
	if strings.TrimSpace(prefilled) == "" {
		return inner
	}
	return ValidateDefaulted(inner)
}

// confirmOverwrite asks whether to clobber an existing config.
//
// Scripted callers must never be blocked: --yes answers affirmatively, and a
// non-terminal stdin declines exactly as the previous fmt.Scanln did.
func confirmOverwrite(opts onboardOptions) (bool, error) {
	if opts.yes {
		return true, nil
	}
	if !huhstyle.Interactive() {
		return false, nil
	}

	var confirmed bool
	// Accessible mode is a Form option, so the single-field question still runs
	// through a form rather than the field's own Run.
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Overwrite the existing configuration?").
				Description(getConfigPath()).
				Affirmative("Overwrite").
				Negative("Keep it").
				Value(&confirmed),
		),
	).
		WithTheme(huh.ThemeFunc(huhstyle.Theme)).
		WithAccessible(huhstyle.Accessible())

	if err := form.Run(); err != nil {
		return false, err
	}
	return confirmed, nil
}

// onboard is the `picooraclaw onboard` entry point.
func onboard() {
	configPath := getConfigPath()

	opts, err := parseOnboardArgs(os.Args[2:])
	if err != nil {
		if errors.Is(err, errOnboardHelp) {
			onboardHelp()
			return
		}
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if _, statErr := os.Stat(configPath); statErr == nil {
		fmt.Printf("Config already exists at %s\n", configPath)
		confirmed, cErr := confirmOverwrite(opts)
		if cErr != nil {
			fmt.Printf("Error: %v\n", cErr)
			os.Exit(1)
		}
		if !confirmed {
			fmt.Println("Aborted. Re-run with --yes to overwrite it.")
			return
		}
	}

	cfg := config.DefaultConfig()

	switch {
	case opts.defaults:
		// Unattended install: the stock config, no questions.
	case !huhstyle.Interactive():
		fmt.Println("No terminal detected - writing the default config.")
		fmt.Println("Re-run `picooraclaw onboard` in a terminal for guided setup.")
	default:
		cfg, err = runOnboardWizard(cfg)
		if err != nil {
			fmt.Printf("Setup aborted: %v\n", err)
			return
		}
	}

	if err := config.SaveConfig(configPath, cfg); err != nil {
		fmt.Printf("Error saving config: %v\n", err)
		os.Exit(1)
	}

	workspace := cfg.WorkspacePath()
	createWorkspaceTemplates(workspace)

	fmt.Printf("%s picooraclaw is ready!\n", logo)
	fmt.Println()
	fmt.Printf("  Provider:  %s\n", cfg.Agents.Defaults.Provider)
	fmt.Printf("  Model:     %s\n", cfg.Agents.Defaults.Model)
	fmt.Printf("  Workspace: %s\n", workspace)
	fmt.Printf("  Config:    %s\n", configPath)
	if cfg.Oracle.Enabled {
		fmt.Printf("  Oracle:    %s (%s)\n", cfg.Oracle.Service, cfg.Oracle.Mode)
	}

	if !providerKeyIsSet(cfg) {
		fmt.Printf("\nNext: add your API key to %s\n", configPath)
	}
	fmt.Println("  Chat: picooraclaw agent -m \"Hello!\"")
}

// providerKeyIsSet reports whether the configured provider already has the
// credential it needs, so onboard only nudges the user when something is
// actually missing.
func providerKeyIsSet(cfg *config.Config) bool {
	// Local and CLI-backed providers work without a key, and the CLI ones have
	// no providers.<name> entry at all, so this check must come first.
	switch cfg.Agents.Defaults.Provider {
	case "ollama", "claude-cli", "codex-cli", "github_copilot":
		return true
	}

	pc := providerConfigFor(cfg, cfg.Agents.Defaults.Provider)
	if pc == nil {
		return false
	}
	if cfg.Agents.Defaults.Provider == "vllm" {
		return pc.APIBase != ""
	}
	return pc.APIKey != ""
}
