package antigravity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Antigravity wire identity.
//
// Sources (all from captured real client traffic, see
// docs/ANTIGRAVITY_429_AND_IDENTITY_PROFILES.md):
//   - docs/JEIKL_ANTIGRAVITY_CLIENT_CAPTURE.md in lbjlaq/Antigravity-Manager
//     (three Windows native client captures):
//     inner request key order contents / systemInstruction / tools / labels /
//     generationConfig / sessionId, requestId "agent/<agentId>/<step>",
//     sessionId constant "-3750763034362895579" (int64 of the FNV-1a 64
//     empty-string offset basis 0xcbf29ce484222325),
//     labels {last_execution_id,last_step_index,model_enum,request_id,
//     trajectory_id,used_claude,used_claude_conservative,used_non_gemini_model}.
//   - oh-my-pi packages/ai/src/providers/google-gemini-cli.ts
//     (buildAntigravityRequestEnvelope): requestId format, monotonic step,
//     labels derived from a per-conversation agent/trajectory identity.
//
// The real client mints one agent (cascade) + trajectory identity per app
// instance. A relay that reuses one identity across several OAuth accounts
// tells the provider those accounts are the same client, so the identity is
// derived from (client session, serving account) instead of being global.
const (
	sessionIDModeEnv = "SUB2API_ANTIGRAVITY_SESSION_ID_MODE"

	sessionIDModeOfficial = "official"
	sessionIDModeContent  = "content"
	sessionIDModeRandom   = "random"

	// OfficialAntigravitySessionID is the constant sessionId the current
	// Antigravity client sends: "-3750763034362895579" (FNV-1a 64 of "").
	OfficialAntigravitySessionID = "-3750763034362895579"
)

// wireIdentityNamespace is a fixed UUID namespace for deriving stable
// agent/trajectory ids from a conversation seed.
var wireIdentityNamespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

var sessionIDMode = resolveSessionIDMode()

func resolveSessionIDMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(sessionIDModeEnv))) {
	case "", sessionIDModeOfficial:
		return sessionIDModeOfficial
	case sessionIDModeContent, "legacy":
		return sessionIDModeContent
	case sessionIDModeRandom:
		return sessionIDModeRandom
	default:
		log.Printf("[Antigravity] unknown %s=%q; using official session id", sessionIDModeEnv, os.Getenv(sessionIDModeEnv))
		return sessionIDModeOfficial
	}
}

// WireEnvelope is the per-request identity the client attaches to a
// v1internal request.
type WireEnvelope struct {
	RequestID string
	SessionID string
	Labels    map[string]string
}

// BuildWireEnvelope builds the envelope for one request.
//
// conversationSeed is the client session id if the inbound protocol carries
// one (empty falls back to a hash of the first user message), accountID scopes
// the identity to the serving OAuth account so a credential rotation starts a
// fresh cascade instead of linking accounts.
//
// The counters are derived from the request contents because sub2api is
// stateless: step approximates the number of trajectory steps (each content
// entry is one step, matching the 1-step-first-request / 3-steps-after-tool
// observation of the real client), and request_id counts prior requests in the
// trajectory. Exact client counters live in client-side session state, which a
// stateless relay cannot reproduce without persisting per-conversation state.
func BuildWireEnvelope(targetModel string, contents []GeminiContent, conversationSeed string, accountID int64) WireEnvelope {
	seed := strings.TrimSpace(conversationSeed)
	if seed == "" {
		seed = deriveConversationSeed(contents)
	}
	seed = seed + "|account:" + strconv.FormatInt(accountID, 10)

	agentID := conversationUUID(seed, "agent")
	trajectoryID := conversationUUID(seed, "trajectory")

	step := len(contents)
	if step < 1 {
		step = 1
	}
	requestIndex := (len(contents) - 1) / 2
	if requestIndex < 0 {
		requestIndex = 0
	}
	usedClaude := isClaudeWireModel(targetModel)
	usedNonGemini := !isGeminiWireModel(targetModel)

	labels := map[string]string{
		"last_step_index":          strconv.Itoa(step - 1),
		"request_id":               trajectoryID + "-" + strconv.Itoa(requestIndex),
		"trajectory_id":            trajectoryID,
		"used_claude":              strconv.FormatBool(usedClaude),
		"used_claude_conservative": strconv.FormatBool(usedClaude),
		"used_non_gemini_model":    strconv.FormatBool(usedNonGemini),
	}
	if enum := modelEnumForWireModel(targetModel); enum != "" {
		labels["model_enum"] = enum
	}

	return WireEnvelope{
		RequestID: "agent/" + agentID + "/" + strconv.Itoa(step),
		SessionID: sessionIDForMode(sessionIDMode, contents),
		Labels:    labels,
	}
}

