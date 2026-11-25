package classroom

import (
	"context"
	"log/slog"
	"strconv"
	"strings"

	"github.com/LTCpond/neuq-one-data/internal/model"
)

// 仅保留行结构与字段类别，原始单元格值不进入日志。
type roomShape struct {
	nameCell, buildingCell, nameKind, equipmentKind, capacityKind string
	cellCount                                                     int
}

func diagnoseRoom(r room) roomShape {
	shape := roomShape{nameCell: r.nameCell, buildingCell: r.buildingCell, nameKind: "named", equipmentKind: "unrecognized",
		capacityKind: "invalid", cellCount: r.cellCount}
	switch {
	case r.name == "":
		shape.nameKind = "blank"
	case model.RoomName.MatchString(strings.ToUpper(r.name)):
		shape.nameKind = "room_number"
	}
	switch {
	case r.equipment == "":
		shape.equipmentKind = "blank"
	case specialRoom(r.equipment):
		shape.equipmentKind = "excluded"
	case model.Equipment(r.equipment) != nil:
		shape.equipmentKind = "known"
	}
	capacity, err := strconv.Atoi(r.capacity)
	switch {
	case r.capacity == "":
		shape.capacityKind = "blank"
	case err == nil && capacity == 0:
		shape.capacityKind = "zero"
	case err == nil && capacity > 0:
		shape.capacityKind = "positive"
	}
	return shape
}

func (s roomShape) attrs() []slog.Attr {
	return []slog.Attr{
		slog.String("name_cell", s.nameCell), slog.String("building_cell", s.buildingCell), slog.Int("cell_count", s.cellCount),
		slog.String("name_kind", s.nameKind), slog.String("equipment_kind", s.equipmentKind), slog.String("capacity_kind", s.capacityKind),
	}
}

type gridDiagnostics struct {
	blankBuildings, rowspanBuildings, excluded, otherBuildings, unresolved, unsupportedNames int
}

func (d *gridDiagnostics) observe(r room) {
	if r.building == "" {
		d.blankBuildings++
	}
	if r.buildingCell == "rowspan" {
		d.rowspanBuildings++
	}
}

func (d gridDiagnostics) log(ctx context.Context, logger *slog.Logger, date string, start int, building string, page, rows int) {
	if d.blankBuildings+d.rowspanBuildings+d.unresolved == 0 {
		return
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "结果网格楼栋诊断",
		slog.String("date", date), slog.Int("period_start", start), slog.String("building", building), slog.Int("page", page),
		slog.Int("raw_rows", rows), slog.Int("blank_building_rows", d.blankBuildings), slog.Int("rowspan_building_rows", d.rowspanBuildings),
		slog.Int("excluded_rows", d.excluded), slog.Int("other_building_rows", d.otherBuildings),
		slog.Int("unresolved_rows", d.unresolved), slog.Int("unsupported_name_rows", d.unsupportedNames))
}
