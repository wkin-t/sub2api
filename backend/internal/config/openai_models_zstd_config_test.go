package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIModelsResponseZstdConfigDefaultAndEnv(t *testing.T) {
	t.Run("default false", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		cfg, err := Load()
		require.NoError(t, err)
		require.False(t, cfg.Gateway.OpenAIModelsResponseZstd)
	})

	t.Run("env override true", func(t *testing.T) {
		resetViperWithJWTSecret(t)
		t.Setenv("GATEWAY_OPENAI_MODELS_RESPONSE_ZSTD", "true")
		cfg, err := Load()
		require.NoError(t, err)
		require.True(t, cfg.Gateway.OpenAIModelsResponseZstd)
	})
}
