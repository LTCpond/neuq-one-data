package exporter

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/LTCpond/neuq-one-data/internal/model"
)

const todayFile = "api/v1/classrooms/today.json"
const weekFile = "api/v1/classrooms/week.json"
const statusFile = "api/v1/status.json"
const maxJSON = 20 << 20

//go:embed headers.txt
var headers []byte

func LoadPrevious(ctx context.Context, source string) (model.Week, error) {
	if source == "" {
		return model.Week{}, nil
	}
	u, err := url.Parse(source)
	if err == nil && u.Host != "" {
		if u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
			(u.Port() != "" && u.Port() != "443") || (u.Path != "" && u.Path != "/") {
			return model.Week{}, errors.New("历史数据源必须为 HTTPS 站点根地址")
		}
		u.Path = "/" + weekFile
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return model.Week{}, errors.New("无法创建历史数据请求")
		}
		req.Header.Set("Cache-Control", "no-cache")
		httpClient := http.Client{
			Timeout:       15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		response, err := httpClient.Do(req)
		if err != nil {
			return model.Week{}, errors.New("无法读取上次发布的数据")
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return model.Week{}, nil
		}
		if response.StatusCode != http.StatusOK {
			return model.Week{}, fmt.Errorf("历史数据读取失败（HTTP %d）", response.StatusCode)
		}
		return readWeek(response.Body)
	}
	file, err := os.Open(source)
	if err != nil {
		return model.Week{}, errors.New("无法打开本地历史快照")
	}
	defer file.Close()
	return readWeek(file)
}

func readWeek(r io.Reader) (model.Week, error) {
	var w model.Week
	if err := decode(r, &w); err != nil {
		return w, err
	}
	return w, w.Validate()
}

func decode(r io.Reader, destination any) error {
	data, err := io.ReadAll(io.LimitReader(r, maxJSON+1))
	if err != nil || len(data) > maxJSON {
		return errors.New("JSON 文件无法读取或超过大小限制")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("JSON 格式或字段无效")
	}
	return nil
}

func Write(destination string, w model.Week) error {
	if err := w.Validate(); err != nil {
		return err
	}
	if w.Status().FreshSlots == 0 {
		return errors.New("快照缺少可用的新鲜时段")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); !errors.Is(err, fs.ErrNotExist) {
		return errors.New("输出目录必须尚未创建")
	}
	parent := filepath.Dir(destination)
	stage, err := os.MkdirTemp(parent, ".neuq-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for name, value := range map[string]any{todayFile: w.Today(), weekFile: w, statusFile: w.Status()} {
		target := filepath.Join(stage, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, append(data, '\n'), 0644); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "_headers"), headers, 0644); err != nil {
		return err
	}
	if err := Verify(stage); err != nil {
		return err
	}
	return os.Rename(stage, destination)
}

func Verify(root string) error {
	allowed := map[string]bool{todayFile: true, weekFile: true, statusFile: true, "_headers": true}
	parents := map[string]bool{"api": true, "api/v1": true, path.Dir(todayFile): true}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() && (relative == "." || parents[relative]) {
			return nil
		}
		if !entry.Type().IsRegular() || !allowed[relative] {
			return errors.New("输出目录包含非预期文件")
		}
		delete(allowed, relative)
		return nil
	})
	if err != nil {
		return err
	}
	if len(allowed) != 0 {
		return errors.New("输出目录缺少发布文件")
	}
	w, err := LoadPrevious(context.Background(), filepath.Join(root, filepath.FromSlash(weekFile)))
	if err != nil {
		return err
	}
	if w.Status().FreshSlots == 0 {
		return errors.New("快照缺少可用的新鲜时段")
	}
	today, status := model.Today{}, model.Status{}
	for name, item := range map[string]struct{ output, expected any }{
		todayFile: {&today, w.Today()}, statusFile: {&status, w.Status()},
	} {
		if err := readOutput(root, name, item.output); err != nil {
			return err
		}
		encoded, err := json.Marshal(item.output)
		if err != nil {
			return err
		}
		expected, err := json.Marshal(item.expected)
		if err != nil {
			return err
		}
		if !bytes.Equal(encoded, expected) {
			return errors.New("发布文件的数据或版本不一致")
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "_headers"))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, headers) {
		return errors.New("静态响应头配置无效")
	}
	return nil
}

func readOutput(root, name string, destination any) error {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return err
	}
	defer file.Close()
	if err := decode(file, destination); err != nil {
		return fmt.Errorf("%s：%w", strings.TrimPrefix(name, "api/v1/"), err)
	}
	return nil
}
