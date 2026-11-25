package classroom

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
)

func TestResultEchoSelection(t *testing.T) {
	const table = `<table id="grid123" class="gridtable"><tbody>
 <tr><td>1</td><td>工学馆201</td><td>工学馆</td></tr></tbody></table>`
	const correct = "cycleTime.dateBegin=2026-11-25&cycleTime.dateEnd=2026-11-25&timeBegin=1&timeEnd=2"
	script := func(fields, paging string) string {
		return `<script>var _paramstring = '` + fields + "'; " + paging + "</script>"
	}
	for _, tc := range []struct {
		name, scripts                   string
		page                            int
		buildingID                      string
		source, field, expected, actual string
	}{
		{name: "ignore defaults and unrelated grid",
			scripts: script("cycleTime.dateBegin=2026-11-24&classroom.building.id=7", "") +
				script("timeBegin=9&timeEnd=10", "page_other.pageInfo(1,100,1);") +
				script(strings.ReplaceAll(correct, "&", "&amp;")+"&amp;classroom.building.id=&amp;pageNo=&amp;classroom.name=%ZZ",
					"page_grid123.pageInfo(1,100,1);"), page: 1},
		{name: "separate two grid echoes in a shared script",
			scripts: `<script>var _paramstring = 'timeBegin=9&timeEnd=10';
    page_other.pageInfo(1,100,1); var _paramstring = '` + correct + `';
    page_grid123.pageInfo(1,100,1); var _paramstring = 'timeBegin=7&timeEnd=8';
    page_another.pageInfo(1,100,1);</script>`, page: 1},
		{name: "ignore timed script outside result grid when tied echo exists",
			scripts: script("timeBegin=9&timeEnd=10", "") +
				script(correct+"&pageSize=100", "page_grid123.pageInfo(1,100,1);"), page: 1},
		{name: "timed fallback requires unique scope without grid association",
			scripts: script(correct, "") + script("timeBegin=9&timeEnd=10", ""),
			page:    1, source: "paramstring", field: "timeBegin", expected: "1", actual: "9"},
		{name: "actual grid mismatch despite matching decoy",
			scripts: script(correct, "") + script("timeBegin=9&timeEnd=10", "page_grid123.pageInfo(1,100,1);"),
			page:    1, source: "paramstring", field: "timeBegin", expected: "1", actual: "9"},
		{name: "page-only grid metadata must not hide a conflicting timed echo",
			scripts: script("timeBegin=9&timeEnd=10", "") + script("pageNo=1", "page_grid123.pageInfo(1,100,1);"),
			page:    1, source: "paramstring", field: "timeBegin", expected: "1", actual: "9"},
		{name: "validate date in grid even when times are absent",
			scripts: script("cycleTime.dateBegin=2026-11-24", "page_grid123.pageInfo(1,100,1);"),
			page:    1, source: "paramstring", field: "cycleTime.dateBegin", expected: date, actual: "2026-11-24"},
		{name: "validate empty end date",
			scripts: script("cycleTime.dateEnd=&timeBegin=1", ""), page: 1,
			source: "paramstring", field: "cycleTime.dateEnd", expected: date, actual: "<空>"},
		{name: "preserve verified ISO date format",
			scripts: script("cycleTime.dateBegin=2026/11/25&timeBegin=1", ""), page: 1,
			source: "paramstring", field: "cycleTime.dateBegin", expected: date, actual: "2026/11/25"},
		{name: "percent encoding and numeric period formatting",
			scripts: script("cycleTime.dateBegin=%202026%2D11%2D25%20&timeBegin=01&timeEnd=02&pageNo=", ""), page: 1},
		{name: "empty scoped ID is a mismatch",
			scripts: script(correct+"&classroom.building.id=", ""), page: 1, buildingID: "1",
			source: "paramstring", field: "classroom.building.id", expected: "1", actual: "<空>"},
		{name: "bulk ID must be blank if returned",
			scripts: script(correct+"&classroom.building.id=0", ""), page: 1,
			source: "paramstring", field: "classroom.building.id", expected: "<空>", actual: "0"},
		{name: "duplicate scope fields are rejected",
			scripts: script(correct+"&timeBegin=2", ""), page: 1,
			source: "paramstring", field: "timeBegin", expected: "1", actual: "<重复字段>"},
		{name: "grid page wins over original query page",
			scripts: script(correct+"&pageNo=1", "page_grid123.pageInfo(2,1,2);"), page: 2},
		{name: "wrong grid page is rejected despite matching query page",
			scripts: script(correct+"&pageNo=2", "page_grid123.pageInfo(1,1,2);"), page: 2,
			source: "grid.pageInfo", field: "pageNo", expected: "2", actual: "1"},
		{name: "empty query page is only valid on page one when grid omits metadata",
			scripts: script(correct+"&pageNo=", ""), page: 2,
			source: "paramstring", field: "pageNo", expected: "2", actual: "<空>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := auth.Document(table + tc.scripts)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseGrid(doc, query(date, 1, tc.page, tc.buildingID), tc.page)
			if tc.field == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var mismatch *scopeMismatch
			if !errors.As(err, &mismatch) || !errors.Is(err, errScope) || mismatch.source != tc.source ||
				mismatch.field != tc.field || mismatch.expected != tc.expected || mismatch.actual != tc.actual ||
				mismatch.page != tc.page {
				t.Fatalf("错误的范围校验结果：%+v，详细诊断 %#v", err, mismatch)
			}
		})
	}
}

