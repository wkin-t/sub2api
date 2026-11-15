package antigravity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
//   - agy CLI 1.5.9 plaintext gateway capture (recon/agy-wire-fingerprint.md):
//     CLI labels additionally carry client="antigravity-cli" and the
//     conversation/cascade/root_cascade ids (conversation_id == cascade_id ==
//     root_cascade_id for the root of a conversation).
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

	conversationKindAgent      = "agent"
	conversationKindTrajectory = "trajectory"
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

// WireIdentity scopes the wire identity to one conversation and one serving
// OAuth account.
type WireIdentity struct {
	// ConversationSeed is the client session id when the inbound protocol
	// carries one; empty falls back to a digest of the request contents.
	ConversationSeed string
	// AccountID prevents one cascade from being reused across accounts.
	AccountID int64
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
// systemSalt is extra stable conversation content (normally the system
// instruction text). It distinguishes conversations that start with the same
// user message; without it, two conversations sharing the same opening
// message would collide on one agent/trajectory identity.
//
// The counters are derived from the request contents because sub2api is
// stateless: step approximates the number of trajectory steps, and request_id
// counts prior requests in the trajectory. Exact client counters live in
// client-side session state, which a stateless relay cannot reproduce without
// persisting per-conversation state.
func BuildWireEnvelope(targetModel string, contents []GeminiContent, identity WireIdentity, systemSalt string) WireEnvelope {
	seed := strings.TrimSpace(identity.ConversationSeed)
	if seed == "" {
		seed = deriveConversationSeed(contents, systemSalt)
	}
	seed = seed + "|account:" + strconv.FormatInt(identity.AccountID, 10)

	// CLI 侧实测 conversation_id == cascade_id == root_cascade_id（根 cascade）；
	// requestId 里的 agentId 就是同一个 cascade id。
	conversationID := conversationUUID(seed, conversationKindAgent)
	agentID := conversationID
	trajectoryID := conversationUUID(seed, conversationKindTrajectory)

	step := len(contents)
	if step < 1 {
		step = 1
	}
	requestIndex := (len(contents) - 1) / 2
	usedClaude := isClaudeWireModel(targetModel)
	usedNonGemini := !isGeminiWireModel(targetModel)

	labels := map[string]string{
		"last_step_index":          strconv.Itoa(step - 1),
		"request_id":               trajectoryID + "-" + strconv.Itoa(requestIndex),
		"trajectory_id":            trajectoryID,
		"used_claude":              strconv.FormatBool(usedClaude),
		"used_claude_conservative": strconv.FormatBool(usedClaude), // 真机样本中与 used_claude 恒等
		"used_non_gemini_model":    strconv.FormatBool(usedNonGemini),
	}
	if enum := modelEnumForWireModel(targetModel); enum != "" {
		labels["model_enum"] = enum
	}

	// agy CLI 的 wire 身份额外带 client 与 cascade 组；默认 hub 形态按
	// Windows 原生客户端抓包保持 8 个 label，不臆造 cascade 字段。
	if antigravityUserAgentProfile == userAgentFamilyAGYCLI {
		labels["client"] = "antigravity-cli"
		labels["conversation_id"] = conversationID
		labels["cascade_id"] = agentID
		labels["root_cascade_id"] = agentID
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

// deriveConversationSeed produces a stable seed from the first user text and
// the system instruction, so a client that does not send a session header still
// keeps one identity across the turns of a conversation while different
// conversations (different system prompts) stay apart.
func deriveConversationSeed(contents []GeminiContent, systemSalt string) string {
	h := sha256.New()
	h.Write([]byte(firstUserText(contents)))
	h.Write([]byte{0})
	h.Write([]byte(systemSalt))
	return hex.EncodeToString(h.Sum(nil))
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

// SystemInstructionText joins the text parts of a systemInstruction so it can
// salt the conversation seed.
func SystemInstructionText(content *GeminiContent) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
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
// JSON object: {"gemini-4-hydrogen":"MODEL_PLACEHOLDER_M462"}.
const antigravityModelEnumEnv = "SUB2API_ANTIGRAVITY_MODEL_ENUM_MAP"

// modelEnumByWireModel holds the model_enum labels observed in captures.
// M298/M35 come from the Antigravity-Manager Windows capture document;
// M462 <-> gemini-4-hydrogen was observed locally through the agy plaintext
// gateway. The backend accepts requests without the label (oh-my-pi verifies
// this live), so unknown models simply omit it.
var modelEnumByWireModel = loadModelEnumMap()

func loadModelEnumMap() map[string]string {
	out := map[string]string{
		"gemini-3.7-flash-high": "MODEL_PLACEHOLDER_M298",
		"gemini-4-hydrogen":     "MODEL_PLACEHOLDER_M462",
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
	return strings.HasPrefix(model, "gemini-")
}

// WrapGeminiRequest injects the wire identity into a raw Gemini request body
// and wraps it in the v1internal envelope. Unknown request fields are preserved
// as raw JSON; the request object is re-marshalled so its keys sort
// alphabetically (the official client keeps a fixed order, which a JSON map
// cannot express — documented gap).
func WrapGeminiRequest(projectID, requestType, model string, body []byte, identity WireIdentity) ([]byte, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, fmt.Errorf("antigravity: project id is required")
	}

	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("antigravity: parse request body: %w", err)
	}

	var contents []GeminiContent
	if raw, ok := request["contents"]; ok && raw != nil {
		if err := json.Unmarshal(raw, &contents); err != nil {
			return nil, fmt.Errorf("antigravity: parse contents: %w", err)
		}
	}
	var systemSalt string
	if raw, ok := request["systemInstruction"]; ok && raw != nil {
		var instruction GeminiContent
		if err := json.Unmarshal(raw, &instruction); err == nil {
			systemSalt = SystemInstructionText(&instruction)
		}
	}

	envelope := BuildWireEnvelope(model, contents, identity, systemSalt)
	request["sessionId"] = mustMarshalJSON(envelope.SessionID)
	request["labels"] = mustMarshalJSON(envelope.Labels)

	wrapped := struct {
		Project     string                     `json:"project"`
		UserAgent   string                     `json:"userAgent"`
		RequestType string                     `json:"requestType"`
		Model       string                     `json:"model"`
		RequestID   string                     `json:"requestId"`
		Request     map[string]json.RawMessage `json:"request"`
	}{
		Project:     projectID,
		UserAgent:   "antigravity", // 固定值，与官方客户端一致
		RequestType: requestType,
		Model:       model,
		RequestID:   envelope.RequestID,
		Request:     request,
	}
	return json.Marshal(wrapped)
}

func mustMarshalJSON(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return raw
}
