package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/client"
	"github.com/LTCpond/neuq-one-data/internal/collector/classroom"
	"github.com/LTCpond/neuq-one-data/internal/config"
	"github.com/LTCpond/neuq-one-data/internal/exporter"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

func main() {
	mode := flag.String("mode", "week", "采集范围（today 或 week）")
	output := flag.String("output", "public", "待发布目录")
	previous := flag.String("previous", "", "上次发布的 HTTPS 站点根地址或本地 week.json 路径")
	verify := flag.Bool("verify", false, "校验待发布目录")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var err error
	if *verify {
		err = exporter.Verify(*output)
	} else {
		err = collect(ctx, *mode, *output, *previous, logger)
	}
	if err != nil {
		logger.Error("任务结束，未生成可部署快照", "reason", err)
		os.Exit(1)
	}
}

func collect(ctx context.Context, mode, output, previous string, logger *slog.Logger) error {
	if mode != "today" && mode != "week" {
		return errors.New("采集范围必须是 today 或 week")
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	if c.Username == "" || c.Password == "" {
		return errors.New("请设置 NEUQ_USERNAME 和 NEUQ_PASSWORD")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("输出目录必须尚未创建")
	}

	// 工作流并发组覆盖 GitHub runner，锁文件覆盖同一工作目录的本地进程。
	lock, err := os.OpenFile(".collector.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("无法获得采集锁，请检查是否已有采集任务")
	}
	lock.Close()
	defer os.Remove(lock.Name())

	ctx, cancel := context.WithTimeout(ctx, c.TaskTimeout)
	defer cancel()
	old, err := exporter.LoadPrevious(ctx, previous)
	if err != nil {
		return err
	}

	start := time.Now()
	if old.GeneratedAt.After(start) {
		return errors.New("历史快照发布时间晚于当前时间")
	}
	routes := auth.Official(c)
	school := client.New(http.DefaultClient, routes.Allow, c.Timeout, c.Delay, c.MaxRequests, c.Retries)
	collector := classroom.Collector{
		Session:   &auth.Session{HTTP: school, Routes: routes, Username: c.Username, Password: c.Password},
		Buildings: c.Buildings, MaxPages: c.MaxPages, SlotRetries: c.SlotRetries,
		TodayTTL: c.TodayTTL, FutureTTL: c.FutureTTL, Log: logger,
	}
	collectDays := c.Days
	if mode == "today" {
		collectDays = 1
	}
	newData := collector.Run(ctx, start, c.Days, collectDays)
	if errors.Is(newData.Failure, client.ErrDenied) || errors.Is(newData.Failure, client.ErrChallenge) ||
		errors.Is(newData.Failure, auth.ErrLogin) || errors.Is(newData.Failure, client.ErrExpired) {
		return newData.Failure
	}
	if newData.SuccessfulSlots == 0 {
		return errors.New("本次没有成功采集的时段，保留上次发布结果")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	historicalDays := map[string]model.Day{}
	for _, day := range old.Days {
		historicalDays[day.Date] = day
	}
	now := time.Now()
	if model.Date(start) != model.Date(now) {
		return errors.New("采集期间北京时间跨天，请使用下一次计划任务")
	}
	for i := range newData.Days {
		day := &newData.Days[i]
		day.Carry(historicalDays[day.Date], i < collectDays, now)
	}
	week := model.NewWeek(newData.Days, now)
	if err := exporter.Write(output, week); err != nil {
		return err
	}
	status := week.Status()
	logger.Info("可部署快照已生成", "date", status.Date, "state", status.State, "fresh", status.FreshSlots, "stale", status.StaleSlots, "unavailable", status.UnavailableSlots)
	return nil
}
