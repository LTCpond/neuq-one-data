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
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxRequests != 200 || c.TaskTimeout != 12*time.Minute || !slices.Equal(c.Buildings, []string{"工学馆", "科技楼", "人文楼"}) {
		t.Fatalf("采集默认值错误：楼栋 %v、请求上限 %d、任务超时 %s", c.Buildings, c.MaxRequests, c.TaskTimeout)
	}
}
