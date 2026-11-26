package classroom

import (
	"errors"
	"log/slog"
)

var errResult = errors.New("无法确认空教室结果的完整性")

const (
	codePageLimit        = "page_limit_exceeded"
	codePageTotalChanged = "page_total_changed"
	codeRecordChanged    = "record_total_changed"
	codePageSizeChanged  = "page_size_changed"
	codePageRows         = "page_row_count_mismatch"
	codeRecordCount      = "record_count_mismatch"
	codeDuplicate        = "cross_page_duplicate"
	codeRoomConflict     = "room_attribute_conflict"
	codeTableStructure   = "html_table_structure"
	codePagingMissing    = "pagination_metadata_missing"
	codePagingInvalid    = "pagination_metadata_invalid"
	codePagingConflict   = "pagination_metadata_conflict"
)

// 字段只使用已检查的计数、位置或代码中的固定值。
type integrityError struct {
	code   string
	reason string
	attrs  []slog.Attr
}

func (e *integrityError) Error() string { return errResult.Error() + "：" + e.reason }
func (e *integrityError) Unwrap() error { return errResult }

func (e *integrityError) with(attrs ...slog.Attr) *integrityError {
	e.attrs = append(e.attrs, attrs...)
	return e
}

func integrity(code, reason string, attrs ...slog.Attr) *integrityError {
	return &integrityError{code: code, reason: reason, attrs: attrs}
}

func retryableIntegrity(err error) bool {
	var failure *integrityError
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.code {
	case codePageTotalChanged, codeRecordChanged, codePageSizeChanged, codePageRows, codeRecordCount, codeDuplicate:
		return true
	default:
		return false
	}
}

type pageFailure struct {
	error
	page int
}

func (e *pageFailure) Unwrap() error { return e.error }
