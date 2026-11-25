package classroom

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/client"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

type Collector struct {
	Session             *auth.Session
	Buildings           []string
	MaxPages            int
	TodayTTL, FutureTTL time.Duration
	Log                 *slog.Logger
	ids                 map[string]string
	renewed             bool
}

type Result struct {
	Days            []model.Day
	SuccessfulSlots int
	Failure         error
}

func (c *Collector) Run(ctx context.Context, start time.Time, horizon, collectDays int) Result {
	result := Result{Days: make([]model.Day, horizon)}
	for i := range result.Days {
		result.Days[i] = model.NewDay(model.Date(start.In(model.Beijing).AddDate(0, 0, i)), c.Buildings)
	}
	if err := c.prepare(ctx); err != nil {
		result.Failure = err
		return result
	}
	for i := range collectDays {
		day := &result.Days[i]
		ttl := c.FutureTTL
		if i == 0 {
			ttl = c.TodayTTL
		}
		for j, period := range model.Periods {
			count, err := c.period(ctx, day, period, 2*j+1, ttl)
			result.SuccessfulSlots += count
			if err != nil {
				result.Failure = err
				if stop(err) {
					return result
				}
			}
		}
	}
	return result
}

func (c *Collector) prepare(ctx context.Context) error {
	form, err := c.Session.Prepare(ctx)
	if err == nil {
		c.ids = buildingIDs(form, c.Buildings)
	}
	return err
}

func stop(err error) bool {
	return errors.Is(err, client.ErrDenied) || errors.Is(err, client.ErrChallenge) || errors.Is(err, auth.ErrLogin) ||
		errors.Is(err, client.ErrExpired) || errors.Is(err, client.ErrBudget) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Collector) period(ctx context.Context, day *model.Day, period string, start int, ttl time.Duration) (int, error) {
	rooms, err := c.fetch(ctx, day.Date, start, "")
	if err == nil {
		collected := time.Now()
		for building, details := range rooms {
			day.Buildings[building][period] = model.NewSlot(details, collected, ttl)
		}
		return len(rooms), nil
	}
	c.Log.Warn("全校时段查询失败", "date", day.Date, "period", period, "reason", err)
	if !errors.Is(err, errResult) && !errors.Is(err, errScope) && !errors.Is(err, client.ErrTooLarge) {
		return 0, err
	}
	count := 0
	failed := err
	for _, building := range c.Buildings {
		if c.ids[building] == "" {
			c.Log.Warn("无法确认教务楼栋编号", "building", building)
			continue
		}
		rooms, err := c.fetch(ctx, day.Date, start, building)
		if err != nil {
			c.Log.Warn("楼栋时段补查失败", "date", day.Date, "period", period, "building", building, "reason", err)
			if stop(err) {
				return count, err
			}
			failed = err
			continue
		}
		day.Buildings[building][period] = model.NewSlot(rooms[building], time.Now(), ttl)
		count++
	}
	if count == len(c.Buildings) {
		failed = nil
	}
	return count, failed
}

func (c *Collector) page(ctx context.Context, date string, start, number int, building string) (grid, error) {
	for {
		id := c.ids[building]
		if building != "" && id == "" {
			return grid{}, errScope
		}
		form := query(date, start, number, id)
		response, err := c.Session.HTTP.Do(ctx, c.Session.Routes.Academic(auth.SearchPath), form, true, true)
		if err == nil {
			doc, parseErr := c.Session.SearchDocument(response)
			if parseErr == nil {
				return parseGrid(doc, form, number)
			}
			err = parseErr
		}
		if !errors.Is(err, client.ErrExpired) || c.renewed {
			return grid{}, err
		}
		c.renewed = true
		c.Log.Info("教务会话失效，重新认证一次")
		if err := c.prepare(ctx); err != nil {
			return grid{}, fmt.Errorf("%w，重新认证失败：%w", client.ErrExpired, err)
		}
		if building != "" && id != c.ids[building] {
			return grid{}, errScope
		}
	}
}

func (c *Collector) fetch(ctx context.Context, date string, start int, building string) (map[string]map[string]model.RoomDetail, error) {
	g, err := c.page(ctx, date, start, 1, building)
	if err != nil {
		return nil, err
	}
	pages, size, err := g.plan()
	if err != nil || pages > c.MaxPages {
		return nil, errResult
	}
	total, rawCount := g.total, 0
	found := make(map[string]map[string]model.RoomDetail, len(c.Buildings))
	for _, name := range c.Buildings {
		if building == "" || name == building {
			found[name] = map[string]model.RoomDetail{}
		}
	}
	seen := map[[2]string]int{}
	for page := 1; page <= pages; page++ {
		if page > 1 {
			g, err = c.page(ctx, date, start, page, building)
			if err != nil {
				return nil, err
			}
		}
		rawCount += len(g.rows)
		if (g.total >= 0 && total >= 0 && g.total != total) || (g.size > 0 && g.size != size) ||
			(g.pages > 0 && g.pages != pages) || (page < pages && len(g.rows) != size) ||
			(page == pages && (len(g.rows) > size || (page > 1 && len(g.rows) == 0))) {
			return nil, errResult
		}
		if total < 0 && g.total >= 0 {
			total = g.total
		}
		for _, r := range g.rows {
			if noteSuffix.ReplaceAllString(r.name, "") == "" {
				continue
			}
			actual, err := c.resolveBuilding(r, building)
			if err != nil {
				return nil, err
			}
			key := [2]string{r.name, actual}
			if previous := seen[key]; previous != 0 && previous != page {
				return nil, errResult
			}
			seen[key] = page
			target, configured := found[actual]
			if !configured {
				continue
			}
			name, detail, publishable := collectRoom(r, actual)
			if !publishable {
				continue
			}
			if old, exists := target[name]; exists {
				detail, err = mergeRoom(old, detail)
				if err != nil {
					return nil, err
				}
			}
			target[name] = detail
		}
	}
	if total >= 0 && rawCount != total {
		return nil, errResult
	}
	return found, nil
}

func (c *Collector) resolveBuilding(r room, requested string) (string, error) {
	match := ""
	for _, name := range c.Buildings {
		if strings.Contains(r.name, name) {
			if match != "" {
				return "", errScope
			}
			match = name
		}
	}
	if r.building != "" {
		if (match != "" && match != r.building) || (requested != "" && requested != r.building) {
			return "", errScope
		}
		return r.building, nil
	}
	if requested != "" && (match == "" || match == requested) {
		return requested, nil
	}
	if requested == "" && match != "" {
		return match, nil
	}
	return "", errScope
}

func mergeRoom(old, next model.RoomDetail) (model.RoomDetail, error) {
	if old.Capacity != nil && next.Capacity != nil && *old.Capacity != *next.Capacity {
		return next, errResult
	}
	if old.Equipment != nil && next.Equipment != nil && *old.Equipment != *next.Equipment {
		return next, errResult
	}
	if next.Capacity == nil {
		next.Capacity = old.Capacity
	}
	if next.Equipment == nil {
		next.Equipment = old.Equipment
	}
	return next, nil
}
