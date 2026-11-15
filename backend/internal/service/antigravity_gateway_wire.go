package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

// wrapV1InternalRequestWithContext 包装请求为 v1internal 格式，并按真机客户端
// 结构补全 requestId / labels / sessionId（见 pkg/antigravity/envelope.go）。
// Gemini 原生链路走这里；Claude 转换链路由 TransformClaudeToGeminiWithOptions
// 直接生成同一份契约（结构体定义都在 pkg 内，避免第二份键名字面量）。
func (s *AntigravityGatewayService) wrapV1InternalRequestWithContext(ctx context.Context, projectID, model string, originalBody []byte) ([]byte, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil, errAntigravityProjectIDRequired
	}
	wrapped, err := antigravity.WrapGeminiRequest(projectID, "agent", model, originalBody, antigravityWireIdentityFromContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("包装 v1internal 请求失败: %w", err)
	}
	return wrapped, nil
}
