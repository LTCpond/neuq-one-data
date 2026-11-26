package classroom

import (
	"context"
	"errors"
	"log/slog"
	"maps"

	"github.com/LTCpond/neuq-one-data/internal/model"
)

type pagination struct {
	pages, size, total, rows int
}

func (c *Collector) fetch(ctx context.Context, date string, start int, building string) (_ map[string]model.RoomDetail, failure error) {
	var g grid
	progress := pagination{total: -1}
	page := 1
	requestSize := preferredPageSize
	defer func() {
		if failure == nil {
			return
		}
		var diagnostic *integrityError
		if errors.As(failure, &diagnostic) {
			diagnostic.with(progress.diagnostics(g, page)...)
		}
		failure = &pageFailure{error: failure, page: page}
	}()

	rooms := roomResults{
		details: map[string]model.RoomDetail{}, rawPages: map[[2]string]roomOccurrence{},
		roomPages: map[string]int{}, pages: map[int]pageRecords{}, seenRecords: pageRecords{},
	}
	for {
		var err error
		g, err = c.page(ctx, date, start, page, requestSize, building)
		if err != nil {
			return nil, err
		}
		if g.pages > c.MaxPages {
			return nil, integrity(codePageLimit, "响应报告的页数超过采集上限", slog.Int("max_pages", c.MaxPages))
		}
		if page > 1 && g.size > 0 && g.total > g.size*c.MaxPages {
			return nil, integrity(codePageLimit, "按当前页元数据计算的页数超过采集上限",
				slog.Int("max_pages", c.MaxPages), slog.Int("calculated_pages", (g.total+g.size-1)/g.size))
		}
		if page == 1 {
			c.logPageSize(ctx, building, g)
			if g.size > 0 {
				requestSize = g.size
			}
			progress.total = g.total
			// 没有数据行时，无法用实际行数推算分页大小。
			if len(g.rows) == 0 && g.total > 0 && g.size == 0 {
				return nil, integrity(codePageRows, "首页报告有记录，但缺少数据行", slog.Int("minimum_records", 1))
			}
			progress.pages, progress.size, err = g.plan()
			if err != nil {
				return nil, err
			}
			if progress.pages > c.MaxPages {
				return nil, integrity(codePageLimit, "查询页数超过采集上限", slog.Int("max_pages", c.MaxPages))
			}
		}

		duplicate, err := c.addPage(&rooms, g, page, building)
		if err != nil {
			return nil, err
		}
		if err := progress.validate(g, page); err != nil {
			return nil, err
		}
		if duplicate != nil {
			return nil, duplicate
		}
		if page == progress.pages {
			if rooms.specialDuplicates+rooms.samePageRaw+rooms.samePageNormalized+rooms.samePageSpecial > 0 {
				c.Log.InfoContext(ctx, "已处理重复的教室记录", "date", date, "building", building, "time_begin", start, "pages", page,
					"special_duplicates", rooms.specialDuplicates, "same_page_raw_duplicates", rooms.samePageRaw,
					"same_page_normalized_duplicates", rooms.samePageNormalized, "same_page_special_duplicates", rooms.samePageSpecial)
			}
			return rooms.details, nil
		}
		progress.rows += len(g.rows)
		page++
	}
}

func (p *pagination) validate(g grid, page int) error {
	switch {
	case p.total >= 0 && g.total >= 0 && g.total != p.total:
		return integrity(codeRecordChanged, "不同页报告的记录总数不同")
	case g.pages > 0 && g.pages != p.pages:
		return integrity(codePageTotalChanged, "不同页报告的页数不同")
	case g.size > 0 && g.size != p.size:
		return integrity(codePageSizeChanged, "各页的分页大小不同")
	}
	if p.total < 0 && g.total >= 0 {
		p.total = g.total
	}
	rows := len(g.rows)
	switch {
	case page < p.pages && rows != p.size:
		return integrity(codePageRows, "非末页记录数与分页大小不符")
	case rows > p.size:
		return integrity(codePageRows, "末页记录数超过分页大小")
	case rows == 0 && (page > 1 || p.total != 0):
		return integrity(codePageRows, "应有数据的页缺少记录", slog.Int("minimum_records", 1))
	}
	count := p.rows + rows
	if p.total >= 0 && (count > p.total || (page == p.pages && count != p.total)) {
		return integrity(codeRecordCount, "累计记录数与记录总数不符")
	}
	return nil
}

func (p pagination) diagnostics(g grid, page int) []slog.Attr {
	attrs := []slog.Attr{slog.Int("actual_records", len(g.rows)), slog.Int("accumulated_records", p.rows+len(g.rows))}
	if p.pages > 0 {
		attrs = append(attrs, slog.Int("expected_pages", p.pages))
	}
	if p.size > 0 {
		attrs = append(attrs, slog.Int("page_size", p.size))
		if page < p.pages {
			attrs = append(attrs, slog.Int("expected_records", p.size))
		} else if p.total >= 0 {
			attrs = append(attrs, slog.Int("expected_records", max(0, p.total-p.rows)))
		}
	}
	if p.total >= 0 {
		attrs = append(attrs, slog.Int("expected_total_records", p.total))
	}
	if g.pages >= 0 {
		attrs = append(attrs, slog.Int("reported_pages", g.pages))
	}
	if g.size > 0 {
		attrs = append(attrs, slog.Int("reported_page_size", g.size))
	}
	if g.total >= 0 {
		attrs = append(attrs, slog.Int("reported_total_records", g.total))
	}
	return attrs
}

