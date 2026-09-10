package apikey

import (
	"fmt"
	"strings"
)

// PlaceholderToken stands in for a real key when the settings page shows what
// setup will look like before anybody has created one.
//
// Deliberately not shaped like a real token. A convincing placeholder is how
// somebody copies the preview, pastes it into a config, and spends an hour on a
// key that never existed — so this says what it is, loudly, in the one place a
// person's eye lands when they check whether they pasted the right thing.
const PlaceholderToken = "PASTE_YOUR_KEY_HERE__create_one_below"

// TokenEnvVar is the environment variable the git-safe configurations read.
const TokenEnvVar = "LOCI_MCP_TOKEN"

// desktopHeaderEnvVar is the variable Claude Desktop's no-space form reads.
//
// It holds the whole header value — "Bearer <token>" — not the token, because
// mcp-remote splices it into an argument that must contain no space. Named
// differently from TokenEnvVar so the two cannot be confused: exporting a bare
// token under this name would send "Authorization: loci_sk_…" and fail.
const desktopHeaderEnvVar = "LOCI_MCP_AUTH"

// Setup is everything a user needs to point one agent at Loci: the config a
// person edits, and the prompt a person hands to the agent instead.
//
// A Setup built with a real token is returned only in the response to the
// request that created the key, and never stored.
type Setup struct {
	Kind     ClientKind
	Endpoint string

	// ConfigLabel names what the snippet is, and ConfigLang the syntax it is
	// written in — used for the copy block's heading and highlighting.
	ConfigLabel string
	ConfigLang  string
	Config      string

	// Safe is the same configuration with the token read from an environment
	// variable instead of written into the file, and Export is the line that
	// sets it. Empty for clients whose config is not a file somebody commits.
	//
	// Not an advanced option: .mcp.json lives in a project root and is
	// routinely committed, so offering only the literal form is how a token
	// reaches a public repository.
	SafeLabel string
	SafeLang  string
	Safe      string
	SafeNote  string
	Export    string

	// Prompt is the client-agnostic instruction to paste into an agent that is
	// already running, for people who would rather not open a config file.
	Prompt string
}

// SetupWriter renders setup instructions.
//
// It is given the endpoint and the tool names rather than reading them from
// internal/mcp, which imports this package: taking them as data is what keeps
// that from becoming a cycle. The composition root supplies them from the one
// place that defines them, so they cannot drift.
type SetupWriter struct {
	endpoint       string
	readOnlyTools  []string
	writingTools   []string
	generatingTool []string
}

// NewSetupWriter builds a writer for one server address.
//
// baseURL is the server's public address and mcpPath where the MCP endpoint is
// mounted; both come from the composition root.
func NewSetupWriter(baseURL, mcpPath string, readOnly, writing, generating []string) *SetupWriter {
	return &SetupWriter{
		endpoint:       strings.TrimRight(baseURL, "/") + mcpPath,
		readOnlyTools:  readOnly,
		writingTools:   writing,
		generatingTool: generating,
	}
}

// Endpoint is the MCP address these instructions point at.
func (w *SetupWriter) Endpoint() string { return w.endpoint }

// Preview renders the setup for a client nobody has a key for yet.
//
// The same instructions the real thing produces, with PlaceholderToken where
// the credential goes. It exists so the page can answer "what am I signing up
// for" without issuing a credential to answer it — minting a key to find out
// what setup looks like leaves a live credential behind for a question.
func (w *SetupWriter) Preview(kind ClientKind) Setup {
	return w.Instructions(kind, PlaceholderToken)
}

