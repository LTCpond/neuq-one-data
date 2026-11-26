package classroom

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/client"
	"github.com/LTCpond/neuq-one-data/internal/model"
)

const date = "2026-11-25"
const daySetup = `<form><select name="classroom.building.id">
<option value="1">工学馆</option><option value="7">科技楼</option><option value="9">人文楼</option>
</select></form>`

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newCollector(t *testing.T, vpn bool, timeout time.Duration, handler http.HandlerFunc) *Collector {
	t.Helper()
	return newCollectorWithBudget(t, vpn, timeout, 60, handler)
}

func newCollectorWithBudget(t *testing.T, vpn bool, timeout time.Duration, budget int, handler http.HandlerFunc) *Collector {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	httpClient := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		req := r.Clone(r.Context())
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(req)
	})}
	routes := auth.Routes{IdentityHost: "ids.neuq.edu.cn"}
	routes.EAMS, _ = url.Parse("https://jwxt.neuq.edu.cn/eams/")
	if vpn {
		routes.VPN, _ = url.Parse("https://vpn.neuq.edu.cn")
		routes.EAMS, _ = url.Parse("https://vpn.neuq.edu.cn/http/0123456789abcdef0123456789abcdef/eams/")
	}
	return &Collector{
		Session:   &auth.Session{HTTP: client.New(httpClient, routes.Allow, timeout, 0, budget, 0), Routes: routes, Username: "mock", Password: "password"},
		Buildings: []string{"工学馆", "科技楼", "人文楼"}, MaxPages: 4, TodayTTL: 2 * time.Hour, FutureTTL: 26 * time.Hour,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func ordinaryEAMS(search http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eams/homeExt.action":
			fmt.Fprint(w, "<p>教务主页</p>")
		case "/eams/" + auth.FormPath:
			fmt.Fprint(w, daySetup)
		case "/eams/" + auth.SearchPath:
			search(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}

func TestQueryResult(t *testing.T) {
	html := fixture(t, "free-rooms.html")
	c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" ||
			r.FormValue("cycleTime.dateBegin") != date || r.FormValue("cycleTime.dateEnd") != date ||
			r.FormValue("timeBegin") != "3" || r.FormValue("timeEnd") != "4" ||
			r.FormValue("roomApplyTimeType") != "0" || r.FormValue("classroom.building.id") != "7" ||
			r.Form.Has("pageSize") || r.Form.Has("pageNo") {
			t.Error("错误的教务查询参数")
		}
		fmt.Fprint(w, html)
	}))
	c.ids = map[string]string{"科技楼": "7"}
	got, err := c.fetch(t.Context(), date, 3, "科技楼")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["201"].Capacity == nil || *got["201"].Capacity != 80 ||
		got["6026-A自习室A"].Equipment == nil || *got["6026-A自习室A"].Equipment != "录播教室" ||
		got["302"].Capacity == nil || got["302"].Equipment != nil {
		t.Fatalf("错误的规范化结果 %#v", got)
	}
}