type roomOccurrence struct {
	page                   int
	special, pageConfirmed bool
}

// 比较整页的记录及出现次数，忽略页面内的显示顺序和序号。
type pageRecords map[[4]string]int

type roomResults struct {
	details                                          map[string]model.RoomDetail
	rawPages                                         map[[2]string]roomOccurrence
	roomPages                                        map[string]int
	pages                                            map[int]pageRecords
	seenRecords                                      pageRecords
	specialDuplicates                                int
	samePageRaw, samePageNormalized, samePageSpecial int
}

func (c *Collector) addPage(rooms *roomResults, g grid, page int, building string) (*integrityError, error) {
	records := make(pageRecords, len(g.rows))
	for _, r := range g.rows {
		records[[4]string{r.name, c.canonicalBuilding(r.building), r.equipment, r.capacity}]++
	}
	var duplicate, conflict *integrityError
	var rawDuplicates, normalizedDuplicates, specialDuplicates int
	for i, r := range g.rows {
		if noticeRoom(r.name) {
			continue
		}
		actual, err := c.resolveBuilding(r, building, g.buildingConfirmed, page, i+1)
		if err != nil {
			return nil, err
		}
		special := excludedRoom(r)
		key := [2]string{r.rawName, r.rawBuilding}
		if special || actual == "" {
			key = [2]string{r.name, c.canonicalBuilding(r.building)}
		}
		previous := rooms.rawPages[key]
		rawDuplicate := previous.page != 0 && previous.page != page
		switch {
		case rawDuplicate && special && previous.special:
			specialDuplicates++
			if duplicate == nil && (!g.pageConfirmed || !previous.pageConfirmed) {
				duplicate = duplicateRoom("special_room", "特殊教室记录重复且页码未经确认", previous.page, i+1, r)
			}
		case rawDuplicate:
			rawDuplicates++
			if duplicate == nil {
				duplicate = duplicateRoom("raw_room", "不同页出现相同教室记录", previous.page, i+1, r)
			}
		case previous.page == page && special && previous.special:
			rooms.samePageSpecial++
		case previous.page == page:
			rooms.samePageRaw++
		}
		if previous.page == 0 {
			rooms.rawPages[key] = roomOccurrence{page, special, g.pageConfirmed}
		}
		if actual == "" {
			continue
		}
		name, detail, publishable := collectRoom(r, building)
		if !publishable {
			continue
		}
		if previous := rooms.roomPages[name]; previous != 0 && previous != page {
			if !rawDuplicate {
				normalizedDuplicates++
			}
			if duplicate == nil {
				duplicate = duplicateRoom("normalized_room", "不同页出现归一化后相同的教室", previous, i+1, r)
			}
			continue
		}
		if old, exists := rooms.details[name]; exists {
			if previous.page != page {
				rooms.samePageNormalized++
			}
			merged, err := mergeRoom(old, detail)
			if err != nil && conflict == nil {
				conflict = err.with(slog.Int("row", i+1))
			}
			detail = merged
		}
		rooms.roomPages[name], rooms.details[name] = page, detail
	}
	hasNewRecord := false
	for record, count := range records {
		if rooms.seenRecords[record] < count {
			hasNewRecord = true
		}
		rooms.seenRecords[record] += count
	}
	if page > 1 && len(records) > 0 && !hasNewRecord {
		duplicate = integrity(codeDuplicate, "该页全部记录均已在前页出现", slog.String("duplicate_kind", "repeated_page"))
	}
	for previous := 1; previous < page; previous++ {
		if len(records) > 0 && maps.Equal(rooms.pages[previous], records) {
			duplicate = integrity(codeDuplicate, "不同页返回整页相同记录", slog.String("duplicate_kind", "repeated_page"),
				slog.Int("previous_page", previous))
			break
		}
	}
	if conflict != nil {
		return nil, conflict
	}
	if duplicate != nil {
		duplicate.with(slog.Int("raw_duplicates", rawDuplicates), slog.Int("normalized_duplicates", normalizedDuplicates),
			slog.Int("special_duplicates", specialDuplicates))
	}
	rooms.pages[page] = records
	rooms.specialDuplicates += specialDuplicates
	return duplicate, nil
}

func duplicateRoom(kind, reason string, previous, row int, r room) *integrityError {
	return integrity(codeDuplicate, reason, slog.String("duplicate_kind", kind),
		slog.Int("row", row), slog.Int("previous_page", previous)).with(diagnoseRoom(r).attrs()...)
}