// Instructions renders the setup for one issued token.
func (w *SetupWriter) Instructions(kind ClientKind, token string) Setup {
	if !kind.Valid() {
		kind = ClientOther
	}

	setup := Setup{
		Kind:     kind,
		Endpoint: w.endpoint,
		Prompt:   w.prompt(token),
	}

	switch kind {
	case ClientClaudeCode:
		// The CLI first: it needs no file, no path, and no JSON, which is the
		// whole difficulty for someone who has not edited one before.
		setup.ConfigLabel = "one command"
		setup.ConfigLang = "bash"
		setup.Config = fmt.Sprintf(`claude mcp add --transport http loci %s \
  --header "Authorization: Bearer %s"`, w.endpoint, token)

		setup.SafeLabel = ".mcp.json"
		setup.SafeLang = "json"
		setup.SafeNote = "If that file lives in a git repository, use this version instead and keep " +
			"the key in your environment — .mcp.json is committed more often than people expect."
		setup.Safe = fmt.Sprintf(`{
  "mcpServers": {
    "loci": {
      "type": "http",
      "url": %q,
      "headers": {
        "Authorization": "Bearer ${%s}"
      }
    }
  }
}`, w.endpoint, TokenEnvVar)
		setup.Export = "export " + TokenEnvVar + "=" + token

	case ClientClaudeDesktop:
		// Claude Desktop cannot dial an HTTP MCP server with a static bearer
		// header itself; its remote connectors want OAuth. mcp-remote is the
		// documented stdio bridge: Desktop runs it as a local server and it
		// forwards to Loci with the header. It defaults to trying streamable
		// HTTP first, which is what Loci serves, so no --transport flag.
		//
		// Verified against mcp-remote 0.8.6: `--header "Name: value"` is the
		// flag, ${VAR} in an argument is read from the "env" map, and the
		// no-space form below is its own documented workaround for Claude
		// Desktop on Windows, where spaces inside args are not escaped.
		setup.ConfigLabel = "claude_desktop_config.json"
		setup.ConfigLang = "json"
		setup.Config = fmt.Sprintf(`{
  "mcpServers": {
    "loci": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote", %q,
        "--header", "Authorization: Bearer %s"
      ]
    }
  }
}`, w.endpoint, token)

		// Not the git-safe form the other clients have: this file lives in
		// Application Support, and Desktop is launched from the dock, so it
		// never sees a variable you export — the "env" block is the only way
		// to hand it one. The variable therefore carries the whole header
		// value, "Bearer" included, because the argument it is spliced into
		// must not contain a space. A different name from TokenEnvVar so
		// nobody exports the bare token under it and gets a 401 with no clue.
		setup.SafeLabel = "claude_desktop_config.json (Windows)"
		setup.SafeLang = "json"
		setup.SafeNote = "On Windows, Claude Desktop mangles spaces inside args, so the header " +
			"cannot be passed as one string. Use this form instead: the header value goes in " +
			"the env block and the argument has no space in it. The file is the same one: " +
			"macOS ~/Library/Application Support/Claude/, Windows %APPDATA%\\Claude\\."
		setup.Safe = fmt.Sprintf(`{
  "mcpServers": {
    "loci": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote", %q,
        "--header", "Authorization:${%s}"
      ],
      "env": {
        %q: "Bearer %s"
      }
    }
  }
}`, w.endpoint, desktopHeaderEnvVar, desktopHeaderEnvVar, token)

	case ClientCursor:
		// Cursor reads the same shape as Claude Code's .mcp.json: a url and a
		// headers map, with the transport inferred from the url. The safe form
		// uses Cursor's own ${env:NAME} interpolation, which is its documented
		// syntax rather than the shell's ${NAME}.
		//
		// The headers map is documented in Cursor's MCP guide; the ${env:}
		// form is from the same page and has not been exercised against a
		// Cursor release here. If it turns out not to interpolate, the literal
		// form above it still works — the snippet degrades to needing the key
		// in the file, not to being wrong.
		setup.ConfigLabel = "~/.cursor/mcp.json"
		setup.ConfigLang = "json"
		setup.Config = fmt.Sprintf(`{
  "mcpServers": {
    "loci": {
      "url": %q,
      "headers": {
        "Authorization": "Bearer %s"
      }
    }
  }
}`, w.endpoint, token)

		setup.SafeLabel = ".cursor/mcp.json"
		setup.SafeLang = "json"
		setup.SafeNote = "A project-level .cursor/mcp.json is committed with the project. If you " +
			"put it there rather than in your home directory, use this version and keep the " +
			"key in your environment."
		setup.Safe = fmt.Sprintf(`{
  "mcpServers": {
    "loci": {
      "url": %q,
      "headers": {
        "Authorization": "Bearer ${env:%s}"
      }
    }
  }
}`, w.endpoint, TokenEnvVar)
		setup.Export = "export " + TokenEnvVar + "=" + token

	case ClientCodex:
		// Codex reads the credential from a named environment variable rather
		// than taking it inline, so its config file never contains the key.
		// That makes this the one client where the git-safe form is the only
		// form — there is no literal variant to offer or to warn about.
		//
		// Verified against codex-cli 0.147.0: `codex mcp add --url` writes
		// exactly the TOML below, and the flag is --bearer-token-env-var.
		// There is no --header option for HTTP servers.
		setup.ConfigLabel = "one command"
		setup.ConfigLang = "bash"
		setup.Config = fmt.Sprintf(`codex mcp add loci --url %s --bearer-token-env-var %s`,
			w.endpoint, TokenEnvVar)

		setup.SafeLabel = "~/.codex/config.toml"
		setup.SafeLang = "toml"
		setup.SafeNote = "That command writes this, which you can also write yourself. Codex reads " +
			"the key from the environment, so the file never contains it."
		setup.Safe = fmt.Sprintf(`[mcp_servers.loci]
url = %q
bearer_token_env_var = %q`, w.endpoint, TokenEnvVar)
		setup.Export = "export " + TokenEnvVar + "=" + token

	default:
		// Hermes lands here too, deliberately. Every other snippet in this file
		// was checked against a released client, and inventing a `hermes mcp
		// add` form from memory is exactly how somebody spends an afternoon on
		// a command that was never real. The connection details below work with
		// any MCP client, including Hermes; replace this with a CLI form once
		// one has been run against a Hermes release.
		setup.ConfigLabel = "connection details"
		setup.ConfigLang = "text"
		setup.Config = fmt.Sprintf(`Transport: streamable HTTP
URL:       %s
Header:    Authorization: Bearer %s`, w.endpoint, token)
	}

	return setup
}

