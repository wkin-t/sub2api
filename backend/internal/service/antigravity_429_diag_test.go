//go:build unit

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func resetAntigravity429DiagState(t *testing.T) {
	t.Helper()
	antigravity429DiagMu.Lock()
	antigravity429DiagSeen = map[string]struct{}{}
	antigravity429DiagWrites = 0
	antigravity429DiagMu.Unlock()
}

func TestAntigravity429LooksPseudo(t *testing.T) {
	require.True(t, antigravity429LooksPseudo([]byte(
		`{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`)))
	require.False(t, antigravity429LooksPseudo([]byte(
		`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"30s"}]}}`)))
	require.False(t, antigravity429LooksPseudo([]byte(
		`{"error":{"details":[{"quotaResetDelay":"1h"}]}}`)))
	require.False(t, antigravity429LooksPseudo(nil))
}

func TestBuildAntigravity429RequestDiag(t *testing.T) {
	body := []byte(`{"model":"gemini-3.7-flash-high","requestType":"agent","request":{"systemInstruction":{"parts":[{"text":"<identity>hi</identity>"},{"text":"second"}]}}}`)
	diag := buildAntigravity429RequestDiag(body)
	require.Equal(t, "gemini-3.7-flash-high", diag.Model)
	require.Equal(t, "agent", diag.RequestType)
	require.Contains(t, diag.SystemText, "<identity>hi</identity>")
	require.Len(t, diag.SystemSHA, 16)
	require.Equal(t, diag.SystemSHA, buildAntigravity429RequestDiag(body).SystemSHA)

	other := []byte(`{"request":{"systemInstruction":{"parts":[{"text":"other"}]}}}`)
	require.NotEqual(t, diag.SystemSHA, buildAntigravity429RequestDiag(other).SystemSHA)
}

func TestWriteAntigravity429DiagDump(t *testing.T) {
	resetAntigravity429DiagState(t)
	dir := t.TempDir()
	p := antigravityRetryLoopParams{
		prefix:         "test",
		requestedModel: "gemini-3.7-flash-high",
		account:        &Account{ID: 42, Name: "acct"},
	}
	diag := antigravity429RequestDiag{
		Model:       "gemini-3.7-flash-high",
		RequestType: "agent",
		SystemText:  "SYSTEM_TEXT_MARKER",
		SystemSHA:   "abcdef0123456789",
	}
	path, err := writeAntigravity429DiagDump(dir, p, diag, antigravity429Unknown, true, []byte(`{"error":"exhausted"}`), "https://upstream.example")
	require.NoError(t, err)
	require.NotEmpty(t, path)
	require.True(t, strings.HasPrefix(path, dir))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(raw)
	require.Contains(t, content, "SYSTEM_TEXT_MARKER")
	require.Contains(t, content, "pseudo_429=true")
	require.Contains(t, content, "account_id=42")
	require.Contains(t, content, "system_sha256=abcdef0123456789")

	// 同一 system 指纹只写一次。
	path2, err := writeAntigravity429DiagDump(dir, p, diag, antigravity429Unknown, true, nil, "https://upstream.example")
	require.NoError(t, err)
	require.Empty(t, path2)

	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, filepath.Base(path), files[0].Name())
}
