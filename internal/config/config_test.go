package config

import (
	"slices"
	"testing"
	"time"
)

func TestClassroomCollectionDefaults(t *testing.T) {
	t.Setenv("NEUQ_USE_WEBVPN", "false")
	t.Setenv("NEUQ_BUILDINGS", "")
	t.Setenv("NEUQ_MAX_REQUESTS", "")
	t.Setenv("NEUQ_TASK_TIMEOUT", "")
	t.Setenv("NEUQ_SLOT_RETRIES", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxRequests != 200 || c.TaskTimeout != 12*time.Minute || !slices.Equal(c.Buildings, []string{"工学馆", "科技楼", "人文楼"}) {
		t.Fatalf("采集默认值错误：楼栋 %v、请求上限 %d、任务超时 %s", c.Buildings, c.MaxRequests, c.TaskTimeout)
	}
	if c.SlotRetries != 2 {
		t.Fatalf("默认时段补偿次数 %d，期望 2", c.SlotRetries)
	}
}

func TestSlotRetriesConfiguration(t *testing.T) {
	t.Setenv("NEUQ_USE_WEBVPN", "false")
	t.Setenv("NEUQ_SLOT_RETRIES", "0")
	if c, err := Load(); err != nil || c.SlotRetries != 0 {
		t.Fatalf("无法关闭时段补偿，次数 %d，错误 %v", c.SlotRetries, err)
	}
	t.Setenv("NEUQ_SLOT_RETRIES", "5")
	if _, err := Load(); err == nil || err.Error() != "NEUQ_SLOT_RETRIES 必须为 0 至 4 的整数" {
		t.Fatalf("越界时段补偿配置的错误 = %v", err)
	}
}