// prompt is the text a user pastes into an agent so the agent edits its own
// configuration.
//
// It repeats the two rules an agent left to itself gets wrong — credentials in
// headers rather than URLs, and one registration rather than several — and it
// names a read-only check to run, because an agent that verifies a new
// connection by planning an itinerary has spent the owner's daily quota to
// prove it could.
func (w *SetupWriter) prompt(token string) string {
	// One line per paragraph, deliberately unwrapped: this is displayed in a
	// narrow column that soft-wraps it, and hard breaks sized for a terminal
	// wrap a second time and leave orphans mid-sentence. Nothing downstream
	// wants the breaks either — the string is pasted into an agent, where line
	// length carries no meaning.
	//
	// The transport block keeps its own lines, because the alignment there is
	// the point.
	lines := []string{
		`Add an MCP server called "loci" to your own configuration, then confirm it works.`,
		``,
		`  Transport: streamable HTTP`,
		`  URL:       ` + w.endpoint,
		`  Header:    Authorization: Bearer ` + token,
		``,
		`Put the credential in a header, never in the URL — URLs end up in logs. Do not register Loci a second time under another name; duplicate tool names across MCP servers make tool selection unpredictable.`,
	}

	if len(w.readOnlyTools) > 0 {
		check := fmt.Sprintf(`Then verify by listing the tools: you should see %s. Report what you found.`,
			joinNames(w.readOnlyTools))
		if avoid := append(append([]string(nil), w.writingTools...), w.generatingTool...); len(avoid) > 0 {
			check += fmt.Sprintf(` Do not call any tool that writes or generates (%s) as part of this check.`,
				strings.Join(avoid, ", "))
		}
		lines = append(lines, ``, check)
	}

	lines = append(lines, ``,
		`Once connected: every call acts as one account — mine. Ask before writing, and before planning an itinerary, since that spends my daily quota.`)

	return strings.Join(lines, "\n")
}

// joinNames writes a list the way a person would read it.
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
