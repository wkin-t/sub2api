package service

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/gin-gonic/gin"
)

// antigravityWireIdentity carries the per-conversation seed and serving account
// used to build the Antigravity wire envelope (sessionId / labels / requestId).
//
// The real client mints its cascade identity per app instance; a relay must not
// reuse one identity across several OAuth accounts because the upstream would
// then associate those accounts with one client.
type antigravityWireIdentityContextKey struct{}

func withAntigravityWireIdentity(ctx context.Context, seed string, accountID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, antigravityWireIdentityContextKey{}, antigravity.WireIdentity{
		ConversationSeed: strings.TrimSpace(seed),
		AccountID:        accountID,
	})
}

func antigravityWireIdentityFromContext(ctx context.Context) antigravity.WireIdentity {
	if ctx == nil {
		return antigravity.WireIdentity{}
	}
	identity, _ := ctx.Value(antigravityWireIdentityContextKey{}).(antigravity.WireIdentity)
	return identity
}

// antigravityWireSeed returns the client session id of the current request.
func antigravityWireSeed(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(getSessionID(c))
}

// antigravityHTTP2Enabled reports whether Antigravity inference should speak
// HTTP/2 like the official Go client (ALPN h2,http/1.1).
//
// This is a deliberate transport change (the official client is h2), so it
// defaults to on; set SUB2API_ANTIGRAVITY_HTTP2=0/false/off to keep HTTP/1.1.
var antigravityHTTP2Enabled = resolveAntigravityHTTP2()

func resolveAntigravityHTTP2() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_ANTIGRAVITY_HTTP2"))) {
	case "0", "false", "off", "no":
		return false
	case "", "1", "true", "on", "yes":
		return true
	default:
		log.Printf("[Antigravity] unknown SUB2API_ANTIGRAVITY_HTTP2=%q; using HTTP/2", os.Getenv("SUB2API_ANTIGRAVITY_HTTP2"))
		return true
	}
}

// antigravityRequestContext marks an Antigravity inference request so the
// shared upstream transport negotiates HTTP/2 with keepalive PINGs, matching
// the official Go client. Without the long_stream profile the transport keeps
// HTTP/1.1 (a custom DialContext disables Go's automatic HTTP/2 fallback).
func antigravityRequestContext(ctx context.Context) context.Context {
	if !antigravityHTTP2Enabled {
		return ctx
	}
	return WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileLongStream)
}
