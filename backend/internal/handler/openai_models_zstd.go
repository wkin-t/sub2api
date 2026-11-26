package handler

import (
	"crypto/sha256"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sync/singleflight"
)

const (
	openAIModelsZstdMinBytes      = 1024
	openAIModelsZstdMaxInputBytes = 8 << 20
	openAIModelsZstdMaxEntries    = 64
	openAIModelsZstdMaxTotalBytes = 8 << 20
	// 2 MiB 窗口足以覆盖整份 Codex 模型清单中的跨模型重复 prompt，同时兼容严格解码器上限。
	openAIModelsZstdWindowSize       = 2 << 20
	openAIModelsZstdMaxEncodeConc    = 2
)

type openAIModelsZstdCacheEntry struct {
	data  []byte
	order uint64
}

type openAIModelsZstdCache struct {
	mu         sync.Mutex
	entries    map[[32]byte]openAIModelsZstdCacheEntry
	totalBytes int
	nextOrder  uint64
	sf         singleflight.Group
	encPool    sync.Pool
	encodeSem  chan struct{}
}

var defaultOpenAIModelsZstdCache = newOpenAIModelsZstdCache()

func newOpenAIModelsZstdCache() *openAIModelsZstdCache {
	c := &openAIModelsZstdCache{
		encodeSem: make(chan struct{}, openAIModelsZstdMaxEncodeConc),
	}
	c.encPool = sync.Pool{
		New: func() any {
			enc, err := zstd.NewWriter(
				nil,
				zstd.WithEncoderLevel(zstd.SpeedDefault),
				zstd.WithWindowSize(openAIModelsZstdWindowSize),
				zstd.WithEncoderConcurrency(1),
			)
			if err != nil {
				return nil
			}
			return enc
		},
	}
	return c
}

func (c *openAIModelsZstdCache) getOrCompress(body []byte) ([]byte, bool) {
	if len(body) < openAIModelsZstdMinBytes || len(body) > openAIModelsZstdMaxInputBytes {
		return nil, false
	}
	key := sha256.Sum256(body)
	if cached, ok := c.get(key); ok {
		return cached, len(cached) > 0 && len(cached) < len(body)
	}

	val, _, _ := c.sf.Do(string(key[:]), func() (any, error) {
		if cached, ok := c.get(key); ok {
			return cached, nil
		}
		compressed := c.encode(body)
		if len(compressed) == 0 || len(compressed) >= len(body) {
			return []byte(nil), nil
		}
		c.set(key, compressed)
		return compressed, nil
	})
	compressed, _ := val.([]byte)
	return compressed, len(compressed) > 0 && len(compressed) < len(body)
}

func (c *openAIModelsZstdCache) encode(body []byte) []byte {
	c.encodeSem <- struct{}{}
	defer func() { <-c.encodeSem }()
	enc, _ := c.encPool.Get().(*zstd.Encoder)
	if enc == nil {
		return nil
	}
	defer c.encPool.Put(enc)
	return enc.EncodeAll(body, nil)
}

func (c *openAIModelsZstdCache) get(key [32]byte) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	c.nextOrder++
	entry.order = c.nextOrder
	c.entries[key] = entry
	return entry.data, true
}

func (c *openAIModelsZstdCache) set(key [32]byte, data []byte) {
	if len(data) == 0 || len(data) > openAIModelsZstdMaxTotalBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[[32]byte]openAIModelsZstdCacheEntry)
	}
	if existing, exists := c.entries[key]; exists {
		c.totalBytes -= len(existing.data)
		delete(c.entries, key)
	}
	for len(c.entries) > 0 && (len(c.entries) >= openAIModelsZstdMaxEntries || c.totalBytes+len(data) > openAIModelsZstdMaxTotalBytes) {
		var oldestKey [32]byte
		var oldestOrder uint64
		hasOldest := false
		for k, entry := range c.entries {
			if !hasOldest || entry.order < oldestOrder {
				oldestKey = k
				oldestOrder = entry.order
				hasOldest = true
			}
		}
		if !hasOldest {
			break
		}
		c.totalBytes -= len(c.entries[oldestKey].data)
		delete(c.entries, oldestKey)
	}
	c.nextOrder++
	c.entries[key] = openAIModelsZstdCacheEntry{
		data:  data,
		order: c.nextOrder,
	}
	c.totalBytes += len(data)
}

// acceptsZstdEncoding 按 RFC 9110 解析 Accept-Encoding，仅对显式声明 zstd 且 q>0 时返回 true。
// 不匹配通配符 "*"，避免未明确声明支持 zstd 的客户端收到无法解码的响应。
func acceptsZstdEncoding(header string) bool {
	if strings.TrimSpace(header) == "" {
		return false
	}
	accepted := false
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(segments[0]))
		if coding != "zstd" {
			continue
		}
		q := 1.0
		valid := true
		for _, param := range segments[1:] {
			param = strings.TrimSpace(param)
			if param == "" {
				continue
			}
			kv := strings.SplitN(param, "=", 2)
			if len(kv) != 2 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(kv[0]), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
				if err != nil || parsed < 0 || parsed > 1 {
					valid = false
					break
				}
				q = parsed
			}
		}
		if !valid || q <= 0 {
			return false
		}
		accepted = true
	}
	return accepted
}

func appendVaryHeader(h http.Header, token string) {
	if h == nil || strings.TrimSpace(token) == "" {
		return
	}
	for _, existing := range h.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), token) {
				return
			}
		}
	}
	h.Add("Vary", token)
}
