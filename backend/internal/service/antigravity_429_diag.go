package service

// 伪 429 观测：Antigravity 的黑名单是滚动更新的，线上仍可能出现新的触发句。
// 这个文件提供 opt-in 的诊断通道，用于「先上线观测」阶段回答两个问题：
//   1) 429 是真限流（带 RetryInfo/quotaResetDelay）还是内容黑名单造成的裸 429；
//   2) 失败请求的 systemInstruction 指纹是否重复（重复→同一触发句，可 diff 后加 strip）。
//
// 默认关闭。SUB2API_ANTIGRAVITY_429_DIAG=summary 只记指纹；
// =dump 时额外把 systemInstruction 落到 0600 文件（可能含客户端系统提示，属敏感数据，
// 只在排障窗口开启并事后删除）。env 在进程启动时读取一次，改完需重启。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
)

const (
	antigravity429DiagEnv      = "SUB2API_ANTIGRAVITY_429_DIAG"
	antigravity429DiagDirEnv   = "SUB2API_ANTIGRAVITY_429_DIAG_DIR"
	antigravity429DiagMaxText  = 256 << 10 // 单文件最多保存 256KB systemInstruction
	antigravity429DiagMaxFiles = 512       // 单进程最多写 512 个诊断文件，防止磁盘打满
)

type antigravity429DiagMode int

const (
	antigravity429DiagOff antigravity429DiagMode = iota
	antigravity429DiagSummary
	antigravity429DiagDump
)

func resolveAntigravity429DiagMode() antigravity429DiagMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(antigravity429DiagEnv))) {
	case "", "0", "off", "false", "no":
		return antigravity429DiagOff
	case "1", "true", "on", "yes", "summary":
		return antigravity429DiagSummary
	case "dump", "full", "text":
		return antigravity429DiagDump
	default:
		logger.LegacyPrintf("service.antigravity_gateway",
			"[Antigravity] unknown %s=%q; 429 diagnostics disabled", antigravity429DiagEnv, os.Getenv(antigravity429DiagEnv))
		return antigravity429DiagOff
	}
}

var antigravity429DiagState = resolveAntigravity429DiagMode()

var (
	antigravity429DiagMu     sync.Mutex
	antigravity429DiagSeen   = map[string]struct{}{}
	antigravity429DiagWrites int
)

type antigravity429RequestDiag struct {
	Model       string
	RequestType string
	SystemText  string
	SystemSHA   string
}

func buildAntigravity429RequestDiag(body []byte) antigravity429RequestDiag {
	diag := antigravity429RequestDiag{
		Model:       gjson.GetBytes(body, "model").String(),
		RequestType: gjson.GetBytes(body, "requestType").String(),
	}
	var builder strings.Builder
	parts := gjson.GetBytes(body, "request.systemInstruction.parts")
	if parts.IsArray() {
		for _, part := range parts.Array() {
			if text := part.Get("text").String(); text != "" {
				builder.WriteString(text)
				builder.WriteByte('\n')
			}
		}
	}
	diag.SystemText = builder.String()
	sum := sha256.Sum256([]byte(diag.SystemText))
	diag.SystemSHA = hex.EncodeToString(sum[:8])
	return diag
}

