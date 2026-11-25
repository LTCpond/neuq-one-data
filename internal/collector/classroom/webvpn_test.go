package classroom

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/auth"
	"github.com/LTCpond/neuq-one-data/internal/client"
)

func TestWebVPNOfficialCAS(t *testing.T) {
	const origin = "https://vpn.neuq.edu.cn"
	const eams = "/http/0123456789abcdef0123456789abcdef/eams/"
	const cas = "https://ids.neuq.edu.cn/authserver/login"
	var signIns, searches atomic.Int32
	c := newCollector(t, true, time.Second, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			switch {
			case r.URL.Query().Get("cas_login") == "true":
				http.Redirect(w, r, cas+"?service=https%3A%2F%2Fvpn.neuq.edu.cn%2Fcas-login", http.StatusFound)
			default:
				fmt.Fprint(w, `<a href="/login?cas_login=true">统一身份认证</a>`)
			}
		case "/authserver/login":
			if r.Method == "GET" {
				fmt.Fprint(w, `<form id="casLoginForm" method="post" action="/authserver/login">
      <input name="lt" value="mock"><input name="execution" value="mock">
      <input id="pwdDefaultEncryptSalt" value="0123456789abcdef">
      </form>`)
				return
			}
			signIns.Add(1)
			http.Redirect(w, r, origin+"/cas-login", http.StatusSeeOther)
		case "/cas-login":
			http.SetCookie(w, &http.Cookie{Name: "vpn", Value: "mock", Path: "/", Secure: true})
			http.Redirect(w, r, origin+"/", http.StatusFound)
		case "/":
			fmt.Fprint(w, "<p>校内应用</p>")
		case eams + "homeExt.action":
			fmt.Fprint(w, "<p>教务主页</p>")
		case eams + auth.FormPath:
			fmt.Fprint(w, daySetup)
		case eams + auth.SearchPath:
			cookie, _ := r.Cookie("vpn")
			if cookie == nil || cookie.Value != "mock" {
				t.Error("教务请求未保留 VPN 会话")
			}
			searches.Add(1)
			fmt.Fprint(w, "<p>没有查询结果</p>")
		default:
			t.Errorf("收到未预期的请求 %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	result := c.Run(t.Context(), time.Now(), 1, 1)
	if result.Failure != nil || signIns.Load() != 1 || searches.Load() != 18 || result.SuccessfulSlots != 18 {
		t.Fatalf("模拟 WebVPN 采集失败：%v，登录 %d 次，查询 %d 次", result.Failure, signIns.Load(), searches.Load())
	}
}

func TestWebVPNStopsAtDeviceChallenge(t *testing.T) {
	var requests atomic.Int32
	c := newCollector(t, true, time.Second, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/login" || r.Method != "GET" || strings.Contains(r.URL.RawQuery, "cas_login") {
			t.Error("设备验证之后仍尝试访问认证端点")
		}
		fmt.Fprint(w, "<p>需要验证设备</p>")
	})
	result := c.Run(t.Context(), time.Now(), 1, 1)
	if !errors.Is(result.Failure, client.ErrChallenge) || requests.Load() != 1 {
		t.Fatalf("没有识别设备验证：%v，共 %d 个请求", result.Failure, requests.Load())
	}
}
