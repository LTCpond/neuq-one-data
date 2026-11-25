package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	AcademicHost = "jwxt.neuq.edu.cn"
	IdentityHost = "ids.neuq.edu.cn"
	VPNHost      = "vpn.neuq.edu.cn"
)

type Config struct {
	Username, Password                               string
	UseVPN                                           bool
	EAMS                                             *url.URL
	Buildings                                        []string
	Days, MaxRequests, MaxPages, Retries             int
	Delay, Timeout, TaskTimeout, TodayTTL, FutureTTL time.Duration
}

var BuildingName = regexp.MustCompile(`^[\p{Han}]{2,16}$`)
var VPNPath = regexp.MustCompile(`^/(?:https?)/[a-f0-9]{24,128}/eams/$`)

func HTTPS(u *url.URL, host string) bool {
	return u != nil && u.Scheme == "https" && u.Hostname() == host &&
		(u.Port() == "" || u.Port() == "443") && u.User == nil && u.Opaque == ""
}

func Load() (Config, error) {
	var c Config
	var configErr error
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	number := func(key string, fallback, min, max int) int {
		value, err := strconv.Atoi(get(key, strconv.Itoa(fallback)))
		if err != nil || value < min || value > max {
			configErr = fmt.Errorf("%s 必须为 %d 至 %d 的整数", key, min, max)
		}
		return value
	}
	duration := func(key string, fallback, min, max time.Duration) time.Duration {
		value, err := time.ParseDuration(get(key, fallback.String()))
		if err != nil || value < min || value > max {
			configErr = fmt.Errorf("%s 必须为 %s 至 %s 的时长", key, min, max)
		}
		return value
	}
	c.Username, c.Password = os.Getenv("NEUQ_USERNAME"), os.Getenv("NEUQ_PASSWORD")
	c.UseVPN, configErr = strconv.ParseBool(get("NEUQ_USE_WEBVPN", "false"))
	if configErr != nil {
		return c, fmt.Errorf("NEUQ_USE_WEBVPN 必须为布尔值")
	}
	c.EAMS, _ = url.Parse("https://" + AcademicHost + "/eams/")
	if c.UseVPN {
		u, err := url.Parse(os.Getenv("NEUQ_WEBVPN_EAMS_URL"))
		if err != nil || !HTTPS(u, VPNHost) || !VPNPath.MatchString(u.EscapedPath()) || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return c, fmt.Errorf("NEUQ_WEBVPN_EAMS_URL 必须为官方 WebVPN 门户中以 /eams/ 结尾的教务资源 HTTPS 地址")
		}
		c.EAMS = u
	}
	seen := map[string]bool{}
	for _, item := range strings.Split(get("NEUQ_BUILDINGS", "工学馆,科技楼,人文楼"), ",") {
		name := strings.TrimSpace(item)
		if !BuildingName.MatchString(name) || seen[name] {
			return c, fmt.Errorf("NEUQ_BUILDINGS 包含无效或重复的楼栋")
		}
		seen[name] = true
		c.Buildings = append(c.Buildings, name)
	}
	if len(c.Buildings) > 20 {
		return c, fmt.Errorf("NEUQ_BUILDINGS 最多支持 20 栋")
	}
	c.Days = number("NEUQ_DAYS", 7, 1, 14)
	c.MaxRequests = number("NEUQ_MAX_REQUESTS", 200, 1, 250)
	c.MaxPages = number("NEUQ_MAX_PAGES", 4, 1, 10)
	c.Retries = number("NEUQ_RETRIES", 2, 0, 4)
	c.Delay = duration("NEUQ_REQUEST_DELAY", 2*time.Second, time.Second, time.Minute)
	c.Timeout = duration("NEUQ_REQUEST_TIMEOUT", 20*time.Second, time.Second, time.Minute)
	c.TaskTimeout = duration("NEUQ_TASK_TIMEOUT", 12*time.Minute, time.Minute, 12*time.Minute)
	c.TodayTTL = duration("NEUQ_TODAY_TTL", 2*time.Hour, time.Hour, 24*time.Hour)
	c.FutureTTL = duration("NEUQ_FUTURE_TTL", 26*time.Hour, time.Hour, 72*time.Hour)
	return c, configErr
}