// antigravity429LooksPseudo 判断响应是否为「无重试/配额元数据」的裸 429。
// 真限流通常带 RetryInfo / quotaResetDelay / RATE_LIMIT_EXCEEDED。
func antigravity429LooksPseudo(body []byte) bool {
	if len(bytes.TrimSpace(body)) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	for _, marker := range []string{"retryinfo", "quotaresetdelay", "rate_limit_exceeded", "ratelimitexceeded"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return strings.Contains(lower, "exhausted") || strings.Contains(lower, "resource has been exhausted")
}

func antigravity429CategoryLabel(category antigravity429Category) string {
	switch category {
	case antigravity429QuotaExhausted:
		return "quota_exhausted"
	case antigravity429RateLimited:
		return "rate_limited"
	default:
		return "unknown"
	}
}

// logAntigravity429Diagnostics 在 429 响应处记录请求侧指纹；dump 模式下落盘 systemInstruction。
func (s *AntigravityGatewayService) logAntigravity429Diagnostics(p antigravityRetryLoopParams, resp *http.Response, respBody []byte, baseURL string) {
	mode := antigravity429DiagState
	if mode == antigravity429DiagOff || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return
	}

	diag := buildAntigravity429RequestDiag(p.body)
	category := classifyAntigravity429(respBody)
	pseudo := antigravity429LooksPseudo(respBody)
	retryInfo := bytes.Contains(bytes.ToLower(respBody), []byte("retryinfo"))
	quotaResetDelay := bytes.Contains(bytes.ToLower(respBody), []byte("quotaresetdelay"))

	logger.LegacyPrintf("service.antigravity_gateway",
		"%s status=429 diag=1 account=%d model=%s base_url=%s category=%s pseudo=%t retryinfo=%t quota_delay=%t system_sha=%s system_len=%d body=%s",
		p.prefix, p.account.ID, antigravityFirstNonEmpty(diag.Model, p.requestedModel), baseURL,
		antigravity429CategoryLabel(category), pseudo, retryInfo, quotaResetDelay,
		diag.SystemSHA, len(diag.SystemText), truncateForLog(respBody, 300))

	if mode != antigravity429DiagDump || diag.SystemText == "" {
		return
	}
	dumpDir := strings.TrimSpace(os.Getenv(antigravity429DiagDirEnv))
	path, err := writeAntigravity429DiagDump(dumpDir, p, diag, category, pseudo, respBody, baseURL)
	if err != nil {
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=429 diag_dump_failed error=%v", p.prefix, err)
		return
	}
	if path != "" {
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=429 diag_dumped file=%s", p.prefix, path)
	}
}

// writeAntigravity429DiagDump 把 systemInstruction 写到一个 0600 文件；同一 system 指纹只写一次。
func writeAntigravity429DiagDump(dir string, p antigravityRetryLoopParams, diag antigravity429RequestDiag, category antigravity429Category, pseudo bool, respBody []byte, baseURL string) (string, error) {
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "sub2api-429-diag")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create diag dir: %w", err)
	}

	antigravity429DiagMu.Lock()
	if _, ok := antigravity429DiagSeen[diag.SystemSHA]; ok {
		antigravity429DiagMu.Unlock()
		return "", nil
	}
	if antigravity429DiagWrites >= antigravity429DiagMaxFiles {
		antigravity429DiagMu.Unlock()
		return "", nil
	}
	antigravity429DiagSeen[diag.SystemSHA] = struct{}{}
	antigravity429DiagWrites++
	antigravity429DiagMu.Unlock()

	text := diag.SystemText
	if len(text) > antigravity429DiagMaxText {
		text = text[:antigravity429DiagMaxText] + "\n... [truncated]"
	}
	var content strings.Builder
	fmt.Fprintf(&content, "time=%s\naccount_id=%d\naccount_name=%s\nmodel=%s\nrequest_type=%s\nbase_url=%s\ncategory=%s\npseudo_429=%t\nsystem_sha256=%s\nsystem_len=%d\n",
		time.Now().UTC().Format(time.RFC3339), p.account.ID, p.account.Name,
		antigravityFirstNonEmpty(diag.Model, p.requestedModel), diag.RequestType, baseURL,
		antigravity429CategoryLabel(category), pseudo, diag.SystemSHA, len(diag.SystemText))
	content.WriteString("\n== response_body ==\n")
	content.WriteString(truncateForLog(respBody, 2000))
	content.WriteString("\n\n== systemInstruction ==\n")
	content.WriteString(text)
	content.WriteString("\n")

	name := fmt.Sprintf("%s-%s.txt", time.Now().UTC().Format("20060102-150405"), diag.SystemSHA)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		return "", fmt.Errorf("write diag file: %w", err)
	}
	return path, nil
}

func antigravityFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
