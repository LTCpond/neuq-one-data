package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/exporter"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

type schoolTransport func(*http.Request) (*http.Response, error)

func (f schoolTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoFreshSlotsPreservesLastPublishedSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	previous := filepath.Join(dir, "published")
	output := filepath.Join(dir, "pending")
	buildings := []string{"科技楼"}
	now := time.Now()
	day := model.NewDay(model.Date(now), buildings)
	day.Buildings["科技楼"]["1-2"] = model.NewSlot(map[string]model.RoomDetail{"501": {}}, now, 2*time.Hour)
	if err := exporter.Write(previous, model.NewWeek([]model.Day{day}, now)); err != nil {
		t.Fatal(err)
	}
	previousWeek := filepath.Join(previous, "api", "v1", "classrooms", "week.json")
	t.Setenv("NEUQ_USERNAME", "mock-account")
	t.Setenv("NEUQ_PASSWORD", "mock-password")
	t.Setenv("NEUQ_USE_WEBVPN", "false")
	t.Setenv("NEUQ_BUILDINGS", "科技楼")
	t.Setenv("NEUQ_DAYS", "1")
	t.Setenv("NEUQ_REQUEST_DELAY", "1s")
	t.Setenv("NEUQ_MAX_REQUESTS", "3")
	t.Setenv("NEUQ_SLOT_RETRIES", "2")
	searches := 0
	savedClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = savedClient })
	http.DefaultClient = &http.Client{Transport: schoolTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		switch r.URL.Path {
		case "/eams/homeExt.action":
			body = `<p>教务主页</p>`
		case "/eams/" + auth.FormPath:
			body = `<select name="classroom.building.id"><option value="7">科技楼</option></select>`
		case "/eams/" + auth.SearchPath:
			searches++
			body = `<p>无法识别的结果</p>`
		default:
			return nil, fmt.Errorf("mock received an unexpected route")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html; charset=utf-8"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := collect(t.Context(), "today", output, previousWeek, logger); err == nil || !strings.Contains(err.Error(), "保留上次发布结果") {
		t.Fatalf("整体失败应该禁止生成发布快照，错误 %v", err)
	}
	if searches != 1 {
		t.Fatalf("请求预算未限制实际查询次数，收到 %d 次查询", searches)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("整体失败后不应创建待发布目录，得到 %v", err)
	}
	if err := exporter.Verify(previous); err != nil {
		t.Fatal(err)
	}
	old, err := exporter.LoadPrevious(t.Context(), previousWeek)
	if err != nil || !old.GeneratedAt.Equal(now) || old.Status().FreshSlots != 1 ||
		old.Days[0].Buildings["科技楼"]["1-2"].Rooms[0] != "501" {
		t.Fatalf("原有发布快照有变化：%+v，错误 %v", old, err)
	}
}
