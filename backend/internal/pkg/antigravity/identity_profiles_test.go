package antigravity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectIdentityPrompt_Profiles(t *testing.T) {
	agy := strings.TrimSpace(agyCLIIdentityPrompt)
	require.Contains(t, agy, "You are Antigravity")
	legacy := strings.TrimSpace(antigravityIdentity)
	require.Contains(t, legacy, "Advanced Agentic Coding")

	for _, profile := range []string{"", "agy-cli", "AGY-CLI", "agy", "agy-cli-generic", "agy-cli-1.5.9"} {
		require.Equal(t, agy, selectIdentityPrompt(profile, nil), "profile %q", profile)
	}
	for _, profile := range []string{"agy-cli-coding", "AGY-CLI-CODING"} {
		coding := selectIdentityPrompt(profile, nil)
		require.Contains(t, coding, "Advanced Agentic Coding", "profile %q", profile)
		require.Contains(t, coding, "<USER_REQUEST>")
	}
	require.NotEqual(t, agy, selectIdentityPrompt("agy-cli-coding", nil))
	for _, profile := range []string{"legacy", "upstream", "LEGACY"} {
		require.Equal(t, legacy, selectIdentityPrompt(profile, nil), "profile %q", profile)
	}
	// Unknown profiles must not silently leave the identity empty.
	require.Equal(t, agy, selectIdentityPrompt("unknown-profile", nil))
}

func TestSelectIdentityPrompt_FileOverrideWins(t *testing.T) {
	require.Equal(t, "custom prompt", selectIdentityPrompt("legacy", []byte("\n custom prompt \n")))
	// A blank file falls back to the profile instead of injecting nothing.
	require.Equal(t, strings.TrimSpace(antigravityIdentity), selectIdentityPrompt("legacy", []byte(" \n\t ")))
}

func TestDefaultIdentityPrompt_IsAntigravityIdentity(t *testing.T) {
	require.Contains(t, DefaultIdentityPrompt(), "You are Antigravity")
}
