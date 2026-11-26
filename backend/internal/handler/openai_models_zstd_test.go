package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func TestAcceptsZstdEncoding(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{header: "", want: false},
		{header: "gzip, deflate, br", want: false},
		{header: "*", want: false},
		{header: "zstd,gzip", want: true},
		{header: "ZSTD, gzip", want: true},
		{header: "gzip, zstd;q=0.8", want: true},
		{header: "zstd;q=1.0, gzip;q=0.5", want: true},
		{header: "zstd;q=0, gzip", want: false},
		{header: "zstd;q=0.000", want: false},
		{header: "zstd;q=invalid, gzip", want: false},
		{header: "zstd;q=-0.5", want: false},
		{header: "zstd;q=1.5", want: false},
		{header: "zstd, zstd;q=0", want: false},
		{header: "x-zstd, gzip", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			require.Equal(t, tc.want, acceptsZstdEncoding(tc.header))
		})
	}
}

func TestWriteOpenAIModelsResponseWithConfig_ZstdBehavior(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repeatedPrompt := strings.Repeat("You are Codex, an engineering assistant with shell tools. ", 200)
	largeBody := []byte(fmt.Sprintf(`{"models":[{"slug":"m1","instructions":%q},{"slug":"m2","instructions":%q}]}`, repeatedPrompt, repeatedPrompt))
	manifest := &service.OpenAIModelsResponse{
		Body: largeBody,
		ETag: `"strong-sha256-etag"`,
	}

	t.Run("disabled by default returns plain JSON without Vary", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd,gzip")

		cfg := &config.Config{}
		writeOpenAIModelsResponseWithConfig(c, manifest, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
		require.Empty(t, rec.Header().Get("Vary"))
		require.Equal(t, `"strong-sha256-etag"`, rec.Header().Get("ETag"))
		require.Equal(t, largeBody, rec.Body.Bytes())
	})

	t.Run("enabled with zstd Accept-Encoding returns valid zstd frame and strong ETag", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd,gzip")

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		writeOpenAIModelsResponseWithConfig(c, manifest, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "zstd", rec.Header().Get("Content-Encoding"))
		require.Contains(t, rec.Header().Get("Vary"), "Accept-Encoding")
		require.Equal(t, `"strong-sha256-etag"`, rec.Header().Get("ETag"))
		require.Equal(t, fmt.Sprintf("%d", rec.Body.Len()), rec.Header().Get("Content-Length"))
		require.Less(t, rec.Body.Len(), len(largeBody)/5)

		dec, err := zstd.NewReader(bytes.NewReader(rec.Body.Bytes()))
		require.NoError(t, err)
		defer dec.Close()
		decoded, err := io.ReadAll(dec)
		require.NoError(t, err)
		require.Equal(t, largeBody, decoded)
	})

	t.Run("enabled without zstd Accept-Encoding returns plain JSON with Vary", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "gzip, br")

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		writeOpenAIModelsResponseWithConfig(c, manifest, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
		require.Contains(t, rec.Header().Get("Vary"), "Accept-Encoding")
		require.Equal(t, largeBody, rec.Body.Bytes())
	})

	t.Run("enabled with zstd q=0 returns plain JSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd;q=0, gzip")

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		writeOpenAIModelsResponseWithConfig(c, manifest, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
		require.Equal(t, largeBody, rec.Body.Bytes())
	})

	t.Run("304 NotModified skips zstd and keeps ETag", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd,gzip")

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		writeOpenAIModelsResponseWithConfig(c, &service.OpenAIModelsResponse{
			ETag:        `"strong-sha256-etag"`,
			NotModified: true,
		}, cfg)

		require.Equal(t, http.StatusNotModified, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
		require.Contains(t, rec.Header().Get("Vary"), "Accept-Encoding")
		require.Equal(t, "private, no-cache", rec.Header().Get("Cache-Control"))
		require.Equal(t, `"strong-sha256-etag"`, rec.Header().Get("ETag"))
		require.Empty(t, rec.Body.Bytes())
	})

	t.Run("single model subpath :model skips zstd", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models/m1", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd,gzip")
		c.Params = gin.Params{{Key: "model", Value: "m1"}}

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		listBody := []byte(fmt.Sprintf(`{"object":"list","data":[{"id":"m1","pad":%q}]}`, repeatedPrompt))
		writeOpenAIModelsResponseWithConfig(c, &service.OpenAIModelsResponse{Body: listBody}, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
		require.Contains(t, rec.Body.String(), `"id":"m1"`)
	})

	t.Run("HEAD request skips zstd", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodHead, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd,gzip")

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		writeOpenAIModelsResponseWithConfig(c, manifest, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
	})

	t.Run("small body below threshold skips zstd", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models?client_version=0.171.0", nil)
		c.Request.Header.Set("Accept-Encoding", "zstd,gzip")

		cfg := &config.Config{}
		cfg.Gateway.OpenAIModelsResponseZstd = true
		small := &service.OpenAIModelsResponse{Body: []byte(`{"models":[]}`), ETag: `"small"`}
		writeOpenAIModelsResponseWithConfig(c, small, cfg)

		require.Equal(t, http.StatusOK, rec.Code)
		require.Empty(t, rec.Header().Get("Content-Encoding"))
		require.Equal(t, small.Body, rec.Body.Bytes())
	})
}

func TestOpenAIModelsZstdCache_BoundedEviction(t *testing.T) {
	cache := newOpenAIModelsZstdCache()
	for i := range openAIModelsZstdMaxEntries + 10 {
		body := []byte(fmt.Sprintf(`{"id":%d,"pad":%q}`, i, strings.Repeat("codex-model-entry-", 120)))
		compressed, ok := cache.getOrCompress(body)
		require.True(t, ok)
		require.NotEmpty(t, compressed)
		require.LessOrEqual(t, len(cache.entries), openAIModelsZstdMaxEntries)
		require.LessOrEqual(t, cache.totalBytes, openAIModelsZstdMaxTotalBytes)
	}
}