func TestPaginationAndEmpty(t *testing.T) {
	fragment := fixture(t, "free-rooms-fragment.html")
	const row = `<tr><td>1</td><td>科技楼201</td><td>科技楼</td><td>校本部</td><td>普通教室</td><td>80</td></tr>`
	grid := func(row string) string {
		return `<table id="grid123" class="gridtable"><tbody>` + row + `</tbody></table>`
	}
	for _, tc := range []struct {
		name, first, second string
		failed              bool
	}{
		{"pagination and AJAX fragment", grid(row) + `<script>page_grid123.pageInfo(1,1,2);</script>`, fragment, false},
		{"missing page", grid(row) + `<script>page_grid123.pageInfo(1,1,2);</script>`, "<p>没有查询结果</p>", true},
		{"duplicate page", grid(row) + `<script>page_grid123.pageInfo(1,1,2);</script>`, grid(row) + `<script>page_grid123.pageInfo(2,1,2);</script>`, true},
		{"wrong page number", grid(row) + `<script>page_grid123.pageInfo(1,1,2);</script>`, grid(row) + `<script>page_grid123.pageInfo(1,1,2);</script>`, true},
		{"wrong date", grid(row) + `共1条<script>var _paramstring='cycleTime.dateBegin=2026-11-24&timeBegin=1';</script>`, "", true},
		{"wrong period", grid(row) + `共1条<script>var _paramstring='timeBegin=3&timeEnd=4';</script>`, "", true},
		{"unknown HTML", "<p>系统状态异常</p>", "", true},
		{"missing completeness evidence", grid(row), "", true},
		{"empty result", "<p>没有查询结果</p>", "", false},
		{"empty grid", grid("") + `<script>page_grid123.pageInfo(1,1000,0);</script>`, "", false},
		{"unproven empty result", grid(""), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				if r.FormValue("pageNo") == "2" {
					fmt.Fprint(w, tc.second)
				} else {
					fmt.Fprint(w, tc.first)
				}
			}))
			c.ids = map[string]string{"科技楼": "7"}
			got, err := c.fetch(t.Context(), date, 1, "科技楼")
			if (err != nil) != tc.failed {
				t.Fatalf("结果 %#v，错误 %v", got, err)
			}
			if tc.name == "pagination and AJAX fragment" {
				capacity := got["203"].Capacity
				if len(got) != 2 || got["201"].Capacity == nil || capacity == nil || *capacity != 45 {
					t.Fatalf("分页数据错误 %#v", got)
				}
			}
			if strings.HasPrefix(tc.name, "empty") && (got == nil || len(got) != 0) {
				t.Fatalf("空结果应返回空集合 %#v", got)
			}
		})
	}
}

func TestCollectBuildingsInOrderWithNonemptyIDs(t *testing.T) {
	var mutex sync.Mutex
	var queries []string
	c := newCollector(t, false, time.Second, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		start, building, page := r.FormValue("timeBegin"), r.FormValue("classroom.building.id"), r.FormValue("pageNo")
		if building == "" {
			t.Error("采集发送了楼栋编号为空的请求")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mutex.Lock()
		queries = append(queries, start+":"+building+":"+page)
		mutex.Unlock()
		switch {
		case start == "1" && building == "1" && page == "":
			writeGrid(w, `<tr><td>1</td><td>工学馆201</td><td>工学馆</td></tr>`, 1, 1, 2)
		case start == "1" && building == "1" && page == "2":
			writeGrid(w, `<tr><td>2</td><td>工学馆202</td><td>工学馆</td></tr>`, 2, 1, 2)
		case start == "3" && building == "7":
			fmt.Fprint(w, `<table class="gridtable"><tbody><tr><td>1</td><td>工学馆201</td><td>工学馆</td></tr></tbody></table>共1条`)
		default:
			fmt.Fprint(w, "<p>没有查询结果</p>")
		}
	}))
	result := c.Run(t.Context(), time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC), 7, 1)
	mutex.Lock()
	defer mutex.Unlock()
	expected := []string{"1:1:", "1:1:2", "1:7:", "1:9:"}
	for start := 3; start <= 11; start += 2 {
		for _, id := range []string{"1", "7", "9"} {
			expected = append(expected, strconv.Itoa(start)+":"+id+":")
		}
	}
	if !slices.Equal(queries, expected) || result.SuccessfulSlots != 17 || result.Days[0].Date != date ||
		!slices.Equal(result.Days[0].Buildings["工学馆"]["1-2"].Rooms, []string{"201", "202"}) ||
		result.Days[0].Buildings["科技楼"]["3-4"].Status != model.Unavailable ||
		result.Days[0].Buildings["工学馆"]["3-4"].Status != model.Success ||
		result.Days[1].Buildings["人文楼"]["1-2"].Status != model.Unavailable {
		t.Fatalf("逐栋采集结果不匹配，查询 %v，成功 %d，错误 %v", queries, result.SuccessfulSlots, result.Failure)
	}
}

