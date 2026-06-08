package service

import (
	"context"
	"log/slog"
)

// AuditLog emits a structured audit log entry for security-relevant operations.
// Every call includes action, actor, target, and outcome fields that are
// consistent across all audit points, enabling downstream SIEM/log analysis.
func AuditLog(ctx context.Context, action, actor, target string, outcome error, extra ...any) {
	attrs := []slog.Attr{
		slog.String("audit", "true"),
		slog.String("action", action),
		slog.String("actor", actor),
		slog.String("target", target),
	}
	if outcome != nil {
		attrs = append(attrs, slog.String("outcome", "failure"), slog.String("error", outcome.Error()))
	} else {
		attrs = append(attrs, slog.String("outcome", "success"))
	}
	// Convert extra pairs (key, value, key, value...) to slog.Attr.
	for i := 0; i+1 < len(extra); i += 2 {
		if key, ok := extra[i].(string); ok {
			attrs = append(attrs, slog.Any(key, extra[i+1]))
		}
	}
	slog.LogAttrs(ctx, slog.LevelInfo, "audit: "+action, attrs...)
}