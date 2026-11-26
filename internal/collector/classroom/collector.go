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
	Session               *auth.Session
	Buildings             []string
	MaxPages, SlotRetries int
	TodayTTL, FutureTTL   time.Duration
	Log                   *slog.Logger
	ids                   map[string]string
	campusBuildings       []string
	pageSizes             map[string]int
	renewed               bool
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

func (c *Collector) page(ctx context.Context, date string, start, number, size int, building string) (grid, error) {
	for {
		id := c.ids[building]
		if !numberID.MatchString(id) {
			return grid{}, &scopeMismatch{source: "building.option", field: "classroom.building.id", expected: "<已确认编号>", actual: "<空>", page: number}
		}
		form := query(date, start, number, size, id)
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
		if id != c.ids[building] {
			return grid{}, &scopeMismatch{source: "building.option", field: "classroom.building.id",
				expected: echoDiagnostic("classroom.building.id", []string{id}),
				actual:   echoDiagnostic("classroom.building.id", []string{c.ids[building]}), page: number}
		}
	}
}

func (c *Collector) logPageSize(ctx context.Context, building string, g grid) {
	if previous, exists := c.pageSizes[building]; exists && previous == g.size {
		return
	}
	if c.pageSizes == nil {
		c.pageSizes = make(map[string]int)
	}
	c.pageSizes[building] = g.size
	c.Log.InfoContext(ctx, "教务分页大小", "building", building, "requested_page_size", preferredPageSize,
		"reported_page_size", g.size, "reported_total_records", g.total, "actual_records", len(g.rows))
}

func (c *Collector) resolveBuilding(r room, requested string, buildingConfirmed bool, page, row int) (string, error) {
	column := c.canonicalBuilding(r.building)
	fromName := c.buildingFromRoomName(r.name)
	switch {
	case column != "" && column != requested:
		return "", c.buildingMismatch(r, "grid.building", requested, column, page, row)
	case fromName != "" && fromName != requested:
		return "", c.buildingMismatch(r, "grid.roomName", requested, fromName, page, row)
	}
	if excludedRoom(r) {
		return "", nil
	}
	// 无楼栋前缀的编号只在结果回显确认了楼栋过滤时才使用。
	if column == "" && fromName == "" && !(buildingConfirmed && model.RoomName.MatchString(strings.ToUpper(r.name))) {
		return "", c.buildingMismatch(r, "grid.building", requested, "", page, row)
	}
	if r.name == "" {
		return "", &scopeMismatch{source: "grid.roomName", field: "roomName", expected: "<可识别教室>", actual: "<空>",
			page: page, row: row, shape: diagnoseRoom(r)}
	}
	return requested, nil
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
	switch {
	case actual == "":
		actual = "<空>"
	case !slices.Contains(c.Buildings, actual) && actual != "科技楼":
		// 配置以外的楼栋单元格可能含任意文本。
		actual = "<已脱敏：未识别楼栋>"
	}
	return &scopeMismatch{source: source, field: "building", expected: expected, actual: actual, page: page, row: row,
		shape: diagnoseRoom(r)}
}

func mergeRoom(old, next model.RoomDetail) (model.RoomDetail, *integrityError) {
	if old.Capacity != nil && next.Capacity != nil && *old.Capacity != *next.Capacity {
		return next, integrity(codeRoomConflict, "同一教室的容量不同", slog.String("field", "capacity"))
	}
	if old.Equipment != nil && next.Equipment != nil && *old.Equipment != *next.Equipment {
		return next, integrity(codeRoomConflict, "同一教室的设备分类不同", slog.String("field", "equipment"))
	}
	if next.Capacity == nil {
		next.Capacity = old.Capacity
	}
	if next.Equipment == nil {
		next.Equipment = old.Equipment
	}
	return next, nil
}
