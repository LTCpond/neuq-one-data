package classroom

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/client"
	"github.com/LTCpond/neuq-one-data/internal/exporter"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

func TestSlotCompensation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		tryToSucceed int
	}{
		{"first pass succeeds", 1},
		{"compensation succeeds", 2},
		{"second compensation succeeds", 3},
		{"compensations exhausted", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var order []string
			var successfulResponse time.Time
			visits := 0
			c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				building, period, page := r.FormValue("classroom.building.id"), r.FormValue("timeBegin"), r.FormValue("pageNo")
				order = append(order, building+":"+period+":"+page)
				if building != "7" || period != "1" {
					fmt.Fprint(w, "<p>没有查询结果</p>")
					return
				}
				if page == "" {
					visits++
					name := "科技楼201"
					if visits == tc.tryToSucceed {
						name = "科技楼301"
					}
					writeGrid(w, scienceRow(name), 1, 1, 2)
					return
				}
				if visits == tc.tryToSucceed {
					successfulResponse = time.Now()
					writeGrid(w, scienceRow("科技楼302"), 2, 1, 2)
					return
				}
				writeGrid(w, scienceRow("科技楼201"), 2, 1, 2)
			}))
			c.SlotRetries = 2
			var logs bytes.Buffer
			c.Log = slog.New(slog.NewJSONHandler(&logs, nil))
			start := time.Now()
			result := c.Run(t.Context(), start, 1, 1)
			mu.Lock()
			defer mu.Unlock()
			wantVisits := tc.tryToSucceed
			if wantVisits == 0 {
				wantVisits = 3
			}
			if visits != wantVisits || result.RetryAttempts != wantVisits-1 || len(order) != 19+2*(wantVisits-1) ||
				order[1] != "7:1:" || order[2] != "7:1:2" || order[18] != "9:11:" || c.Session.HTTP.Count() != len(order)+2 {
				t.Fatalf("查询次数或顺序错误 %v，汇总 %+v", order, result)
			}
			for i := 19; i < len(order); i += 2 {
				if order[i] != "7:1:" || order[i+1] != "7:1:2" {
					t.Fatalf("补偿没有从第一页重查：%v", order[i:])
				}
			}
			slot := result.Days[0].Buildings["科技楼"]["1-2"]
			if tc.tryToSucceed == 0 {
				if result.FirstSuccessfulSlots != 17 || result.RetrySuccessfulSlots != 0 || result.SuccessfulSlots != 17 || result.FailedSlots != 1 ||
					!errors.Is(result.Failure, errResult) || slot.Status != model.Unavailable || slot.CollectedAt != nil {
					t.Fatalf("失败的尝试影响了其他时段或保留了部分结果：%+v", result)
				}
				old := model.NewDay(result.Days[0].Date, c.Buildings)
				oldTime := start.Add(-time.Minute)
				old.Buildings["科技楼"]["1-2"] = model.NewSlot(map[string]model.RoomDetail{"401": {}}, oldTime, c.TodayTTL)
				result.Days[0].Carry(old, true, time.Now())
				carried := result.Days[0].Buildings["科技楼"]["1-2"]
				if carried.Status != model.Stale || carried.CollectedAt == nil || !carried.CollectedAt.Equal(oldTime) || !slices.Equal(carried.Rooms, []string{"401"}) {
					t.Fatalf("历史数据回退错误：%+v", carried)
				}
				assertLog(t, logs.String(), `"failures_by_code":{"cross_page_duplicate":1}`, `"attempt_failures_by_code":{"cross_page_duplicate":3}`,
					`"building":"科技楼","success":5,"failed":1`, `"building":"工学馆","success":6,"failed":0`)
				return
			}
			wantRetrySuccess := 0
			if tc.tryToSucceed > 1 {
				wantRetrySuccess = 1
			}
			if result.SuccessfulSlots != 18 || result.FailedSlots != 0 || result.Failure != nil ||
				result.RetrySuccessfulSlots != wantRetrySuccess || result.FirstSuccessfulSlots != 18-wantRetrySuccess ||
				slot.Status != model.Success || !slices.Equal(slot.Rooms, []string{"301", "302"}) ||
				slot.CollectedAt == nil || slot.ExpiresAt == nil || slot.CollectedAt.Before(successfulResponse) || slot.ExpiresAt.Sub(*slot.CollectedAt) != c.TodayTTL {
				t.Fatalf("采集结果、时间戳或统计错误：%+v，楼栋时段 %+v", result, slot)
			}
			week := model.NewWeek(result.Days, time.Now())
			status := week.Status()
			if status.FreshSlots != 18 || status.StaleSlots != 0 || status.UnavailableSlots != 0 || !status.TodayComplete ||
				(tc.tryToSucceed > 1 && !week.Days[0].UpdatedAt.Equal(*slot.CollectedAt)) {
				t.Fatalf("快照统计错误：%+v", status)
			}
			// 采集跨午夜时，命令行入口会跳过导出。
			if model.Date(start) == model.Date(week.GeneratedAt) {
				if err := exporter.Write(filepath.Join(t.TempDir(), "snapshot"), week); err != nil {
					t.Fatal(err)
				}
			}
			assertLog(t, logs.String(), `"failures_by_code":{}`, `"building":"科技楼","success":6,"failed":0`,
				fmt.Sprintf(`"first_success":%d,"retry_attempts":%d,"retry_success":%d,"success":18,"failed":0`,
					18-wantRetrySuccess, wantVisits-1, wantRetrySuccess), fmt.Sprintf(`"requests":%d`, len(order)+2))
		})
	}
}

