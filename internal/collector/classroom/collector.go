package classroom

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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
	campusBuildings     []string
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
		c.campusBuildings = readCampusBuildings(form)
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
	c.warnQuery(ctx, "全校时段查询失败", err, slog.String("date", day.Date), slog.String("period", period))
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
			c.warnQuery(ctx, "楼栋时段补查失败", err, slog.String("date", day.Date), slog.String("period", period), slog.String("building", building))
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
			return grid{}, &scopeMismatch{source: "building.option", field: "classroom.building.id", expected: "<已确认编号>", actual: "<空>", page: number}
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
			return grid{}, &scopeMismatch{source: "building.option", field: "classroom.building.id",
				expected: echoDiagnostic("classroom.building.id", []string{id}),
				actual:   echoDiagnostic("classroom.building.id", []string{c.ids[building]}), page: number}
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
	var scopeFailure error
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
		var diagnostics gridDiagnostics
		for row, r := range g.rows {
			diagnostics.observe(r)
			if noticeRoom(r.name) {
				diagnostics.excluded++
				continue
			}
			actual, err := c.resolveBuilding(r, building, g.buildingConfirmed, page, row+1)
			if err != nil {
				diagnostics.unresolved++
				if scopeFailure == nil {
					scopeFailure = err
				}
				continue
			}
			keyBuilding := actual
			if keyBuilding == "" {
				keyBuilding = c.canonicalBuilding(r.building)
			}
			key := [2]string{r.name, keyBuilding}
			if previous := seen[key]; previous != 0 && previous != page {
				return nil, errResult
			}
			seen[key] = page
			target, configured := found[actual]
			if !configured {
				if actual == "" {
					diagnostics.excluded++
				} else {
					diagnostics.otherBuildings++
				}
				continue
			}
			name, detail, publishable := collectRoom(r, actual)
			if !publishable {
				diagnostics.unsupportedNames++
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
		diagnostics.log(ctx, c.Log, date, start, building, page, len(g.rows))
	}
	if total >= 0 && rawCount != total {
		return nil, errResult
	}
	if scopeFailure != nil {
		return nil, scopeFailure
	}
	return found, nil
}

func (c *Collector) resolveBuilding(r room, requested string, buildingConfirmed bool, page, row int) (string, error) {
	column := c.canonicalBuilding(r.building)
	fromName := c.buildingFromRoomName(r.name)
	excluded := excludedRoom(r)

	if requested != "" {
		switch {
		case column != "" && column != requested:
			return "", c.buildingMismatch(r, "grid.building", requested, column, page, row)
		case fromName != "" && fromName != requested:
			return "", c.buildingMismatch(r, "grid.roomName", requested, fromName, page, row)
		}
	}
	if excluded {
		return "", nil
	}
	if column != "" && fromName != "" && column != fromName {
		return "", c.buildingMismatch(r, "grid.building", fromName, column, page, row)
	}

	actual := column
	if actual == "" {
		actual = fromName
	}
	if actual == "" && requested != "" && buildingConfirmed && model.RoomName.MatchString(strings.ToUpper(r.name)) {
		// 无楼栋前缀的编号只在结果回显确认了楼栋过滤时才使用。
		actual = requested
	}
	if actual == "" || !c.knownBuilding(actual) {
		return "", c.buildingMismatch(r, "grid.building", requested, actual, page, row)
	}
	if r.name == "" && slices.Contains(c.Buildings, actual) {
		return "", &scopeMismatch{source: "grid.roomName", field: "roomName", expected: "<可识别教室>", actual: "<空>",
			page: page, row: row, shape: diagnoseRoom(r)}
	}
	return actual, nil
}

func (c *Collector) canonicalBuilding(raw string) string {
	label := compact(raw)
	if !slices.Contains(c.Buildings, label) {
		label = stripCampusLabel(label)
	}
	if label == "综合楼" && !slices.Contains(c.Buildings, label) {
		return "综合实验楼"
	}
	return label
}

func (c *Collector) knownBuilding(name string) bool {
	return slices.Contains(c.Buildings, name) || slices.Contains(c.campusBuildings, name) || slices.Contains(referencedBuildings, name)
}

func (c *Collector) buildingFromRoomName(raw string) string {
	name := noteSuffix.ReplaceAllString(strings.ToUpper(stripCampusLabel(raw)), "")
	if matches := scienceStudy.FindStringSubmatch(name); matches != nil && model.RoomName.MatchString(matches[2]) {
		return "科技楼"
	}
	for _, buildings := range [][]string{c.Buildings, c.campusBuildings, referencedBuildings} {
		for _, building := range buildings {
			suffix, ok := strings.CutPrefix(name, building)
			if ok && (suffix == "" || model.RoomName.MatchString(suffix) ||
				slices.ContainsFunc(specialRooms, func(label string) bool { return strings.HasPrefix(suffix, label) })) {
				return c.canonicalBuilding(building)
			}
		}
	}
	return ""
}

func (c *Collector) buildingMismatch(r room, source, expected, actual string, page, row int) error {
	if expected == "" {
		expected = "<可识别楼栋>"
	}
	switch {
	case actual == "":
		actual = "<空>"
	case !slices.Contains(c.Buildings, actual) && actual != "科技楼":
		// 配置以外的楼栋单元格可能含任意文本。
		actual = "<已脱敏：未识别楼栋>"
	}
	if !slices.Contains(c.Buildings, expected) && expected != "<可识别楼栋>" && expected != "科技楼" {
		expected = "<其他已知楼栋>"
	}
	return &scopeMismatch{source: source, field: "building", expected: expected, actual: actual, page: page, row: row,
		shape: diagnoseRoom(r)}
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
