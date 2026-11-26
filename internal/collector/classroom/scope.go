package classroom

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/client"
)

var errScope = errors.New("响应的日期、时段、楼栋或页码与请求不符")

// expected 和 actual 只使用字段专用的脱敏值，禁止传入原始查询串或 HTML。
type scopeMismatch struct {
	source, field, expected, actual string
	page, row                       int
	shape                           roomShape
}

func (*scopeMismatch) Error() string { return errScope.Error() }
func (*scopeMismatch) Unwrap() error { return errScope }

func (c *Collector) warnQuery(ctx context.Context, message string, err error, attrs ...slog.Attr) {
	attrs = append(attrs, slog.String("reason", err.Error()), slog.Bool("retryable_integrity", retryableIntegrity(err)))
	var integrity *integrityError
	if errors.As(err, &integrity) {
		attrs = append(attrs, integrity.attrs...)
	}
	var mismatch *scopeMismatch
	if errors.As(err, &mismatch) {
		attrs = append(attrs,
			slog.String("source", mismatch.source),
			slog.String("field", mismatch.field),
			slog.String("expected", mismatch.expected),
			slog.String("actual", mismatch.actual))
		if mismatch.page > 0 {
			attrs = append(attrs, slog.Int("page", mismatch.page))
		}
		if mismatch.row > 0 {
			attrs = append(attrs, slog.Int("row", mismatch.row))
			attrs = append(attrs, mismatch.shape.attrs()...)
		}
	} else {
		var page *pageFailure
		if errors.As(err, &page) {
			attrs = append(attrs, slog.Int("page", page.page))
		}
	}
	c.Log.LogAttrs(ctx, slog.LevelWarn, message, attrs...)
}

func failureCode(err error) string {
	var integrity *integrityError
	if errors.As(err, &integrity) {
		return integrity.code
	}
	switch {
	case errors.Is(err, errScope):
		return "scope_mismatch"
	case errors.Is(err, client.ErrDenied):
		return "access_denied"
	case errors.Is(err, client.ErrChallenge):
		return "verification_required"
	case errors.Is(err, auth.ErrLogin):
		return "authentication_failed"
	case errors.Is(err, client.ErrBudget):
		return "request_budget_exhausted"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "task_deadline_exceeded"
	case errors.Is(err, client.ErrExpired):
		return "session_expired"
	case errors.Is(err, client.ErrTooLarge):
		return "response_too_large"
	case errors.Is(err, client.ErrProtocol):
		return "protocol_error"
	case errors.Is(err, client.ErrNetwork):
		return "network_error"
	case errors.Is(err, client.ErrUnavailable):
		return "upstream_unavailable"
	default:
		return "unknown_error"
	}
}

var safeDate = regexp.MustCompile(`^[0-9]{4}[-/][0-9]{1,2}[-/][0-9]{1,2}$`)
var diagnosticID = regexp.MustCompile(`^[0-9]{1,6}$`)

func echoDiagnostic(field string, values []string) string {
	if len(values) > 1 {
		return "<重复字段>"
	}
	if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
		return "<空>"
	}
	value := strings.TrimSpace(values[0])
	switch field {
	case "cycleTime.dateBegin", "cycleTime.dateEnd":
		if safeDate.MatchString(value) {
			return value
		}
	case "timeBegin", "timeEnd":
		if n, err := strconv.Atoi(value); len(value) <= 3 && err == nil && n >= 0 && n <= 99 {
			return strconv.Itoa(n)
		}
	case "classroom.building.id", "pageNo":
		if diagnosticID.MatchString(value) {
			return value
		}
	}
	return "<已脱敏：格式不符>"
}
