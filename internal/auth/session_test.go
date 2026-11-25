package auth

import (
	"errors"
	"net/url"
	"testing"

	"github.com/LTCpond/neuq-one-data/internal/client"
)

func TestAllowSchoolRedirects(t *testing.T) {
	base, _ := url.Parse("https://jwxt.neuq.edu.cn/eams/")
	r := Routes{EAMS: base, IdentityHost: "ids.neuq.edu.cn"}
	for _, tc := range []struct {
		url     string
		allowed bool
	}{
		{"https://jwxt.neuq.edu.cn/eams/classroom/apply/free!search.action", true},
		{"https://ids.neuq.edu.cn/authserver/login;jsessionid=opaque?service=http%3A%2F%2Fjwxt.neuq.edu.cn%2Feams%2FhomeExt.action", true},
		{"https://ids.neuq.edu.cn/authserver/login?service=https%3A%2F%2Fother.example%2Feams%2FhomeExt.action", false},
		{"https://ids.neuq.edu.cn/authserver/login?service=https%3A%2F%2Fjwxt.neuq.edu.cn%2Feams%2FhomeExt.action&service=x", false},
		{"https://jwxt.neuq.edu.cn/eams/teach/grade/course/person!historyCourseGrade.action", false},
		{"http://ids.neuq.edu.cn/authserver/login", false},
		{"https://person@jwxt.neuq.edu.cn/eams/homeExt.action", false},
		{"https://jwxt.neuq.edu.cn:444/eams/homeExt.action", false},
	} {
		u, err := url.Parse(tc.url)
		if err != nil {
			t.Fatal(err)
		}
		if r.Allow(u) != tc.allowed {
			t.Errorf("错误的跳转判断 %s", tc.url)
		}
	}
}

func TestSearchDocumentClassifiesLoginAndChallenge(t *testing.T) {
	base, _ := url.Parse("https://jwxt.neuq.edu.cn/eams/")
	s := Session{Routes: Routes{EAMS: base, IdentityHost: "ids.neuq.edu.cn"}}
	for _, tc := range []struct {
		html string
		want error
	}{
		{`<form><input type="password" name="password"></form>`, client.ErrExpired},
		{`<form><input name="needCaptcha" value="true" type="hidden"></form>`, client.ErrChallenge},
		{`<div style="display: none"><input name="captcha"></div><p>没有查询结果</p>`, nil},
		{`<p>请不要过快点击</p>`, client.ErrDenied},
	} {
		_, err := s.SearchDocument(client.Page{URL: s.Routes.Academic(SearchPath), HTML: tc.html})
		if !errors.Is(err, tc.want) {
			t.Errorf("得到 %v，期望 %v", err, tc.want)
		}
	}
}
