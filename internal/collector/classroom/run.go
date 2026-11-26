package classroom

import (
	"context"
	"log/slog"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/client"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

type Result struct {
	Days                 []model.Day
	FirstSuccessfulSlots int
	RetryAttempts        int
	RetrySuccessfulSlots int
	SuccessfulSlots      int
	FailedSlots          int
	Failure              error
}

type slotQuery struct {
	day              *model.Day
	building, period string
	start            int
	ttl              time.Duration
	failure          error
}

func (c *Collector) Run(ctx context.Context, start time.Time, horizon, collectDays int) (result Result) {
	result.Days = make([]model.Day, horizon)
	for i := range result.Days {
		result.Days[i] = model.NewDay(model.Date(start.In(model.Beijing).AddDate(0, 0, i)), c.Buildings)
	}
	queries := make([]slotQuery, 0, collectDays*len(model.Periods)*len(c.Buildings))
	for i := range collectDays {
		ttl := c.FutureTTL
		if i == 0 {
			ttl = c.TodayTTL
		}
		for j, period := range model.Periods {
			for _, building := range c.Buildings {
				queries = append(queries, slotQuery{day: &result.Days[i], building: building, period: period, start: 2*j + 1, ttl: ttl})
			}
		}
	}
	attemptFailures := map[string]int{}
	defer func() { c.summarize(ctx, &result, queries, collectDays, attemptFailures) }()
	if err := c.prepare(ctx); err != nil {
		result.Failure = err
		return
	}

	var pending []*slotQuery
	for i := range queries {
		query := &queries[i]
		if err := c.collectSlot(ctx, query, 0, attemptFailures); err == nil {
			result.FirstSuccessfulSlots++
		} else if stop(err) {
			result.Failure = err
			return
		} else if retryableIntegrity(err) {
			pending = append(pending, query)
		}
	}

	for round := 1; round <= c.SlotRetries && len(pending) > 0; round++ {
		if err := c.retryAllowed(ctx); err != nil {
			result.Failure = err
			return
		}
		backoff := time.Second << (round - 1)
		c.Log.InfoContext(ctx, "等待补偿采集", "retry_round", round, "pending_slots", len(pending), "backoff", backoff)
		if err := waitForRetry(ctx, backoff); err != nil {
			result.Failure = err
			return
		}
		next := pending[:0]
		for _, query := range pending {
			if err := c.retryAllowed(ctx); err != nil {
				result.Failure = err
				return
			}
			result.RetryAttempts++
			if err := c.collectSlot(ctx, query, round, attemptFailures); err == nil {
				result.RetrySuccessfulSlots++
			} else if stop(err) {
				result.Failure = err
				return
			} else if retryableIntegrity(err) {
				next = append(next, query)
			}
		}
		pending = next
	}
	return
}

func (c *Collector) collectSlot(ctx context.Context, query *slotQuery, round int, attemptFailures map[string]int) error {
	rooms, err := c.fetch(ctx, query.day.Date, query.start, query.building)
	query.failure = err
	attrs := []slog.Attr{slog.String("date", query.day.Date), slog.String("building", query.building),
		slog.String("period", query.period), slog.Int("attempt", round+1)}
	if err != nil {
		code := failureCode(err)
		attemptFailures[code]++
		c.warnQuery(ctx, "楼栋时段查询失败", err, append(attrs, slog.String("error_code", code))...)
		return err
	}
	query.day.Buildings[query.building][query.period] = model.NewSlot(rooms, time.Now(), query.ttl)
	if round > 0 {
		c.Log.LogAttrs(ctx, slog.LevelInfo, "楼栋时段补偿采集成功", attrs...)
	}
	return nil
}

func (c *Collector) retryAllowed(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.Session.HTTP.BudgetExhausted() {
		return client.ErrBudget
	}
	return nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func (c *Collector) summarize(ctx context.Context, result *Result, queries []slotQuery, collectDays int, attempts map[string]int) {
	result.SuccessfulSlots = result.FirstSuccessfulSlots + result.RetrySuccessfulSlots
	result.FailedSlots = len(queries) - result.SuccessfulSlots
	terminal := result.Failure
	failures := map[string]int{}
	for _, query := range queries {
		if query.day.Buildings[query.building][query.period].Status == model.Success {
			continue
		}
		if query.failure == nil {
			failures["not_attempted"]++
			continue
		}
		failures[failureCode(query.failure)]++
		if terminal == nil {
			result.Failure = query.failure
		}
	}
	for _, day := range result.Days[:collectDays] {
		for _, building := range c.Buildings {
			success := 0
			for _, slot := range day.Buildings[building] {
				if slot.Status == model.Success {
					success++
				}
			}
			c.Log.InfoContext(ctx, "楼栋采集统计", "date", day.Date, "building", building,
				"success", success, "failed", len(model.Periods)-success)
		}
	}
	attrs := []slog.Attr{slog.Int("requests", c.Session.HTTP.Count()), slog.Int("planned", len(queries)),
		slog.Int("first_success", result.FirstSuccessfulSlots), slog.Int("retry_attempts", result.RetryAttempts),
		slog.Int("retry_success", result.RetrySuccessfulSlots), slog.Int("success", result.SuccessfulSlots),
		slog.Int("failed", result.FailedSlots), slog.Any("failures_by_code", failures), slog.Any("attempt_failures_by_code", attempts)}
	if terminal != nil {
		attrs = append(attrs, slog.String("stop_code", failureCode(terminal)))
	}
	c.Log.LogAttrs(ctx, slog.LevelInfo, "采集统计", attrs...)
}
