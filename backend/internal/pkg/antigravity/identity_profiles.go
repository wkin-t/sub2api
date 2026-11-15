package antigravity

import (
	_ "embed"
	"log"
	"os"
	"strings"
)

// The Antigravity identity block is prepended to every forwarded system
// instruction (the upstream rejects requests without one). Upstream ships the
// older "Advanced Agentic Coding" variant copied from Antigravity-Manager;
// the current official Antigravity CLI (agy 1.5.9, 2026-11) renders its own
// identity from an embedded template and can send either of two variants,
// selected by a server-side experiment flag. Both are accepted upstream, so
// the variant is a deployment choice: a profile lets operators align it with
// the client whose OAuth tokens are in use, without patching code.
//
// Selection order (first non-empty wins):
//  1. admin setting identity_patch_prompt — resolved by the service layer
//     (SettingService.GetIdentityPatchPrompt) and passed in explicitly.
//  2. SUB2API_ANTIGRAVITY_IDENTITY_PROMPT_FILE — path to a file with the prompt.
//  3. SUB2API_ANTIGRAVITY_IDENTITY_PROFILE:
//     agy-cli (default) | agy-cli-generic | agy-cli-coding | legacy | upstream
//  4. built-in default (agy-cli generic identity).
//
// The full agy CLI system instruction is ~20 KB and contains sections about
// client-only capabilities (artifacts, subagents, skills, MCP servers), so it
// is intentionally not the default. Point SUB2API_ANTIGRAVITY_IDENTITY_PROMPT_FILE
// at a captured prompt to experiment with the complete text.
const (
	identityProfileEnv    = "SUB2API_ANTIGRAVITY_IDENTITY_PROFILE"
	identityPromptFileEnv = "SUB2API_ANTIGRAVITY_IDENTITY_PROMPT_FILE"

	identityProfileLegacy     = "legacy"
	identityProfileAGYCLI     = "agy-cli"
	identityProfileAGYGeneric = "agy-cli-generic"
	identityProfileAGYCoding  = "agy-cli-coding"

	// maxIdentityPromptBytes bounds the file override so a misconfigured path
	// cannot push an unbounded body into every upstream request.
	maxIdentityPromptBytes = 256 * 1024
)

//go:embed prompts/agy-cli-1.5.9-identity.txt
var agyCLIIdentityPrompt string

//go:embed prompts/agy-cli-1.5.9-identity-coding.txt
var agyCLICodingIdentityPrompt string

var defaultIdentityPrompt = loadDefaultIdentityPrompt()

func loadDefaultIdentityPrompt() string {
	path := strings.TrimSpace(os.Getenv(identityPromptFileEnv))
	var fileData []byte
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[Antigravity] cannot read identity prompt file %q: %v; falling back to profile", path, err)
		} else {
			fileData = data
		}
	}
	return selectIdentityPrompt(strings.TrimSpace(os.Getenv(identityProfileEnv)), fileData)
}

// selectIdentityPrompt picks the identity prompt for a profile and optional
// file contents. It is separated from loadDefaultIdentityPrompt for testing.
func selectIdentityPrompt(profile string, fileData []byte) string {
	prompt := strings.TrimSpace(string(fileData))
	if prompt != "" {
		if len(prompt) > maxIdentityPromptBytes {
			log.Printf("[Antigravity] identity prompt file too large (%d > %d bytes); ignored", len(prompt), maxIdentityPromptBytes)
		} else {
			return prompt
		}
	}
	switch strings.ToLower(profile) {
	case "", identityProfileAGYCLI, identityProfileAGYGeneric, identityProfileAGYCoding:
		variant := agyCLIIdentityPrompt
		if strings.EqualFold(profile, identityProfileAGYCoding) {
			variant = agyCLICodingIdentityPrompt
		}
		return strings.TrimSpace(variant)
	case identityProfileLegacy, "upstream":
		return antigravityIdentity
	default:
		log.Printf("[Antigravity] unknown %s=%q; using agy-cli identity", identityProfileEnv, profile)
		return strings.TrimSpace(agyCLIIdentityPrompt)
	}
}

// DefaultIdentityPrompt 返回当前生效的内置默认身份提示词。
func DefaultIdentityPrompt() string {
	return defaultIdentityPrompt
}
