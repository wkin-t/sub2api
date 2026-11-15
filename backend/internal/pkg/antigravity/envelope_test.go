package antigravity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func testContents(texts ...string) []GeminiContent {
	contents := make([]GeminiContent, 0, len(texts))
	for i, text := range texts {
		role := "user"
		if i%2 == 1 {
			role = "model"
		}
		contents = append(contents, GeminiContent{Role: role, Parts: []GeminiPart{{Text: text}}})
	}
	return contents
}

func TestOfficialSessionIDIsFNV1a64OfEmptyString(t *testing.T) {
	// 0xcbf29ce484222325 as a signed int64.
	require.Equal(t, "-3750763034362895579", OfficialAntigravitySessionID)
	require.Equal(t, OfficialAntigravitySessionID, sessionIDForMode(sessionIDModeOfficial, testContents("hi")))
}

func TestBuildWireEnvelope_OfficialFormat(t *testing.T) {
	env := BuildWireEnvelope("gemini-3.7-flash-high", testContents("hello", "world", "again"), "session-1", 42)

	require.Regexp(t, `^agent/[0-9a-f-]{36}/3$`, env.RequestID)
	require.Equal(t, OfficialAntigravitySessionID, env.SessionID)
	require.Equal(t, "2", env.Labels["last_step_index"])
	require.Equal(t, "MODEL_PLACEHOLDER_M298", env.Labels["model_enum"])
	require.Equal(t, "false", env.Labels["used_claude"])
	require.Equal(t, "false", env.Labels["used_non_gemini_model"])
	require.Regexp(t, `^[0-9a-f-]{36}$`, env.Labels["trajectory_id"])
	require.Contains(t, env.Labels["request_id"], env.Labels["trajectory_id"]+"-")
	require.NotContains(t, env.Labels, "cascade_id")
	require.NotContains(t, env.Labels, "root_cascade_id")
}

func TestBuildWireEnvelope_ClaudeLabels(t *testing.T) {
	env := BuildWireEnvelope("claude-sonnet-4-6", testContents("hello"), "session-1", 42)

	require.Equal(t, "MODEL_PLACEHOLDER_M35", env.Labels["model_enum"])
	require.Equal(t, "true", env.Labels["used_claude"])
	require.Equal(t, "true", env.Labels["used_claude_conservative"])
	require.Equal(t, "true", env.Labels["used_non_gemini_model"])
	require.Equal(t, "0", env.Labels["last_step_index"])
}

func TestBuildWireEnvelope_IdentityIsStablePerConversationAndAccount(t *testing.T) {
	contents := testContents("hello", "answer", "next")
	first := BuildWireEnvelope("gemini-3.7-flash-high", contents, "session-1", 42)
	second := BuildWireEnvelope("gemini-3.7-flash-high", contents, "session-1", 42)
	require.Equal(t, first.RequestID, second.RequestID)
	require.Equal(t, first.Labels["trajectory_id"], second.Labels["trajectory_id"])

	// A different account must not reuse the same cascade (would link accounts).
	otherAccount := BuildWireEnvelope("gemini-3.7-flash-high", contents, "session-1", 43)
	require.NotEqual(t, first.RequestID, otherAccount.RequestID)
	require.NotEqual(t, first.Labels["trajectory_id"], otherAccount.Labels["trajectory_id"])

	// A different conversation gets a different identity as well.
	otherConversation := BuildWireEnvelope("gemini-3.7-flash-high", contents, "session-2", 42)
	require.NotEqual(t, first.RequestID, otherConversation.RequestID)
}

func TestBuildWireEnvelope_DerivesSeedWhenNoSessionHeader(t *testing.T) {
	contents := testContents("first user text", "answer", "follow up")
	a := BuildWireEnvelope("gemini-3.7-flash-high", contents, "", 42)
	b := BuildWireEnvelope("gemini-3.7-flash-high", contents, "", 42)
	require.Equal(t, a.RequestID, b.RequestID)
}

func TestSessionIDModes(t *testing.T) {
	contents := testContents("some user text")

	require.Equal(t, OfficialAntigravitySessionID, sessionIDForMode(sessionIDModeOfficial, contents))

	content := sessionIDForMode(sessionIDModeContent, contents)
	require.Regexp(t, `^-\d+$`, content)
	require.Equal(t, content, sessionIDForMode(sessionIDModeContent, contents))
	require.NotEqual(t, content, sessionIDForMode(sessionIDModeContent, testContents("other text")))

	random := sessionIDForMode(sessionIDModeRandom, contents)
	require.Regexp(t, `^-\d+$`, random)
}

func TestTransformClaudeToGemini_WireEnvelope(t *testing.T) {
	req := &ClaudeRequest{
		Model:  "gemini-3.7-flash-high",
		System: json.RawMessage(`"system"`),
		Messages: []ClaudeMessage{
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", Content: json.RawMessage(`"hi"`)},
			{Role: "user", Content: json.RawMessage(`"again"`)},
		},
	}

	body, err := TransformClaudeToGeminiWithOptions(req, "project", "gemini-3.7-flash-high", TransformOptions{ConversationSeed: "seed", AccountID: 7})
	require.NoError(t, err)

	// Envelope keys must appear in the real client's order.
	ordered := []string{"project", "userAgent", "requestType", "model", "requestId", "request"}
	last := -1
	for _, key := range ordered {
		index := strings.Index(string(body), `"`+key+`":`)
		require.NotEqual(t, -1, index, "missing envelope key %q", key)
		require.Greater(t, index, last, "envelope key %q is out of order", key)
		last = index
	}

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	requestID := gjsonString(t, raw["requestId"])
	require.Regexp(t, `^agent/[0-9a-f-]{36}/3$`, requestID)

	var inner map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["request"], &inner))
	require.Equal(t, `"`+OfficialAntigravitySessionID+`"`, string(inner["sessionId"]))
	require.Contains(t, string(inner["labels"]), `"model_enum":"MODEL_PLACEHOLDER_M298"`)
	require.Contains(t, string(inner["labels"]), `"used_claude":"false"`)
	require.Contains(t, string(inner["labels"]), `"trajectory_id":"`)
	require.Contains(t, string(inner["labels"]), `"request_id":"`)
	require.Contains(t, string(inner["labels"]), `"last_step_index":"2"`)
}

func gjsonString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}
