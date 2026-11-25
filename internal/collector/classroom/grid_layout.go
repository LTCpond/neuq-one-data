package classroom

import (
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

const maxGridColumns = 64

type gridCell struct {
	text    string
	width   int
	carried bool
}

func (c gridCell) value() string {
	if c.width != 1 {
		return ""
	}
	return c.text
}

func (c gridCell) kind() string {
	switch {
	case c.width == 0:
		return "missing"
	case c.width > 1:
		return "colspan"
	case c.carried && c.text == "":
		return "rowspan_blank"
	case c.carried:
		return "rowspan"
	case c.text == "":
		return "blank"
	default:
		return "present"
	}
}

type gridColumns [maxGridColumns]gridCell

func (columns *gridColumns) cell(index int) gridCell {
	if index < 0 || index >= len(columns) {
		return gridCell{}
	}
	return columns[index]
}

type gridLayout [maxGridColumns]struct {
	cell      gridCell
	remaining int
}

// 每组行独立展开。只有显式 rowspan 可以把楼栋列延伸到下一行。
func (layout *gridLayout) expand(cells *goquery.Selection, remainingRows int) (gridColumns, error) {
	var columns gridColumns
	for i := range layout {
		span := &layout[i]
		if span.remaining == 0 {
			continue
		}
		columns[i] = span.cell
		columns[i].carried = true
		span.remaining--
	}

	var invalid bool
	index := 0
	cells.EachWithBreak(func(_ int, element *goquery.Selection) bool {
		for index < len(columns) && columns[index].width != 0 {
			index++
		}
		width, okWidth := spanLength(element.AttrOr("colspan", "1"), len(columns)-index, false)
		height, okHeight := spanLength(element.AttrOr("rowspan", "1"), remainingRows, true)
		if !okWidth || !okHeight {
			invalid = true
			return false
		}
		cell := gridCell{text: compact(element.Text()), width: width}
		for i := index; i < index+width; i++ {
			if columns[i].width != 0 {
				invalid = true
				return false
			}
			columns[i] = cell
			layout[i].cell, layout[i].remaining = cell, height-1
		}
		index += width
		return true
	})
	if invalid {
		return columns, errResult
	}
	return columns, nil
}

func spanLength(raw string, limit int, allowZero bool) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit < 1 || n < 0 || (n == 0 && !allowZero) || (n > limit && !allowZero) {
		return 0, false
	}
	// HTML 将 rowspan=0 延伸到当前行组末尾；过长的 rowspan 同样在组末截断。
	if n == 0 {
		return limit, true
	}
	return min(n, limit), true
}

type roomColumns struct {
	name, building, equipment, capacity int
}

func readRoomColumns(table *goquery.Selection) (roomColumns, bool, error) {
	rows := table.ChildrenFiltered("thead").ChildrenFiltered("tr")
	if rows.Length() == 0 {
		return roomColumns{1, 2, 4, 5}, false, nil
	}
	positions := roomColumns{-1, -1, -1, -1}
	var layout gridLayout
	for i, row := range rows.EachIter() {
		columns, err := layout.expand(row.ChildrenFiltered("th, td"), rows.Length()-i)
		if err != nil {
			return positions, true, err
		}
		for j, cell := range columns {
			var position *int
			switch cell.value() {
			case "名称", "教室", "教室名称", "场地名称", "教室名", "教室编号":
				position = &positions.name
			case "教学楼", "楼栋", "楼宇", "所在楼栋":
				position = &positions.building
			case "教室设备配置":
				position = &positions.equipment
			case "容量":
				position = &positions.capacity
			}
			if position == nil {
				continue
			}
			if *position >= 0 && *position != j {
				return positions, true, errResult
			}
			*position = j
		}
	}
	if positions.name < 0 {
		return positions, true, errResult
	}
	return positions, true, nil
}
