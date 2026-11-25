package classroom

import (
	"errors"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var errResult = errors.New("无法确认空教室结果的完整性")
var errScope = errors.New("响应的日期、时段或楼栋与请求不符")

var params = regexp.MustCompile(`\bvar\s+_paramstring\s*=\s*["']([^"']*)["']`)
var totals = regexp.MustCompile(`共\s*([0-9]+)\s*(?:条|项)`)
var pageTotals = regexp.MustCompile(`共\s*([0-9]+)\s*页`)
var emptyResult = regexp.MustCompile(`^(?:暂无(?:空闲|空)教室|没有(?:空闲|空)教室|无符合条件的(?:教室|记录)|(?:没有|未)查询到(?:符合条件的)?记录|没有查询结果|暂无数据|没有记录|无记录)[。！!]?$`)

type grid struct {
	rows               []room
	total, size, pages int
	explicitEmpty      bool
}

func parseGrid(doc *goquery.Document, requested url.Values, page int) (grid, error) {
	var g grid
	scripts := doc.Find("script").Text()
	if err := checkEcho(scripts, requested); err != nil {
		return g, err
	}
	content := doc.Find("body").Clone()
	content.Find("script, style, template").Remove()
	body := content.Text()

	table := doc.Find("table.gridtable")
	if table.Length() == 0 && page == 1 && emptyResult.MatchString(compact(body)) {
		return grid{total: 0, pages: 1, explicitEmpty: true}, nil
	}
	if table.Length() != 1 {
		return g, errResult
	}
	tbody := table.ChildrenFiltered("tbody")
	if tbody.Length() != 1 {
		return g, errResult
	}

	headers := table.ChildrenFiltered("thead").Find("th, td")
	name, building, equipment, capacity := 1, 2, 4, 5
	if headers.Length() != 0 {
		name, building, equipment, capacity = -1, -1, -1, -1
		headers.Each(func(i int, cell *goquery.Selection) {
			switch compact(cell.Text()) {
			case "名称", "教室", "教室名称", "场地名称", "教室名", "教室编号":
				name = i
			case "教学楼", "楼栋", "楼宇", "所在楼栋":
				building = i
			case "教室设备配置":
				equipment = i
			case "容量":
				capacity = i
			}
		})
		if name < 0 {
			return g, errResult
		}
	}
	var rowErr error
	tbody.ChildrenFiltered("tr").EachWithBreak(func(_ int, row *goquery.Selection) bool {
		cells := row.ChildrenFiltered("td")
		if cells.Length() == 0 && row.ChildrenFiltered("th").Length() > 0 {
			return true
		}
		if cells.Length() == 1 && emptyResult.MatchString(compact(cells.Text())) {
			g.explicitEmpty = true
			return true
		}
		field := func(i int) string {
			if i < 0 || i >= cells.Length() {
				return ""
			}
			return compact(cells.Eq(i).Text())
		}
		r := room{name: field(name), building: field(building), equipment: field(equipment), capacity: field(capacity)}
		if headers.Length() == 0 {
			if _, err := strconv.Atoi(r.capacity); err != nil {
				if _, err := strconv.Atoi(field(3)); err == nil {
					r.capacity = field(3)
				}
			}
		}
		if r.name == "" {
			rowErr = errResult
			return false
		}
		g.rows = append(g.rows, r)
		return true
	})
	if rowErr != nil || (g.explicitEmpty && len(g.rows) > 0) {
		return g, errResult
	}

	g.total, g.pages = -1, -1
	var err error
	g.total, err = matchCount(totals, body)
	if err != nil {
		return g, err
	}
	g.pages, err = matchCount(pageTotals, body)
	if err != nil {
		return g, err
	}

	id := table.AttrOr("id", "")
	if id == "" {
		id = strings.TrimSuffix(tbody.AttrOr("id", ""), "_data")
	}
	if id != "" {
		paging := regexp.MustCompile(`\bpage_` + regexp.QuoteMeta(id) + `\.pageInfo\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)`)
		if matches := paging.FindAllStringSubmatch(scripts, -1); len(matches) > 0 {
			if len(matches) != 1 {
				return g, errResult
			}
			reportedPage, e1 := strconv.Atoi(matches[0][1])
			size, e2 := strconv.Atoi(matches[0][2])
			total, e3 := strconv.Atoi(matches[0][3])
			if e1 != nil || e2 != nil || e3 != nil || reportedPage != page || size < 1 || size > 10_000 ||
				total > 100_000 || (g.total >= 0 && g.total != total) {
				return g, errResult
			}
			g.total, g.size = total, size
		}
	}

	switch {
	case g.total == 0 || g.explicitEmpty:
		if len(g.rows) != 0 || page != 1 || g.total > 0 || g.pages > 1 {
			return g, errResult
		}
		g.total, g.pages = 0, 1
	case len(g.rows) == 0:
		return g, errResult
	case g.total >= 0 && len(g.rows) > g.total:
		return g, errResult
	}
	return g, nil
}

func checkEcho(scripts string, requested url.Values) error {
	for _, match := range params.FindAllStringSubmatch(scripts, -1) {
		echo := html.UnescapeString(match[1])
		values, err := url.ParseQuery(echo)
		if err != nil {
			return errScope
		}
		for _, key := range []string{"cycleTime.dateBegin", "cycleTime.dateEnd", "timeBegin", "timeEnd", "classroom.building.id", "pageNo"} {
			observed, ok := values[key]
			if !ok {
				continue
			}
			expected := requested.Get(key)
			if key == "pageNo" && expected == "" {
				expected = "1"
			}
			if len(observed) != 1 || observed[0] != expected {
				return errScope
			}
		}
	}
	return nil
}

func matchCount(re *regexp.Regexp, text string) (int, error) {
	result := -1
	for _, match := range re.FindAllStringSubmatch(text, -1) {
		current, err := strconv.Atoi(match[1])
		if err != nil || current > 100_000 || (result >= 0 && result != current) {
			return -1, errResult
		}
		result = current
	}
	return result, nil
}

func (g grid) plan() (pages, size int, err error) {
	if g.total < 0 && g.pages < 1 {
		return 0, 0, errResult
	}
	size = g.size
	if size == 0 {
		size = max(1, len(g.rows))
	}
	pages = g.pages
	if g.total >= 0 {
		calculated := max(1, (g.total+size-1)/size)
		if pages > 0 && pages != calculated {
			return 0, 0, errResult
		}
		pages = calculated
	}
	return pages, size, nil
}