func TestCollectSkipsBuildingsWithoutConfirmedID(t *testing.T) {
	var searches atomic.Int32
	defaultHandler := ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
		searches.Add(1)
		id := r.FormValue("classroom.building.id")
		if id != "1" && id != "9" {
			t.Errorf("发送了未确认的楼栋编号 %q", id)
		}
		fmt.Fprint(w, "<p>没有查询结果</p>")
	})
	c := newCollector(t, false, time.Second, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/eams/"+auth.FormPath {
			fmt.Fprint(w, `<select name="classroom.building.id">
				<option value="1">工学馆</option><option value="">科技楼</option><option value="9">人文楼</option>
			</select>`)
			return
		}
		defaultHandler(w, r)
	})
	result := c.Run(t.Context(), time.Now(), 1, 1)
	if searches.Load() != 12 || result.SuccessfulSlots != 12 || !errors.Is(result.Failure, errScope) {
		t.Fatalf("缺失楼栋编号的处理错误：%d 个请求，%d 个成功时段，%v", searches.Load(), result.SuccessfulSlots, result.Failure)
	}
	for _, slot := range result.Days[0].Buildings["科技楼"] {
		if slot.Status != model.Unavailable {
			t.Fatal("未查询的楼栋时段应保持不可用")
		}
	}
}

func TestSessionExpiryRenewsOnce(t *testing.T) {
	const loginForm = `<form id="casLoginForm" method="post" action="/authserver/login">
<input name="lt" value="mock-ticket"><input name="execution" value="mock-execution">
<input type="password" name="password"><input id="pwdDefaultEncryptSalt" value="0123456789abcdef"></form>`
	var posts, searches atomic.Int32
	c := newCollector(t, false, time.Second, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/eams/homeExt.action":
			if _, err := r.Cookie("eams"); err != nil {
				http.Redirect(w, r, "http://ids.neuq.edu.cn/authserver/login?service=https%3A%2F%2Fjwxt.neuq.edu.cn%2Feams%2FhomeExt.action", http.StatusFound)
				return
			}
			fmt.Fprint(w, "<p>教务主页</p>")
		case "/authserver/login":
			if r.Method == "GET" {
				fmt.Fprint(w, loginForm)
				return
			}
			posts.Add(1)
			if r.FormValue("username") != "mock" || !decrypts(r.FormValue("password")) || r.FormValue("lt") != "mock-ticket" {
				t.Error("CAS 表单不符合模拟协议")
			}
			// CAS 实际跨域跳转由两个服务分别设置 cookie。此处模拟同域共享的测试 cookie。
			http.SetCookie(w, &http.Cookie{Name: "eams", Domain: "neuq.edu.cn", Value: "ok", Path: "/", Secure: true})
			http.Redirect(w, r, "https://jwxt.neuq.edu.cn/eams/homeExt.action", http.StatusSeeOther)
		case "/eams/" + auth.FormPath:
			fmt.Fprint(w, daySetup)
		case "/eams/" + auth.SearchPath:
			if searches.Add(1) == 2 {
				http.SetCookie(w, &http.Cookie{Name: "eams", Domain: "neuq.edu.cn", Path: "/", MaxAge: -1, Secure: true})
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, "<p>没有查询结果</p>")
		default:
			http.NotFound(w, r)
		}
	})
	result := c.Run(t.Context(), time.Now(), 1, 1)
	if result.Failure != nil || result.SuccessfulSlots != 18 || posts.Load() != 2 || searches.Load() != 19 {
		t.Fatalf("续期结果 %v，登录 %d 次，查询 %d 次", result.Failure, posts.Load(), searches.Load())
	}
}

func decrypts(encoded string) bool {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) != 80 {
		return false
	}
	block, _ := aes.NewCipher([]byte("0123456789abcdef"))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(data, data)
	return string(data[64:72]) == "password"
}

func TestStopsForSchoolRestrictionsAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		want       error
		calls      int32
	}{
		{"denial", "", 403, client.ErrDenied, 1},
		{"access restriction", "", 483, client.ErrDenied, 1},
		{"rate limit", "", 429, client.ErrDenied, 1},
		{"challenge", `<div>请拖动下方滑块完成验证</div>`, 200, client.ErrChallenge, 1},
		{"timeout", "", 0, client.ErrNetwork, 18},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			timeout := time.Second
			if tc.code == 0 {
				timeout = 80 * time.Millisecond
			}
			c := newCollector(t, false, timeout, ordinaryEAMS(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if tc.code == 0 {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
					return
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			c.SlotRetries = 2
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			result := c.Run(ctx, time.Now(), 1, 1)
			if !errors.Is(result.Failure, tc.want) || calls.Load() != tc.calls || result.SuccessfulSlots != 0 || result.RetryAttempts != 0 {
				t.Fatalf("得到错误 %v，共 %d 次查询", result.Failure, calls.Load())
			}
		})
	}
}
