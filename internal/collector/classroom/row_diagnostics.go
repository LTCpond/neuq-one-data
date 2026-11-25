package classroom

import (
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
