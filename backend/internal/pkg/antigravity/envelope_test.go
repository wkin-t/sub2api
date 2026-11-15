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

func testIdentity(seed string, accountID int64) WireIdentity {
	return WireIdentity{ConversationSeed: seed, AccountID: accountID}
}

func TestOfficialSessionIDIsFNV1a64OfEmptyString(t *testing.T) {
	// 0xcbf29ce484222325 as a signed int64.
	require.Equal(t, "-3750763034362895579", OfficialAntigravitySessionID)
	require.Equal(t, OfficialAntigravitySessionID, sessionIDForMode(sessionIDModeOfficial, testContents("hi")))
}

func TestBuildWireEnvelope_OfficialFormat(t *testing.T) {
	env := BuildWireEnvelope("gemini-3.7-flash-high", testContents("hello", "world", "again"), testIdentity("session-1", 42), "system")

	require.Regexp(t, `^agent/[0-9a-f-]{36}/3$`, env.RequestID)
	require.Equal(t, OfficialAntigravitySessionID, env.SessionID)
	require.Equal(t, "2", env.Labels["last_step_index"])
	require.Equal(t, "MODEL_PLACEHOLDER_M298", env.Labels["model_enum"])
	require.Equal(t, "false", env.Labels["used_claude"])
	require.Equal(t, "false", env.Labels["used_non_gemini_model"])
	require.Regexp(t, `^[0-9a-f-]{36}$`, env.Labels["trajectory_id"])
	require.Contains(t, env.Labels["request_id"], env.Labels["trajectory_id"]+"-")
	// hub 形态按 Windows 原生客户端抓包保持 8 个 label，没有 cascade/client 组。
	require.NotContains(t, env.Labels, "cascade_id")
	require.NotContains(t, env.Labels, "root_cascade_id")
	require.NotContains(t, env.Labels, "client")
}

func TestBuildWireEnvelope_AGYCLILabels(t *testing.T) {
	previous := antigravityUserAgentProfile
	antigravityUserAgentProfile = userAgentFamilyAGYCLI
	t.Cleanup(func() { antigravityUserAgentProfile = previous })

	env := BuildWireEnvelope("gemini-3.7-flash-high", testContents("hello"), testIdentity("session-1", 42), "system")

	require.Equal(t, "antigravity-cli", env.Labels["client"])
	require.Regexp(t, `^[0-9a-f-]{36}$`, env.Labels["conversation_id"])
	require.Equal(t, env.Labels["conversation_id"], env.Labels["cascade_id"])
	require.Equal(t, env.Labels["conversation_id"], env.Labels["root_cascade_id"])
}

func TestBuildWireEnvelope_ClaudeLabels(t *testing.T) {
	env := BuildWireEnvelope("claude-sonnet-4-6", testContents("hello"), testIdentity("session-1", 42), "system")

	require.Equal(t, "MODEL_PLACEHOLDER_M35", env.Labels["model_enum"])
	require.Equal(t, "true", env.Labels["used_claude"])
	require.Equal(t, "true", env.Labels["used_claude_conservative"])
	require.Equal(t, "true", env.Labels["used_non_gemini_model"])
	require.Equal(t, "0", env.Labels["last_step_index"])
}

func TestBuildWireEnvelope_IdentityIsStablePerConversationAndAccount(t *testing.T) {
	contents := testContents("hello", "answer", "next")
	first := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("session-1", 42), "system")
	second := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("session-1", 42), "system")
	require.Equal(t, first.RequestID, second.RequestID)
	require.Equal(t, first.Labels["trajectory_id"], second.Labels["trajectory_id"])

	// A different account must not reuse the same cascade (would link accounts).
	otherAccount := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("session-1", 43), "system")
	require.NotEqual(t, first.RequestID, otherAccount.RequestID)
	require.NotEqual(t, first.Labels["trajectory_id"], otherAccount.Labels["trajectory_id"])

	// A different conversation gets a different identity as well.
	otherConversation := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("session-2", 42), "system")
	require.NotEqual(t, first.RequestID, otherConversation.RequestID)
}

func TestBuildWireEnvelope_SeparatesConversationsWithSameOpeningMessage(t *testing.T) {
	contents := testContents("same opening message")
	first := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("", 42), "system prompt A")
	second := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("", 42), "system prompt B")
	require.NotEqual(t, first.RequestID, second.RequestID)

	// Without a session header the identity must stay stable across turns.
	third := BuildWireEnvelope("gemini-3.7-flash-high", contents, testIdentity("", 42), "system prompt A")
	require.Equal(t, first.RequestID, third.RequestID)
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

	body, err := TransformClaudeToGeminiWithOptions(req, "project", "gemini-3.7-flash-high", TransformOptions{
		WireIdentity: testIdentity("seed", 7),
	})
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

func TestWrapGeminiRequest_InjectsIdentityAndPreservesUnknownFields(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"systemInstruction":{"parts":[{"text":"sys"}]},"cachedContent":"keep-me"}`)

	wrapped, err := WrapGeminiRequest("project", "agent", "gemini-3.7-flash-high", body, testIdentity("session-1", 42))
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wrapped, &raw))
	require.Regexp(t, `^agent/[0-9a-f-]{36}/1$`, gjsonString(t, raw["requestId"]))

	var inner map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["request"], &inner))
	require.Contains(t, string(inner["sessionId"]), OfficialAntigravitySessionID)
	require.Contains(t, string(inner["labels"]), `"model_enum":"MODEL_PLACEHOLDER_M298"`)
	require.Equal(t, `"keep-me"`, string(inner["cachedContent"]))
	require.Contains(t, string(inner["systemInstruction"]), "sys")
}

func gjsonString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	require.NoError(t, json.Unmarshal(raw, &s))
	return s
}
