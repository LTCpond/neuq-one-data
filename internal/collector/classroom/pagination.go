package classroom

import (
	"context"
	"errors"
	"log/slog"

	"github.com/LTCpond/neuq-one-data/internal/model"
)

type pagination struct {
	pages, size, total, rows int
}

func (c *Collector) fetch(ctx context.Context, date string, start int, building string) (_ map[string]model.RoomDetail, failure error) {
	var g grid
	progress := pagination{total: -1}
	page := 1
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

	rooms := roomResults{details: map[string]model.RoomDetail{}, rawPages: map[[2]string]int{}, roomPages: map[string]int{}}
	for {
		var err error
		g, err = c.page(ctx, date, start, page, building)
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

type roomResults struct {
	details   map[string]model.RoomDetail
	rawPages  map[[2]string]int
	roomPages map[string]int
}

func (c *Collector) addPage(rooms *roomResults, g grid, page int, building string) (*integrityError, error) {
	var duplicate *integrityError
	for i, r := range g.rows {
		if noticeRoom(r.name) {
			continue
		}
		actual, err := c.resolveBuilding(r, building, g.buildingConfirmed, page, i+1)
		if err != nil {
			return nil, err
		}
		keyBuilding := actual
		if keyBuilding == "" {
			keyBuilding = c.canonicalBuilding(r.building)
		}
		key := [2]string{r.name, keyBuilding}
		if previous := rooms.rawPages[key]; previous != 0 && previous != page && duplicate == nil {
			duplicate = integrity(codeDuplicate, "不同页出现相同记录", slog.Int("row", i+1), slog.Int("previous_page", previous))
		}
		rooms.rawPages[key] = page
		if actual == "" {
			continue
		}
		name, detail, publishable := collectRoom(r, building)
		if !publishable {
			continue
		}
		if previous := rooms.roomPages[name]; previous != 0 && previous != page {
			if duplicate == nil {
				duplicate = integrity(codeDuplicate, "不同页出现归一化后相同的教室", slog.Int("row", i+1), slog.Int("previous_page", previous))
			}
			continue
		}
		if old, exists := rooms.details[name]; exists {
			merged, conflict := mergeRoom(old, detail)
			if conflict != nil {
				return nil, conflict.with(slog.Int("row", i+1))
			}
			detail = merged
		}
		rooms.roomPages[name], rooms.details[name] = page, detail
	}
	return duplicate, nil
}
