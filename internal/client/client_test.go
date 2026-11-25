package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryAfterAndBudget(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer s.Close()
	endpoint, _ := url.Parse(s.URL)
	c := New(s.Client(), func(u *url.URL) bool { return u.Host == endpoint.Host }, time.Second, 0, 2, 2)
	ctx, cancel := context.WithTimeout(t.Context(), 350*time.Millisecond)
	defer cancel()
	_, err := c.Do(ctx, endpoint, nil, false, true)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("Retry-After：%v，%d 次请求", err, calls.Load())
	}
	capped := New(s.Client(), func(u *url.URL) bool { return u.Host == endpoint.Host }, time.Second, 0, 1, 2)
	_, err = capped.Do(t.Context(), endpoint, nil, false, true)
	if !errors.Is(err, ErrBudget) || calls.Load() != 2 {
		t.Fatalf("请求预算：%v，%d 次请求", err, calls.Load())
	}
}

func TestTransientResponseRetries(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, "<p>ok</p>")
	}))
	defer s.Close()
	endpoint, _ := url.Parse(s.URL)
	c := New(s.Client(), func(u *url.URL) bool { return u.Host == endpoint.Host }, time.Second, 0, 4, 2)
	page, err := c.Do(t.Context(), endpoint, url.Values{"timeBegin": {"1"}}, true, true)
	if err != nil || page.HTML != "<p>ok</p>" || calls.Load() != 2 {
		t.Fatalf("重试结果 %v，共 %d 次请求", err, calls.Load())
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	now := time.Date(2026, 11, 25, 2, 0, 0, 0, time.UTC)
	if got := retryAfter(now.Add(time.Minute).Format(http.TimeFormat), now); got != time.Minute {
		t.Fatal(got)
	}
	if got := retryAfter(now.Add(-time.Minute).Format(http.TimeFormat), now); got != 0 {
		t.Fatal(got)
	}
}

func TestRejectsCredentialReplayRedirect(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	endpoint, _ := url.Parse(s.URL)
	c := New(s.Client(), func(u *url.URL) bool { return u.Host == endpoint.Host }, time.Second, 0, 5, 2)
	_, err := c.Do(t.Context(), endpoint, url.Values{"password": {"test-only"}}, false, false)
	if !errors.Is(err, ErrProtocol) || calls.Load() != 1 {
		t.Fatalf("敏感表单被重放，共 %d 次请求，错误 %v", calls.Load(), err)
	}
}
