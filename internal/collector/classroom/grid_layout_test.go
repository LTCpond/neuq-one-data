package classroom

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

func TestGridLayoutPreservesRawRows(t *testing.T) {
	doc, err := auth.Document(`<table class="gridtable" id="grid123">
 <thead><tr><th rowspan="2">序号</th><th colspan="2">场地</th><th rowspan="2">校区</th><th colspan="2">配置</th></tr>
 <tr><th>名称</th><th>教学楼</th><th>教室设备配置</th><th>容量</th></tr></thead>
 <tbody>
 <tr><td>1</td><td>201</td><td rowspan="2">工学馆</td><td>学校本部</td><td>普通教室</td><td>60</td></tr>
 <tr><td>2</td><td>202</td><td>学校本部</td><td>多媒体小教室</td><td>45</td></tr>
 <tr><td>3</td><td>203</td><td>&nbsp;</td><td>学校本部</td><td>普通教室</td><td>75</td></tr>
 <tr><td colspan="6">具体安排以开课部门通知为准</td></tr>
 </tbody></table><script>page_grid123.pageInfo(1,100,4);</script>`)
	if err != nil {
		t.Fatal(err)
	}
	g, err := parseGrid(doc, query(date, 1, 1, preferredPageSize, "1"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.rows) != 4 || g.total != 4 {
		t.Fatalf("分页原始行数 %d，总数 %d", len(g.rows), g.total)
	}
	carried, blank := g.rows[1], g.rows[2]
	if carried.building != "工学馆" || carried.buildingCell != "rowspan" || carried.capacity != "45" ||
		carried.equipment != "多媒体小教室" || carried.cellCount != 5 {
		t.Fatal("合并单元格导致楼栋或其他列错位")
	}
	if blank.building != "" || blank.buildingCell != "blank" || !noticeRoom(g.rows[3].name) {
		t.Fatal("独立空单元格或跨列提示行识别错误")
	}
}

func TestScopedPaginationCountsFilteredRows(t *testing.T) {
	const firstRows = `<tr><td>1</td><td>工学馆201</td><td>工学馆</td><td>学校本部</td><td>普通教室</td><td>80</td></tr>
 <tr><td>2</td><td>活动场地</td><td></td><td>学校本部</td><td>活动教室</td><td>0</td></tr>
 <tr><td>3</td><td>运动场</td><td></td><td>学校本部</td><td>体育教学场地</td><td>30</td></tr>`
	const lastRows = `<tr><td>4</td><td>工学馆202</td><td></td><td>学校本部</td><td>普通教室</td><td>60</td></tr>
 <tr><td>5</td><td>工学馆203</td><td>工学馆</td><td>学校本部</td><td>普通教室</td><td>45</td></tr>`
	for _, tc := range []struct {
		name, secondRows string
		want             error
	}{
		{"all included in page totals", lastRows, nil},
		{"one filtered row missing", `<tr><td>4</td><td>工学馆202</td><td>工学馆</td></tr>`, errResult},
		{"duplicates among filtered rows", firstRows[strings.Index(firstRows, `<tr><td>2`):], errResult},
		{"ordinary room without building", `<tr><td>4</td><td>工学馆202</td><td>工学馆</td></tr><tr><td>5</td><td>208</td><td></td></tr>`, errScope},
		{"unrecognized text in building column", `<tr><td>4</td><td>工学馆202</td><td>工学馆</td></tr><tr><td>5</td><td>208</td><td>未知</td></tr>`, errScope},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.FormValue("pageNo") == "2" {
					writeGrid(w, tc.secondRows, 2, 3, 5)
					return
				}
				writeGrid(w, firstRows, 1, 3, 5)
			}))
			c.ids = map[string]string{"工学馆": "1"}
			got, err := c.fetch(t.Context(), date, 1, "工学馆")
			if !errors.Is(err, tc.want) || requests.Load() != 2 {
				t.Fatalf("原始分页校验失败：%v，%d 个请求", err, requests.Load())
			}
			if tc.want == nil && len(got) != 3 {
				t.Fatal("分页结果包含了本应过滤的场地")
			}
			if tc.want != nil && got != nil {
				t.Fatal("不完整的数据不应返回已确认的结果")
			}
		})
	}
}

func TestUnresolvedOrdinaryRoomRemainsUnavailable(t *testing.T) {
	const mockCell = "mock-private-cell-value"
	c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("timeBegin") != "1" {
			fmt.Fprint(w, "<p>没有查询结果</p>")
			return
		}
		switch r.FormValue("classroom.building.id") {
		case "7":
			writeGrid(w, `<tr><td>1</td><td>工程用房</td><td></td><td>学校本部</td><td>`+mockCell+`</td><td>40</td></tr>`, 1, 100, 1)
		default:
			fmt.Fprint(w, "<p>没有查询结果</p>")
		}
	}))
	var logs bytes.Buffer
	c.Log = slog.New(slog.NewJSONHandler(&logs, nil))
	result := c.Run(t.Context(), time.Now(), 1, 1)
	day := result.Days[0]
	if result.SuccessfulSlots != 17 || day.Buildings["科技楼"]["1-2"].Status != model.Unavailable ||
		day.Buildings["工学馆"]["1-2"].Status != model.Success {
		t.Fatal("归属不明的普通教室被误判为已确认结果")
	}
	output := logs.String()
	for _, field := range []string{`"building_cell":"blank"`, `"cell_count":6`, `"equipment_kind":"unrecognized"`, `"capacity_kind":"positive"`} {
		if !strings.Contains(output, field) {
			t.Fatalf("缺少诊断字段 %s", field)
		}
	}
	if strings.Contains(output, mockCell) || strings.Contains(output, "工程用房") || strings.Contains(output, "<tr>") {
		t.Fatal("日志暴露了原始单元格")
	}
}

func TestScopedRoomNumberRequiresConfirmedBuilding(t *testing.T) {
	for _, tc := range []struct {
		echo string
		want error
	}{
		{"", errScope},
		{"&classroom.building.id=1", nil},
		{"&classroom.building.id=7", errScope},
	} {
		t.Run(tc.echo, func(t *testing.T) {
			c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `<table id="grid123" class="gridtable"><tbody><tr><td>1</td><td>201</td>
      <td>&nbsp;</td><td>学校本部</td><td>普通教室</td><td>80</td></tr></tbody></table>
      <script>var _paramstring='timeBegin=1&timeEnd=2%s';page_grid123.pageInfo(1,100,1);</script>`, tc.echo)
			}))
			c.ids = map[string]string{"工学馆": "1"}
			got, err := c.fetch(t.Context(), date, 1, "工学馆")
			if !errors.Is(err, tc.want) || (tc.want != nil && got != nil) {
				t.Fatalf("无楼栋编号的处理错误：%v", err)
			}
			if tc.want == nil {
				if _, ok := got["201"]; !ok {
					t.Fatal("已获响应楼栋证据的房间缺失")
				}
			}
		})
	}
}

func writeGrid(w http.ResponseWriter, rows string, page, size, total int) {
	fmt.Fprintf(w, `<table id="grid123" class="gridtable"><tbody>%s</tbody></table><script>page_grid123.pageInfo(%d,%d,%d);</script>`, rows, page, size, total)
}
