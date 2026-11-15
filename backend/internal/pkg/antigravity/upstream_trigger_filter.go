package antigravity

import (
	"log"
	"os"
	"regexp"
	"strings"
)

// The Antigravity upstream (cloudcode-pa) answers some requests with a bare
// 429 RESOURCE_EXHAUSTED: no quotaResetDelay, no RetryInfo, body
// "Resource has been exhausted (e.g. check quota)." — and the account is
// otherwise idle. Controlled experiments show this is a content rule rather
// than a quota: with the same key, endpoint and model, only the system
// instruction changes between the rejected and the accepted request.
//
// The triggers are a rolling sentence blacklist of third-party client
// identities and policy sentences, so this filter removes the sentences that
// are known to trip it and lets operators add new ones without a rebuild.
//
// Known triggers (all reproduced with controlled experiments):
//   - "You are Hermes Agent, an intelligent AI assistant created by Nous
//     Research." (CLIProxyAPI #4723, 9router #3358)
//   - Claude Code's "Dual-use security tools (...) require clear authorization
//     context: ..." sentence (sub2api #8824, PR #8829)
//
// Client identity openers that start a system block (Claude Agent SDK, Claude
// Code, Codex, Pi, Hermes, ...) are rewritten by systemIdentityOpeners in
// request_transformer.go; this filter catches trigger sentences that can
// appear anywhere inside a block.
const stripPatternsEnv = "SUB2API_ANTIGRAVITY_STRIP_PATTERNS"

var defaultUpstreamTriggerPatterns = []string{
	`(?i)You are Hermes Agent, an intelligent AI assistant created by Nous Research\.?[ \t]*`,
	`(?i)Dual-use security tools \([^)]*\) require clear authorization context:[^.\n]*\.[ \t]*`,
}

var upstreamTriggerPatterns = compileUpstreamTriggerPatterns(os.Getenv(stripPatternsEnv))

func compileUpstreamTriggerPatterns(extra string) []*regexp.Regexp {
	sources := append([]string(nil), defaultUpstreamTriggerPatterns...)
	for _, line := range strings.Split(extra, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			sources = append(sources, line)
		}
	}
	patterns := make([]*regexp.Regexp, 0, len(sources))
	for _, src := range sources {
		re, err := regexp.Compile(src)
		if err != nil {
			log.Printf("[Antigravity] ignoring invalid strip pattern %q: %v", src, err)
			continue
		}
		patterns = append(patterns, re)
	}
	return patterns
}

// StripUpstreamTriggerSentences removes sentences that the Antigravity
// upstream is known to reject with a fake 429. Only Antigravity forwarding
// should use it, so user content and tool schemas stay untouched.
func StripUpstreamTriggerSentences(text string) string {
	for _, re := range upstreamTriggerPatterns {
		text = re.ReplaceAllString(text, "")
	}
	return text
}