func conversationUUID(seed, kind string) string {
	return uuid.NewSHA1(wireIdentityNamespace, []byte("sub2api/antigravity/"+kind+":"+seed)).String()
}

// deriveConversationSeed produces a stable seed from the first user text so a
// client that does not send a session header still keeps one identity across
// the turns of a conversation.
func deriveConversationSeed(contents []GeminiContent) string {
	for _, content := range contents {
		if content.Role != "user" {
			continue
		}
		for _, part := range content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

// sessionIDForMode returns the sessionId value for the configured mode.
func sessionIDForMode(mode string, contents []GeminiContent) string {
	switch mode {
	case sessionIDModeContent:
		return contentDerivedSessionID(contents)
	case sessionIDModeRandom:
		return randomSignedDecimalSessionID()
	default:
		return OfficialAntigravitySessionID
	}
}

// contentDerivedSessionID mirrors the legacy behaviour: "-" + the first 8
// bytes of SHA-256(first user text) masked to 63 bits.
func contentDerivedSessionID(contents []GeminiContent) string {
	text := firstUserText(contents)
	if text == "" {
		return randomSignedDecimalSessionID()
	}
	sum := sha256.Sum256([]byte(text))
	value := binary.BigEndian.Uint64(sum[:8]) & 0x7FFFFFFFFFFFFFFF
	return "-" + strconv.FormatUint(value, 10)
}

func firstUserText(contents []GeminiContent) string {
	for _, content := range contents {
		if content.Role != "user" {
			continue
		}
		for _, part := range content.Parts {
			if text := strings.TrimSpace(part.Text); text != "" {
				return text
			}
		}
	}
	return ""
}

// randomSignedDecimalSessionID returns a random 63-bit value as "-<decimal>",
// mirroring the client's signed-decimal format.
func randomSignedDecimalSessionID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return OfficialAntigravitySessionID
	}
	value := binary.BigEndian.Uint64(buf[:]) & 0x7FFFFFFFFFFFFFFF
	return "-" + strconv.FormatUint(value, 10)
}

// antigravityModelEnumEnv optionally extends the model_enum label map with a
// JSON object: {"gemini-3.8-flash-high":"MODEL_PLACEHOLDER_M298"}.
const antigravityModelEnumEnv = "SUB2API_ANTIGRAVITY_MODEL_ENUM_MAP"

// modelEnumByWireModel holds the model_enum labels observed in real client
// captures. The backend accepts requests without the label (oh-my-pi verifies
// this live), so unknown models simply omit it.
var modelEnumByWireModel = loadModelEnumMap()

func loadModelEnumMap() map[string]string {
	out := map[string]string{
		"gemini-3.7-flash-high": "MODEL_PLACEHOLDER_M298",
		"claude-sonnet-4-6":     "MODEL_PLACEHOLDER_M35",
	}
	if raw := strings.TrimSpace(os.Getenv(antigravityModelEnumEnv)); raw != "" {
		var extra map[string]string
		if err := json.Unmarshal([]byte(raw), &extra); err != nil {
			log.Printf("[Antigravity] ignoring invalid %s: %v", antigravityModelEnumEnv, err)
		} else {
			for model, enum := range extra {
				out[model] = enum
			}
		}
	}
	return out
}

func modelEnumForWireModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	if enum, ok := modelEnumByWireModel[model]; ok {
		return enum
	}
	return ""
}

func isClaudeWireModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "claude-")
}

func isGeminiWireModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "gemma-")
}
