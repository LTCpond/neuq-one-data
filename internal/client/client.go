package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/html/charset"
	"golang.org/x/net/publicsuffix"
)

var (
	ErrExpired     = errors.New("教务会话失效")
	ErrDenied      = errors.New("学校拒绝或限制访问")
	ErrChallenge   = errors.New("学校要求交互验证")
	ErrBudget      = errors.New("已用完请求预算")
	ErrTooLarge    = errors.New("学校响应超过大小限制")
	ErrProtocol    = errors.New("学校响应或重定向不符合预期")
	ErrNetwork     = errors.New("学校请求网络异常或超时")
	ErrUnavailable = errors.New("学校暂时不可用")
)

const maxBody = 2_000_000

type Client struct {
	http         *http.Client
	allow        func(*url.URL) bool
	gate         chan struct{}
	interval     time.Duration
	max, retries int
	count        atomic.Int64
	next         time.Time
}

type Page struct {
	URL  *url.URL
	HTML string
}

func New(httpClient *http.Client, allow func(*url.URL) bool, timeout, interval time.Duration, max, retries int) *Client {
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	return &Client{
		http: &http.Client{
			Transport: httpClient.Transport, Jar: jar, Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		allow: allow, interval: interval, max: max, retries: retries, gate: make(chan struct{}, 1),
	}
}

func (c *Client) Count() int { return int(c.count.Load()) }

func (c *Client) BudgetExhausted() bool { return c.Count() >= c.max }

func (c *Client) Do(ctx context.Context, target *url.URL, form url.Values, ajax, retry bool) (Page, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return Page{}, ctx.Err()
	}
	method := http.MethodGet
	if form != nil {
		method = http.MethodPost
	}
	for redirects := 0; redirects < 10; redirects++ {
		if !c.allow(target) {
			return Page{}, ErrProtocol
		}
		page, next, err := c.request(ctx, method, target, form, ajax, retry)
		if err != nil || next == nil {
			return page, err
		}
		if next.Scheme == "http" && next.User == nil && (next.Port() == "" || next.Port() == "80") {
			https := *next
			https.Scheme, https.Host = "https", next.Hostname()
			if c.allow(&https) {
				next = &https
			}
		}
		method, form, target = http.MethodGet, nil, next
	}
	return Page{}, ErrProtocol
}

func (c *Client) request(ctx context.Context, method string, target *url.URL, form url.Values, ajax, retry bool) (Page, *url.URL, error) {
	for attempt := 0; ; attempt++ {
		if c.BudgetExhausted() {
			return Page{}, nil, ErrBudget
		}
		if err := wait(ctx, time.Until(c.next)); err != nil {
			return Page{}, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, target.String(), strings.NewReader(form.Encode()))
		if err != nil {
			return Page{}, nil, ErrProtocol
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", target.Scheme+"://"+target.Host)
		}
		if ajax {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
		req.Header.Set("User-Agent", "NEUQ-One-Data/1")
		req.Header.Set("Cache-Control", "no-cache")
		c.count.Add(1)
		page, next, temporary, after, err := c.send(req)
		c.deferUntil(max(c.interval, after))
		if ctx.Err() != nil {
			return Page{}, nil, ctx.Err()
		}
		if !temporary || !retry || attempt >= c.retries {
			return page, next, err
		}
		backoff := time.Second << attempt
		c.deferUntil(backoff + rand.N(backoff/2))
	}
}

func (c *Client) deferUntil(delay time.Duration) {
	if until := time.Now().Add(delay); until.After(c.next) {
		c.next = until
	}
}

func (c *Client) send(req *http.Request) (Page, *url.URL, bool, time.Duration, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		var network net.Error
		temporary := errors.As(err, &network) && network.Timeout()
		var operation *net.OpError
		temporary = temporary || errors.As(err, &operation) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		return Page{}, nil, temporary, 0, ErrNetwork
	}
	defer resp.Body.Close()
	after := retryAfter(resp.Header.Get("Retry-After"), time.Now())
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
		next, err := resp.Location()
		if err != nil || (req.Method == http.MethodPost && (resp.StatusCode == 307 || resp.StatusCode == 308)) {
			return Page{}, nil, false, after, ErrProtocol
		}
		return Page{}, next, false, after, nil
	case http.StatusUnauthorized:
		return Page{}, nil, false, after, ErrExpired
	case http.StatusRequestEntityTooLarge:
		return Page{}, nil, false, after, ErrTooLarge
	case 408, 500, 502, 503, 504:
		return Page{}, nil, true, after, fmt.Errorf("%w（HTTP %d）", ErrUnavailable, resp.StatusCode)
	case http.StatusOK:
	default:
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return Page{}, nil, false, after, fmt.Errorf("%w（HTTP %d）", ErrDenied, resp.StatusCode)
		}
		return Page{}, nil, false, after, fmt.Errorf("%w（HTTP %d）", ErrProtocol, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Page{}, nil, true, after, ErrNetwork
	}
	if len(data) > maxBody {
		return Page{}, nil, false, after, ErrTooLarge
	}

	encoding, _, known := charset.DetermineEncoding(data, resp.Header.Get("Content-Type"))
	if known {
		data, err = encoding.NewDecoder().Bytes(data)
		if err != nil {
			return Page{}, nil, false, after, ErrProtocol
		}
	}
	return Page{req.URL, string(data)}, nil, false, after, nil
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, int64((1<<63-1)/time.Second))) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(0, at.Sub(now))
	}
	return 0
}

func wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil || delay <= 0 {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
