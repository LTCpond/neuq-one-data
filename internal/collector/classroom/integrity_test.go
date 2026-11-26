package classroom

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
)

func scienceRow(name string) string {
	return `<tr><td>1</td><td>` + name + `</td><td>科技楼</td></tr>`
}

func TestPaginationFailureCodes(t *testing.T) {
	first := scienceRow("科技楼201") + scienceRow("科技楼202")
	second := scienceRow("科技楼203") + scienceRow("科技楼204")
	for _, tc := range []struct {
		name, rows, extra, code string
		size, total, firstTotal int
		want                    map[string]int64
	}{
		{name: "reported number of pages changed", rows: second, extra: "共3页", code: codePageTotalChanged, size: 2, total: 4, firstTotal: 4,
			want: map[string]int64{"expected_pages": 2, "reported_pages": 3}},
		{name: "reported record total changed", rows: second, code: codeRecordChanged, size: 2, total: 5, firstTotal: 4,
			want: map[string]int64{"expected_total_records": 4, "reported_total_records": 5}},
		{name: "reported page size changed", rows: second, code: codePageSizeChanged, size: 3, total: 4, firstTotal: 4,
			want: map[string]int64{"page_size": 2, "reported_page_size": 3}},
		{name: "repeated raw row", rows: scienceRow("科技楼201") + scienceRow("科技楼204"), code: codeDuplicate, size: 2, total: 4, firstTotal: 4,
			want: map[string]int64{"previous_page": 1, "row": 1}},
		{name: "repeated normalized room", rows: scienceRow("201") + scienceRow("科技楼204"), code: codeDuplicate, size: 2, total: 4, firstTotal: 4,
			want: map[string]int64{"previous_page": 1, "row": 1}},
		{name: "actual record count is short", rows: scienceRow("科技楼203"), code: codeRecordCount, size: 2, total: 4, firstTotal: 4,
			want: map[string]int64{"expected_records": 2, "actual_records": 1, "accumulated_records": 3}},
		{name: "non-final page is short", rows: scienceRow("科技楼203"), code: codePageRows, size: 2, total: 5, firstTotal: 5,
			want: map[string]int64{"expected_records": 2, "actual_records": 1, "accumulated_records": 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				if r.FormValue("pageNo") == "2" {
					if r.Form.Has("pageSize") || r.FormValue("classroom.building.id") != "7" || r.FormValue("timeBegin") != "1" || r.FormValue("cycleTime.dateBegin") != date {
						t.Error("第 2 页查询参数改变")
					}
					writeGrid(w, tc.rows, 2, tc.size, tc.total)
					fmt.Fprint(w, tc.extra)
					return
				}
				writeGrid(w, first, 1, 2, tc.firstTotal)
			}))
			c.ids = map[string]string{"科技楼": "7"}
			rooms, err := c.fetch(t.Context(), date, 1, "科技楼")
			var diagnostic *integrityError
			var page *pageFailure
			if rooms != nil || !errors.Is(err, errResult) || !errors.As(err, &diagnostic) || diagnostic.code != tc.code ||
				!errors.As(err, &page) || page.page != 2 || !retryableIntegrity(err) || c.Session.HTTP.Count() != 2 {
				t.Fatalf("结果 %#v，错误 %v，期望第 2 页 %s", rooms, err, tc.code)
			}
			metrics := map[string]slog.Value{}
			for _, attr := range diagnostic.attrs {
				metrics[attr.Key] = attr.Value
			}
			for key, want := range tc.want {
				if got := metrics[key]; got.Kind() != slog.KindInt64 || got.Int64() != want {
					t.Errorf("诊断字段 %s = %v，期望 %d", key, got, want)
				}
			}
		})
	}
}

func TestGridStructureAndMetadataDiagnostics(t *testing.T) {
	const table = `<table class="gridtable" id="grid1"><tbody><tr><td>1</td><td>科技楼201</td><td>科技楼</td></tr></tbody></table>`
	for _, tc := range []struct{ html, code string }{
		{`<p>untrusted mock response</p>`, codeTableStructure},
		{`<table class="gridtable"><thead><tr><th>容量</th></tr></thead><tbody></tbody></table>`, codeTableStructure},
		{`<table class="gridtable"><tbody><tr><td colspan="secret">internal data</td></tr></tbody></table>`, codeTableStructure},
		{table + `<script>page_grid1.pageInfo(1,0,1);</script>`, codePagingInvalid},
		{table + `<script>page_grid1.pageInfo(1,20,2);</script>共1条`, codePagingConflict},
	} {
		doc, err := auth.Document(tc.html)
		if err != nil {
			t.Fatal(err)
		}
		_, err = parseGrid(doc, query(date, 1, 1, "7"), 1)
		if !errors.Is(err, errResult) || failureCode(err) != tc.code || retryableIntegrity(err) {
			t.Errorf("得到 %s (%v)，期望无补偿的 %s", failureCode(err), err, tc.code)
		}
	}
}