func TestBuildingScope(t *testing.T) {
	c := Collector{Buildings: []string{"工学馆", "科技楼", "人文楼"}, campusBuildings: []string{"新工学馆"}}
	for _, tc := range []struct {
		name, label, requested, capacity, want string
		source, expected, actual               string
	}{
		{name: "201", label: "学校本部－工 学 馆", want: "工学馆"},
		{name: "【校本部】工学馆203", label: "工学馆（学校本部）", requested: "工学馆", want: "工学馆"},
		{name: "校本部：工学馆203", label: "校本部（工学馆）", want: "工学馆"},
		{name: "科技楼6026", want: "科技楼"},
		{name: "自主学习室A科技楼6026-A", want: "科技楼"},
		{name: "体育教学场地", want: ""},
		{name: "201", capacity: "0", want: ""},
		{name: "201", requested: "工学馆", want: "工学馆"},
		{name: "201", source: "grid.building", expected: "<可识别楼栋>", actual: "<空>"},
		{name: "科技楼203", label: "学校本部-科技楼", requested: "工学馆", source: "grid.building", expected: "工学馆", actual: "科技楼"},
		{name: "科技楼203", label: "学校本部-工学馆", requested: "工学馆", source: "grid.roomName", expected: "工学馆", actual: "科技楼"},
		{name: "科技楼机房", requested: "工学馆", source: "grid.roomName", expected: "工学馆", actual: "科技楼"},
		{name: "科技楼203", label: "学校本部-工学馆", source: "grid.building", expected: "科技楼", actual: "工学馆"},
		{name: "新工学馆203", label: "新工学馆", want: "新工学馆"},
		{name: "工学馆203", label: "其他校区-工学馆", source: "grid.building", expected: "工学馆", actual: "<已脱敏：未识别楼栋>"},
	} {
		t.Run(fmt.Sprintf("%s_%s_%s", tc.name, tc.label, tc.requested), func(t *testing.T) {
			r := room{name: tc.name, building: tc.label, capacity: tc.capacity}
			got, err := c.resolveBuilding(r, tc.requested, tc.requested != "", 2, 3)
			if tc.source == "" {
				if got != tc.want || err != nil {
					t.Fatalf("得到 %q，错误 %v", got, err)
				}
				return
			}
			var mismatch *scopeMismatch
			if !errors.As(err, &mismatch) || mismatch.source != tc.source || mismatch.field != "building" ||
				mismatch.expected != tc.expected || mismatch.actual != tc.actual || mismatch.page != 2 || mismatch.row != 3 {
				t.Fatalf("错误的楼栋诊断：%#v", mismatch)
			}
		})
	}
}

func TestBulkGridUsesNormalizedBuildingAndResultEcho(t *testing.T) {
	c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<table id="grid123" class="gridtable"><tbody>
   <tr><td>1</td><td>【学校本部】 工学馆 201</td><td>学校本部－工学馆</td></tr>
   <tr><td>2</td><td>体育教学场地</td><td></td></tr>
   </tbody></table>
   <script>var _paramstring = 'classroom.building.id=7&cycleTime.dateBegin=2026-11-24';</script>
   <script>var _paramstring = 'timeBegin=1&timeEnd=2&classroom.building.id=';
    page_grid123.pageInfo(1,100,2);</script>`)
	}))
	got, err := c.fetch(t.Context(), date, 1, "")
	if _, ok := got["工学馆"]["201"]; err != nil || !ok {
		t.Fatalf("全校查询结果 %#v，错误 %v", got, err)
	}
	doc, err := auth.Document(`<select name="classroom.building.id">
  <option value="1">学校本部／工学馆</option>
  <option value="7">【校本部】科技楼</option>
  <option value="9">人文楼（校本部）</option>
  </select>`)
	if err != nil {
		t.Fatal(err)
	}
	ids := buildingIDs(doc, c.Buildings)
	if ids["工学馆"] != "1" || ids["科技楼"] != "7" || ids["人文楼"] != "9" {
		t.Fatalf("带校区的楼栋选项 %#v", ids)
	}
}

func TestScopeLogsRedactUntrustedValues(t *testing.T) {
	const mockSecret = "mock-not-for-log-32abcd"
	c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		switch r.FormValue("classroom.building.id") {
		case "":
			fmt.Fprintf(w, `<p>没有查询结果</p><script>var _paramstring='timeBegin=%s&token=%s';</script>`, mockSecret, mockSecret)
		case "1":
			fmt.Fprintf(w, `<table class="gridtable"><tbody><tr><td>1</td><td>工学馆201</td><td>%s</td></tr></tbody></table>共1条`, mockSecret)
		default:
			fmt.Fprint(w, "<p>没有查询结果</p>")
		}
	}))
	var logs bytes.Buffer
	c.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	result := c.Run(t.Context(), time.Now(), 1, 1)
	if result.SuccessfulSlots != 12 {
		t.Fatalf("补查结果 %d", result.SuccessfulSlots)
	}
	output := logs.String()
	for _, snippet := range []string{
		`"source":"paramstring","field":"timeBegin","expected":"1","actual":"<已脱敏：格式不符>","page":1`,
		`"source":"grid.building","field":"building","expected":"工学馆","actual":"<已脱敏：未识别楼栋>","page":1,"row":1`,
	} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("日志缺少预期的脱敏字段 %s", snippet)
		}
	}
	if strings.Contains(output, mockSecret) || strings.Contains(output, "token=") || strings.Contains(output, "<script>") {
		t.Fatal("范围诊断包含未脱敏字段")
	}
}
