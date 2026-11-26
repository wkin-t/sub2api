package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIModelsCache_CachesLargeManifestOver512KiBAndTracksBytes(t *testing.T) {
	var cache openAIModelsCache
	now := time.Now()
	largeBody := bytes.Repeat([]byte("a"), 900*1024) // > 512 KiB old effective limit

	manifest := &OpenAIModelsResponse{
		Body: largeBody,
		ETag: `"large-etag"`,
	}
	cache.set("k1", manifest, now)

	got, state := cache.get("k1", now.Add(30*time.Second))
	require.Equal(t, openAIModelsCacheFresh, state)
	require.NotNil(t, got)
	require.Equal(t, len(largeBody), len(got.Body))
	require.Equal(t, len(largeBody), cache.totalBytes)

	// Between TTL (60s) and StaleTTL (5m) -> Stale (SWR window preserved)
	got, state = cache.get("k1", now.Add(2*time.Minute))
	require.Equal(t, openAIModelsCacheStale, state)
	require.NotNil(t, got)

	// After StaleTTL -> Miss and totalBytes reclaimed
	got, state = cache.get("k1", now.Add(openAIModelsCacheStaleTTL))
	require.Equal(t, openAIModelsCacheMiss, state)
	require.Nil(t, got)
	require.Equal(t, 0, cache.totalBytes)
}

func TestOpenAIModelsCache_EnforcesTotalByteBudgetAndOverwriteAccounting(t *testing.T) {
	var cache openAIModelsCache
	now := time.Now()
	chunk := bytes.Repeat([]byte("x"), 3<<20) // 3 MiB per entry (< 4 MiB single limit)

	// Insert 25 entries (75 MiB > 64 MiB total budget)
	for i := range 25 {
		key := "key-" + string(rune('a'+i))
		cache.set(key, &OpenAIModelsResponse{Body: chunk}, now.Add(time.Duration(i)*time.Second))
		require.LessOrEqual(t, cache.totalBytes, openAIModelsCacheMaxTotalBytes)
		require.LessOrEqual(t, len(cache.entries), openAIModelsCacheMaxEntries)
	}
	// Oldest entry must have been evicted
	_, state := cache.get("key-a", now.Add(30*time.Second))
	require.Equal(t, openAIModelsCacheMiss, state)

	// Overwriting an existing key must update totalBytes accurately without double counting
	beforeBytes := cache.totalBytes
	smaller := bytes.Repeat([]byte("y"), 1<<20)
	cache.set("key-y", &OpenAIModelsResponse{Body: smaller}, now.Add(30*time.Second))
	require.Equal(t, beforeBytes-(3<<20)+(1<<20), cache.totalBytes)

	// Entry > 4 MiB single limit is rejected without mutating totalBytes
	oversize := bytes.Repeat([]byte("z"), openAIModelsCacheBodyLimit+1)
	cache.set("oversize", &OpenAIModelsResponse{Body: oversize}, now.Add(31*time.Second))
	_, state = cache.get("oversize", now.Add(31*time.Second))
	require.Equal(t, openAIModelsCacheMiss, state)

	// Verify strict totalBytes == sum(entry.sizeBytes) invariant across mixed operations
	assertInvariant := func() {
		t.Helper()
		sum := 0
		for _, e := range cache.entries {
			sum += e.sizeBytes
		}
		require.Equal(t, sum, cache.totalBytes)
	}
	assertInvariant()
	for i := range 40 {
		ts := now.Add(time.Duration(i*35) * time.Second)
		k := "mix-" + string(rune('a'+(i%10)))
		sz := ((i % 3) + 1) << 20
		cache.set(k, &OpenAIModelsResponse{Body: make([]byte, sz)}, ts)
		_, _ = cache.get("mix-a", ts.Add(openAIModelsCacheStaleTTL))
		assertInvariant()
	}
}

func TestFetchCodexModelsManifest_OAuthLargeManifestCachesAndSkipsUpstreamSourceBody(t *testing.T) {
	// Build a ~750 KiB valid Codex manifest (> 512 KiB old effective limit)
	padding := strings.Repeat("system instruction text repeated across models ", 15000)
	payload, err := json.Marshal(map[string]any{
		"models": []map[string]any{
			{"slug": "gpt-6.2-sol", "display_name": "GPT 6.2 Sol", "instructions": padding},
		},
	})
	require.NoError(t, err)
	require.Greater(t, len(payload), 512*1024)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"upstream-large-etag"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	origURL := chatgptCodexModelsURL
	chatgptCodexModelsURL = srv.URL
	t.Cleanup(func() { chatgptCodexModelsURL = origURL })

	svc := &OpenAIGatewayService{}
	account := &Account{
		ID:       59,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":       "test-oauth-token",
			"chatgpt_account_id": "acct-test",
		},
	}

	first, err := svc.FetchCodexModelsManifest(context.Background(), account, "0.171.0", "")
	require.NoError(t, err)
	require.Equal(t, int32(1), hits.Load())
	require.Empty(t, first.upstreamSourceBody, "OAuth manifest should not retain redundant upstreamSourceBody copy")
	require.Equal(t, `"upstream-large-etag"`, first.ETag)

	// Second call within TTL must hit in-memory cache with zero additional upstream fetches
	second, err := svc.FetchCodexModelsManifest(context.Background(), account, "0.171.0", "")
	require.NoError(t, err)
	require.Equal(t, int32(1), hits.Load(), "second request within TTL should hit cache")
	require.Equal(t, first.Body, second.Body)
	require.Empty(t, second.upstreamSourceBody)
}