func TestDoesNotCompensateDeterministicFailures(t *testing.T) {
	const hidden = "mock-cookie-token-private-value"
	c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("classroom.building.id") != "7" {
			fmt.Fprint(w, "<p>没有查询结果</p>")
			return
		}
		switch r.FormValue("timeBegin") {
		case "1":
			writeGrid(w, scienceRow("科技楼201"), 1, 1, 5)
		case "3":
			fmt.Fprint(w, `<p>`+hidden+`</p>`)
		case "5":
			writeGrid(w, `<tr><td>1</td><td>人文楼201</td><td>人文楼</td></tr>`, 1, 1, 2)
		case "7":
			writeGrid(w, `<tr><td>1</td><td>科技楼201</td><td>科技楼</td><td></td><td>普通教室</td><td>50</td></tr>`+
				`<tr><td>1</td><td>科技楼201</td><td>科技楼</td><td></td><td>普通教室</td><td>40</td></tr>`, 1, 2, 2)
		default:
			fmt.Fprint(w, "<p>没有查询结果</p>")
		}
	}))
	c.SlotRetries = 2
	var logs bytes.Buffer
	c.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	result := c.Run(t.Context(), time.Now(), 1, 1)
	if result.RetryAttempts != 0 || result.SuccessfulSlots != 14 || result.FailedSlots != 4 || c.Session.HTTP.Count() != 20 {
		t.Fatalf("确定性错误的处理有误：%+v", result)
	}
	output := logs.String()
	assertLog(t, output, `"error_code":"page_limit_exceeded"`, `"max_pages":4`, `"error_code":"html_table_structure"`,
		`"error_code":"scope_mismatch"`, `"error_code":"room_attribute_conflict"`, `"field":"capacity"`)
	if strings.Contains(output, hidden) || strings.Contains(output, "<p>") {
		t.Fatal("日志包含未经脱敏的响应")
	}
}

func TestRetryStopsForBudgetOrCancellation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		budget          int
		cancel          bool
		want            error
		code            string
		requests, tries int
	}{
		{"budget used by first pass", 21, false, client.ErrBudget, "request_budget_exhausted", 21, 0},
		{"budget used mid-slot", 22, false, client.ErrBudget, "request_budget_exhausted", 22, 1},
		{"canceled during backoff", 60, true, context.Canceled, "context_canceled", 21, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c := newCollectorWithBudget(t, false, time.Second, tc.budget, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				if r.FormValue("classroom.building.id") == "7" && r.FormValue("timeBegin") == "1" {
					page := 1
					if r.FormValue("pageNo") == "2" {
						page = 2
					}
					writeGrid(w, scienceRow("科技楼201"), page, 1, 2)
					return
				}
				fmt.Fprint(w, "<p>没有查询结果</p>")
			}))
			c.SlotRetries = 2
			var logs bytes.Buffer
			handler := slog.NewJSONHandler(&logs, nil)
			c.Log = slog.New(retryLogHandler{handler, func() {
				if tc.cancel {
					time.AfterFunc(25*time.Millisecond, cancel)
				}
			}})
			result := c.Run(ctx, time.Now(), 1, 1)
			if !errors.Is(result.Failure, tc.want) || result.RetryAttempts != tc.tries || result.RetrySuccessfulSlots != 0 ||
				result.SuccessfulSlots != 17 || result.FailedSlots != 1 || c.Session.HTTP.Count() != tc.requests {
				t.Fatalf("没有正确停止采集，收到 %+v，请求数 %d", result, c.Session.HTTP.Count())
			}
			assertLog(t, logs.String(), `"stop_code":"`+tc.code+`"`)
		})
	}
}

type retryLogHandler struct {
	slog.Handler
	started func()
}

func (h retryLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message == "等待补偿采集" {
		h.started()
	}
	return h.Handler.Handle(ctx, record)
}

func assertLog(t *testing.T, output string, snippets ...string) {
	t.Helper()
	for _, snippet := range snippets {
		if !strings.Contains(output, snippet) {
			t.Errorf("日志缺少 %s", snippet)
		}
	}
}
