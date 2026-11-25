package exporter

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/model"
)

func sampleWeek(now time.Time) model.Week {
	buildings := []string{"工学馆", "科技楼", "人文楼"}
	today := model.NewDay(model.Date(now), buildings)
	tomorrow := model.NewDay(model.Date(now.In(model.Beijing).AddDate(0, 0, 1)), buildings)
	today.Buildings["工学馆"]["1-2"] = model.NewSlot(map[string]model.RoomDetail{}, now.Add(-time.Minute), time.Hour)
	return model.NewWeek([]model.Day{today, tomorrow}, now)
}

func TestPublishRoundTripAndStates(t *testing.T) {
	now := time.Date(2026, 11, 24, 16, 10, 0, 0, time.UTC)
	w := sampleWeek(now)
	dest := filepath.Join(t.TempDir(), "public")
	if err := Write(dest, w); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest); err != nil {
		t.Fatal(err)
	}
	today, err := os.ReadFile(filepath.Join(dest, todayFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"date": "2026-11-25"`, `"generatedAt": "2026-11-25T00:10:00+08:00"`, `"updatedAt": "2026-11-25T00:09:00+08:00"`,
		`"complete": false`, `"rooms": []`, `"rooms": null`, `"roomDetails": {}`, `"status": "unavailable"`,
	} {
		if !bytes.Contains(today, []byte(expected)) {
			t.Errorf("today.json 缺少 %s", expected)
		}
	}
	previous, err := LoadPrevious(context.Background(), filepath.Join(dest, weekFile))
	if err != nil || previous.Days[1].Date != "2026-11-26" || previous.Status().FreshSlots != 1 {
		t.Fatalf("历史结果 %v：%v", previous.Header, err)
	}
	if err := Write(dest, w); err == nil {
		t.Fatal("覆盖了已有发布目录")
	}
}

func TestHistoricalMergeAndExpiry(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 15, 55, 0, 0, time.UTC)
	prev := sampleWeek(t0)
	date := "2026-11-25"
	old := &prev.Days[1]
	collected := t0.Add(-5 * time.Minute)
	old.Buildings["科技楼"]["3-4"] = model.NewSlot(map[string]model.RoomDetail{"201": {}}, collected, 26*time.Hour)
	now := t0.Add(20 * time.Minute)
	d := model.NewDay(date, []string{"工学馆", "科技楼", "人文楼"})
	d.Carry(prev.Days[0], true, now)
	if d.Buildings["工学馆"]["1-2"].Status != model.Unavailable {
		t.Fatal("将昨天的时段用于今天")
	}
	d.Carry(*old, true, now)
	w := model.NewWeek([]model.Day{d}, now)
	slot := w.Days[0].Buildings["科技楼"]["3-4"]
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	if slot.Status != model.Stale || !slot.CollectedAt.Equal(collected) || !slot.ExpiresAt.Equal(collected.Add(26*time.Hour)) || !d.Buildings["科技楼"]["3-4"].CollectedAt.Equal(collected) ||
		!w.Days[0].UpdatedAt.Equal(collected) || w.Days[0].Complete {
		t.Fatalf("历史采集时刻被覆盖或状态错误：%#v", slot)
	}
	d = model.NewDay(date, []string{"工学馆", "科技楼", "人文楼"})
	d.Carry(*old, false, now)
	if d.Buildings["科技楼"]["3-4"].Status != model.Success {
		t.Fatal("提前标记未查询时段为过期")
	}
	d.Recalculate(collected.Add(26 * time.Hour))
	if d.Buildings["科技楼"]["3-4"].Status != model.Stale {
		t.Fatal("过期数据仍标为 success")
	}
}

func TestInvalidPublish(t *testing.T) {
	now := time.Date(2026, 11, 25, 8, 10, 0, 0, model.Beijing)
	for _, tc := range []struct {
		name   string
		change func(*model.Week)
	}{
		{"missing period", func(w *model.Week) { delete(w.Days[0].Buildings["工学馆"], "3-4") }},
		{"wrong date", func(w *model.Week) { w.Days[0].Date = "2026-11-24" }},
		{"invalid status", func(w *model.Week) { w.Days[0].Buildings["工学馆"]["1-2"] = model.Slot{Status: model.Success} }},
		{"personal data", func(w *model.Week) {
			slot := w.Days[0].Buildings["工学馆"]["1-2"]
			slot.Rooms = []string{"201"}
			label := "未核实文本"
			slot.RoomDetails = map[string]model.RoomDetail{"201": {Equipment: &label}}
			w.Days[0].Buildings["工学馆"]["1-2"] = slot
		}},
		{"no fresh slots", func(w *model.Week) {
			w.Days[0].Buildings["工学馆"]["1-2"] = model.Slot{Status: model.Unavailable}
			w.Days[0].Recalculate(now)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := sampleWeek(now)
			tc.change(&w)
			dest := filepath.Join(t.TempDir(), "public")
			if err := Write(dest, w); err == nil {
				t.Fatal("无效数据可以发布")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("产出了不完整的目录")
			}
		})
	}
}

func TestVerifyRejectsMismatchedVersionAndExtraFiles(t *testing.T) {
	w := sampleWeek(time.Date(2026, 11, 25, 8, 10, 0, 0, model.Beijing))
	root := filepath.Join(t.TempDir(), "public")
	if err := Write(root, w); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(root, statusFile)
	original, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte(strings.Replace(string(original), "08:10:00", "08:11:00", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root); err == nil {
		t.Fatal("不同版本通过了校验")
	}
	if err := os.WriteFile(statusPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(root); err == nil {
		t.Fatal("未声明文件通过了校验")
	}
}
