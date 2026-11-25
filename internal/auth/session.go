package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/LTCpond/neuq-one-data/internal/client"
	"github.com/LTCpond/neuq-one-data/internal/config"
	"github.com/PuerkitoBio/goquery"
)

var ErrLogin = errors.New("统一身份认证未完成或协议已变化")
var sessionSuffix = regexp.MustCompile(`(?i);jsessionid=[a-z0-9._-]+$`)

const FormPath = "classroom/apply/free.action"
const SearchPath = "classroom/apply/free!search.action"

type Routes struct {
	EAMS         *url.URL
	VPN          *url.URL
	IdentityHost string
}

func Official(c config.Config) Routes {
	r := Routes{EAMS: c.EAMS, IdentityHost: config.IdentityHost}
	if c.UseVPN {
		r.VPN, _ = url.Parse("https://" + config.VPNHost)
	}
	return r
}

func (r Routes) Academic(path string) *url.URL {
	return r.EAMS.ResolveReference(&url.URL{Path: path, RawPath: path})
}

func (r Routes) academicPage(u *url.URL, relative string) bool {
	return sameOrigin(u, r.EAMS) && sessionSuffix.ReplaceAllString(u.Path, "") == r.EAMS.Path+relative
}

func (r Routes) casPage(u *url.URL) bool {
	return config.HTTPS(u, r.IdentityHost) && sessionSuffix.ReplaceAllString(u.Path, "") == "/authserver/login"
}

func (r Routes) Allow(u *url.URL) bool {
	if u == nil || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.RawPath != "" && u.RawPath != u.Path) {
		return false
	}
	if r.casPage(u) {
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return false
		}
		values := query["service"]
		if len(values) == 0 {
			return true
		}
		if len(values) != 1 {
			return false
		}
		service, err := url.Parse(values[0])
		if err != nil || service.User != nil || service.Fragment != "" || strings.Contains(service.EscapedPath(), "%") {
			return false
		}
		if service.Scheme == "http" && (service.Port() == "" || service.Port() == "80") && r.VPN == nil {
			service.Scheme, service.Host = "https", service.Hostname()
		}
		return r.academicPage(service, "homeExt.action") || r.academicPage(service, "home.action") ||
			(sameOrigin(service, r.VPN) && (service.Path == "/login" || service.Path == "/cas-login"))
	}
	for _, path := range []string{"", "homeExt.action", "home.action", "localLogin.action", "login.action", FormPath, SearchPath} {
		if r.academicPage(u, path) {
			return true
		}
	}
	if sameOrigin(u, r.EAMS) && u.Path == strings.TrimSuffix(r.EAMS.Path, "/") {
		return true
	}
	return sameOrigin(u, r.VPN) && (u.Path == "/" || u.Path == "/login" || u.Path == "/cas-login")
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil || a.User != nil || a.Scheme != b.Scheme || a.Hostname() != b.Hostname() {
		return false
	}
	port := func(u *url.URL) string {
		if u.Port() == "" && u.Scheme == "https" {
			return "443"
		}
		return u.Port()
	}
	return port(a) == port(b)
}

type Session struct {
	HTTP               *client.Client
	Routes             Routes
	Username, Password string
}

func (s *Session) Prepare(ctx context.Context) (*goquery.Document, error) {
	submitted := false
	if s.Routes.VPN != nil {
		portal := s.Routes.VPN.ResolveReference(&url.URL{Path: "/login"})
		page, doc, err := s.get(ctx, portal)
		if err != nil {
			return nil, err
		}
		if !s.Routes.casPage(page.URL) && page.URL.Path == "/login" {
			// 使用参考项目所观察到的正常 CAS 入口，保留学校的挑战及设备检查。
			if doc.Find("form").Length() == 0 && doc.Find("a[href]").Length() == 0 {
				return nil, ErrLogin
			}
			page, doc, err = s.get(ctx, s.Routes.VPN.ResolveReference(&url.URL{Path: "/login", RawQuery: "cas_login=true"}))
			if err != nil {
				return nil, err
			}
		}
		if err := s.signIn(ctx, page, doc, &submitted); err != nil {
			return nil, err
		}
	}
	page, doc, err := s.get(ctx, s.Routes.Academic("homeExt.action"))
	if err != nil {
		return nil, err
	}
	if err := s.signIn(ctx, page, doc, &submitted); err != nil {
		return nil, err
	}
	page, doc, err = s.get(ctx, s.Routes.Academic(FormPath))
	if err != nil {
		return nil, err
	}
	if !s.Routes.academicPage(page.URL, FormPath) || isLogin(doc) || doc.Find(`select[name="classroom.building.id"], form input[name="cycleTime.dateBegin"]`).Length() == 0 {
		return nil, ErrLogin
	}
	return doc, nil
}

func (s *Session) get(ctx context.Context, target *url.URL) (client.Page, *goquery.Document, error) {
	page, err := s.HTTP.Do(ctx, target, nil, false, true)
	if err != nil {
		return page, nil, err
	}
	doc, err := Document(page.HTML)
	return page, doc, err
}

