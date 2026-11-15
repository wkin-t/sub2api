package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// identity_patch_prompt 必须同时作用于 Gemini 原生 / 兼容链路，而不只是 Claude 转换路径。
func TestInjectIdentityPatchToGeminiRequest_UsesCustomPatch(t *testing.T) {
	const custom = "<identity>\nYou are Antigravity, custom profile.\n</identity>"
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]any{
			{"text": "You are Claude Code, Anthropic's official CLI for Claude. Help the user."},
			{"text": "Keep this rule."},
		}},
		"contents": []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "hi"}}}},
	})
	require.NoError(t, err)

	got, err := injectIdentityPatchToGeminiRequest(body, custom)
	require.NoError(t, err)
	require.Equal(t, custom, gjson.GetBytes(got, "systemInstruction.parts.0.text").String())
	require.Equal(t, "You are an AI agent. Help the user.", gjson.GetBytes(got, "systemInstruction.parts.1.text").String())
	require.Equal(t, "Keep this rule.", gjson.GetBytes(got, "systemInstruction.parts.2.text").String())
	require.Equal(t, "hi", gjson.GetBytes(got, "contents.0.parts.0.text").String())

	again, err := injectIdentityPatchToGeminiRequest(got, custom)
	require.NoError(t, err)
	require.JSONEq(t, string(got), string(again))
}

// 清洗后整段为空的 system part 必须被物理移除，随后再注入身份提示词。
func TestInjectIdentityPatchToGeminiRequest_DropsPartEmptiedBySanitizer(t *testing.T) {
	const custom = "PROFILE"
	body := []byte(`{
		"systemInstruction": {"parts": [
			{"text": "Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context: pentesting engagements, CTF competitions, security research, or defensive use cases."}
		]},
		"contents": [{"role": "user", "parts": [{"text": "hi"}]}]
	}`)

	got, err := injectIdentityPatchToGeminiRequest(body, custom)
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(got, "systemInstruction.parts").Array(), 1)
	require.Equal(t, custom, gjson.GetBytes(got, "systemInstruction.parts.0.text").String())
}

// identityPatch 为空时继续使用内置默认模板，保持原行为。
func TestInjectIdentityPatchToGeminiRequest_FallsBackToBuiltinDefault(t *testing.T) {
	body := []byte(`{"contents": [{"role": "user", "parts": [{"text": "hi"}]}]}`)

	got, err := injectIdentityPatchToGeminiRequest(body, "")
	require.NoError(t, err)
	require.Equal(t, antigravity.GetDefaultIdentityPatch(), gjson.GetBytes(got, "systemInstruction.parts.0.text").String())
}
