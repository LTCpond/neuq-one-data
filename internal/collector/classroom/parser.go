package classroom

import (
	"errors"
	"html"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var errResult = errors.New("无法确认空教室结果的完整性")

var params = regexp.MustCompile(`\bvar\s+_paramstring\s*=\s*["']([^"']*)["']`)
var anyGridPaging = regexp.MustCompile(`\bpage_[\w-]+\.pageInfo\(`)
var totals = regexp.MustCompile(`共\s*([0-9]+)\s*(?:条|项)`)
var pageTotals = regexp.MustCompile(`共\s*([0-9]+)\s*页`)
var emptyResult = regexp.MustCompile(`^(?:暂无(?:空闲|空)教室|没有(?:空闲|空)教室|无符合条件的(?:教室|记录)|(?:没有|未)查询到(?:符合条件的)?记录|没有查询结果|暂无数据|没有记录|无记录)[。！!]?$`)

type grid struct {
	rows               []room
	total, size, pages int
	explicitEmpty      bool
	buildingConfirmed  bool
}

func parseGrid(doc *goquery.Document, requested url.Values, page int) (grid, error) {
	var g grid
	scripts := doc.Find("script").Map(func(_ int, script *goquery.Selection) string { return script.Text() })
	content := doc.Find("body").Clone()
	content.Find("script, style, template").Remove()
	body := content.Text()

	table := doc.Find("table.gridtable")
	if table.Length() == 0 && page == 1 && emptyResult.MatchString(compact(body)) {
		if _, err := checkEcho(scripts, nil, requested, page, false); err != nil {
			return g, err
		}
		return grid{total: 0, pages: 1, explicitEmpty: true}, nil
	}
	if table.Length() != 1 {
		return g, errResult
	}
	tbody := table.ChildrenFiltered("tbody")
	if tbody.Length() != 1 {
		return g, errResult
	}

	positions, hasHeaders, err := readRoomColumns(table)
	if err != nil {
		return g, err
	}
	rows := tbody.ChildrenFiltered("tr")
	var layout gridLayout
	for i, row := range rows.EachIter() {
		cells := row.ChildrenFiltered("td, th")
		columns, err := layout.expand(cells, rows.Length()-i)
		if err != nil {
			return g, err
		}
		if row.ChildrenFiltered("td").Length() == 0 {
			if cells.Length() == 0 {
				return g, errResult
			}
			continue
		}
		if cells.Length() == 1 && emptyResult.MatchString(compact(cells.Text())) {
			g.explicitEmpty = true
			continue
		}
		name, building := columns.cell(positions.name), columns.cell(positions.building)
		r := room{name: name.value(), building: building.value(), equipment: columns.cell(positions.equipment).value(),
			capacity: columns.cell(positions.capacity).value(), nameCell: name.kind(), buildingCell: building.kind(),
			cellCount: cells.Length()}
		if !hasHeaders {
			if _, err := strconv.Atoi(r.capacity); err != nil {
				legacy := columns.cell(3).value()
				if _, err := strconv.Atoi(legacy); err == nil {
					r.capacity = legacy
				}
			}
		}
		if r.name == "" && cells.Length() <= 2 {
			cells.Each(func(_ int, cell *goquery.Selection) {
				if text := compact(cell.Text()); noticeRoom(text) {
					r.name = text
				}
			})
		}
		g.rows = append(g.rows, r)
	}
	if g.explicitEmpty && len(g.rows) > 0 {
		return g, errResult
	}

	g.total, g.pages = -1, -1
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
	var paging *regexp.Regexp
	pageConfirmed := false
	if id != "" {
		paging = regexp.MustCompile(`\bpage_` + regexp.QuoteMeta(id) + `\.pageInfo\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)`)
		if matches := paging.FindAllStringSubmatch(strings.Join(scripts, "\n"), -1); len(matches) > 0 {
			if len(matches) != 1 {
				return g, errResult
			}
			reportedPage, e1 := strconv.Atoi(matches[0][1])
			size, e2 := strconv.Atoi(matches[0][2])
			total, e3 := strconv.Atoi(matches[0][3])
			if e1 != nil || e2 != nil || e3 != nil || size < 1 || size > 10_000 ||
				total > 100_000 || (g.total >= 0 && g.total != total) {
				return g, errResult
			}
			if reportedPage != page {
				return g, &scopeMismatch{source: "grid.pageInfo", field: "pageNo",
					expected: strconv.Itoa(page), actual: echoDiagnostic("pageNo", matches[0][1:2]), page: page}
			}
			g.total, g.size = total, size
			pageConfirmed = true
		}
	}
	g.buildingConfirmed, err = checkEcho(scripts, paging, requested, page, pageConfirmed)
	if err != nil {
		return g, err
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

var echoFields = []string{"cycleTime.dateBegin", "cycleTime.dateEnd", "timeBegin", "timeEnd", "classroom.building.id", "pageNo"}

func checkEcho(scripts []string, paging *regexp.Regexp, requested url.Values, page int, pageConfirmed bool) (bool, error) {
	var gridEchoes, gridPartial, loose []url.Values
	for _, script := range scripts {
		var resultPage []int
		if paging != nil {
			resultPage = paging.FindStringIndex(script)
		}
		if resultPage != nil {
			// 已验证样本在 pageInfo 之前声明回显。共享脚本中，前一个网格
			// 的分页调用划定了本组参数的起点。
			script = script[:resultPage[0]]
			if earlier := anyGridPaging.FindAllStringIndex(script, -1); len(earlier) > 0 {
				script = script[earlier[len(earlier)-1][1]:]
			}
		} else if anyGridPaging.MatchString(script) {
			continue
		}
		for _, match := range params.FindAllStringSubmatch(script, -1) {
			values := echoFieldsFrom(match[1])
			switch {
			case resultPage != nil && (values.Has("timeBegin") || values.Has("timeEnd")):
				gridEchoes = append(gridEchoes, values)
			case resultPage != nil && len(values) != 0:
				gridPartial = append(gridPartial, values)
			case values.Has("timeBegin"):
				loose = append(loose, values)
			}
		}
	}

	candidates := gridEchoes
	if len(candidates) == 0 {
		candidates = append(gridPartial, loose...)
	}
	buildingConfirmed := false
	for _, returned := range candidates {
		for _, key := range echoFields {
			observed, present := returned[key]
			// 教务的分页表单可以保留初始页码，实际页码以结果网格为准。
			if !present || (key == "pageNo" && pageConfirmed) {
				continue
			}
			expected := requested.Get(key)
			if key == "pageNo" {
				expected = strconv.Itoa(page)
			}
			if len(observed) == 1 && sameEcho(key, expected, observed[0]) {
				buildingConfirmed = buildingConfirmed || (key == "classroom.building.id" && expected != "")
				continue
			}
			return false, &scopeMismatch{source: "paramstring", field: key, expected: echoDiagnostic(key, []string{expected}),
				actual: echoDiagnostic(key, observed), page: page}
		}
	}
	return buildingConfirmed, nil
}

// 单独解析与范围校验有关的字段。其他字段可能包含账号或任意表单文本。
func echoFieldsFrom(raw string) url.Values {
	values := url.Values{}
	for pair := range strings.SplitSeq(html.UnescapeString(raw), "&") {
		key, value, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(key)
		if err != nil || !slices.Contains(echoFields, key) {
			continue
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			decoded = "\x00"
		}
		values.Add(key, decoded)
	}
	return values
}

func sameEcho(key, expected, actual string) bool {
	actual = strings.TrimSpace(actual)
	switch key {
	case "pageNo":
		if actual == "" {
			return expected == "1"
		}
		fallthrough
	case "timeBegin", "timeEnd":
		if !diagnosticID.MatchString(actual) {
			return false
		}
		n, err := strconv.Atoi(actual)
		return err == nil && strconv.Itoa(n) == expected
	default:
		return expected == actual
	}
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