func (s *Session) signIn(ctx context.Context, page client.Page, doc *goquery.Document, submitted *bool) error {
	if !s.Routes.casPage(page.URL) {
		if isLogin(doc) || (s.Routes.VPN != nil && sameOrigin(page.URL, s.Routes.VPN) && page.URL.Path == "/login") {
			return ErrLogin
		}
		return nil
	}
	if *submitted || s.Username == "" || s.Password == "" {
		return ErrLogin
	}
	form := doc.Find("form#casLoginForm")
	if form.Length() != 1 || !strings.EqualFold(form.AttrOr("method", "post"), "post") {
		return ErrLogin
	}
	action := form.AttrOr("action", "")
	target, err := page.URL.Parse(action)
	if action == "" || err != nil || !s.Routes.casPage(target) || !s.Routes.Allow(target) {
		return ErrLogin
	}
	value := func(name, fallback string) string {
		return form.Find(`input[name="`+name+`"]`).First().AttrOr("value", fallback)
	}
	salt := form.Find("#pwdDefaultEncryptSalt").First().AttrOr("value", "")
	lt, execution := value("lt", ""), value("execution", "")
	if len(salt) != 16 || lt == "" || execution == "" {
		return ErrLogin
	}
	password, err := encryptPassword(s.Password, salt)
	if err != nil {
		return ErrLogin
	}
	*submitted = true
	result, err := s.HTTP.Do(ctx, target, url.Values{
		"username": {s.Username}, "password": {password}, "lt": {lt}, "execution": {execution},
		"_eventId": {value("_eventId", "submit")}, "dllt": {value("dllt", "userNamePasswordLogin")}, "rmShown": {value("rmShown", "1")},
	}, false, false)
	if err != nil {
		return err
	}
	doc, err = Document(result.HTML)
	if err != nil {
		return err
	}
	if s.Routes.casPage(result.URL) || isLogin(doc) {
		return ErrLogin
	}
	return nil
}

func (s *Session) SearchDocument(page client.Page) (*goquery.Document, error) {
	doc, err := Document(page.HTML)
	if err != nil {
		return nil, err
	}
	if s.Routes.casPage(page.URL) || isLogin(doc) ||
		(sameOrigin(page.URL, s.Routes.VPN) && page.URL.Path == "/login") ||
		s.Routes.academicPage(page.URL, "localLogin.action") || s.Routes.academicPage(page.URL, "login.action") || s.Routes.academicPage(page.URL, "homeExt.action") || s.Routes.academicPage(page.URL, "home.action") {
		return nil, client.ErrExpired
	}
	if !s.Routes.academicPage(page.URL, SearchPath) && !s.Routes.academicPage(page.URL, FormPath) {
		return nil, client.ErrProtocol
	}
	return doc, nil
}

func isLogin(doc *goquery.Document) bool {
	return doc.Find(`#casLoginForm, input[type="password"]`).Length() > 0
}

func Document(source string) (*goquery.Document, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(source), "\uFEFF"))
	fragment := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(fragment, "<tbody"):
		trimmed = `<table class="gridtable">` + trimmed + "</table>"
	case strings.HasPrefix(fragment, "<tr"):
		trimmed = `<table class="gridtable"><tbody>` + trimmed + "</tbody></table>"
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(trimmed))
	if err != nil {
		return nil, client.ErrProtocol
	}
	if challenge(doc) {
		return nil, client.ErrChallenge
	}
	messages := doc.Find("body").Clone()
	messages.Find("script, style, template, [hidden]").Remove()
	text := strings.Join(strings.Fields(messages.Text()), "")
	for _, marker := range []string{"访问受限", "访问被拒绝", "拒绝访问", "无权访问", "没有访问权限", "没有权限", "无权限", "请不要过快点击", "访问过于频繁", "Accessdenied", "Forbidden"} {
		if strings.Contains(strings.ToLower(text), strings.ToLower(marker)) {
			return nil, client.ErrDenied
		}
	}
	return doc, nil
}

func challenge(doc *goquery.Document) bool {
	if doc.Find(`input[name="needCaptcha"][value="true"], input[name="needCaptcha"][value="1"]`).Length() > 0 {
		return true
	}
	found := false
	doc.Find(`input[name="captcha"], input[name="captchaResponse"], input[name="captchaCode"], input[name="otp"], input[name="smsCode"], #captchaImg, [data-sitekey]`).EachWithBreak(func(_ int, node *goquery.Selection) bool {
		hidden := node.Closest(`[hidden], [type="hidden"], .hidden, .hide, .d-none, [aria-hidden="true"], [style*="display:none"], [style*="display: none"], template`)
		found = hidden.Length() == 0
		return !found
	})
	if found {
		return true
	}
	visible := doc.Find("body").Clone()
	visible.Find(`script, style, template, [hidden], .hide, .hidden, .d-none, [aria-hidden="true"], [style*="display:none"], [style*="display: none"]`).Remove()
	text := strings.Join(strings.Fields(visible.Text()), "")
	for _, phrase := range []string{"请输入验证码", "验证码错误", "需要验证码", "请完成验证码", "滑块验证", "请拖动滑块", "请拖动下方滑块", "设备校验", "设备验证", "需要验证设备", "双重登录认证", "二次认证", "多因素认证"} {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func encryptPassword(password, salt string) (string, error) {
	block, err := aes.NewCipher([]byte(salt))
	if err != nil {
		return "", err
	}
	// CAS 丢弃解密后的前 64 字节，随机 IV 可直接用于标准 CBC。
	prefix := make([]byte, 64)
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(prefix); err != nil {
		return "", err
	}
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for i := range prefix {
		prefix[i] = alphabet[prefix[i]&63]
	}
	for i := range iv {
		iv[i] = alphabet[iv[i]&63]
	}
	plain := append(prefix, []byte(password)...)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	for range pad {
		plain = append(plain, byte(pad))
	}
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(plain, plain)
	return base64.StdEncoding.EncodeToString(plain), nil
}
