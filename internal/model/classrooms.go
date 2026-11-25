package model

import (
	"fmt"
	"regexp"
	"slices"
	"time"
	_ "time/tzdata"

	"github.com/LTCpond/neuq-one-data/internal/config"
)

const Timezone = "Asia/Shanghai"
const Source = "NEUQ 教务系统"
const SchemaVersion = 1

const (
	Success     = "success"
	Stale       = "stale"
	Unavailable = "unavailable"
)

var Beijing = func() *time.Location {
	location, err := time.LoadLocation(Timezone)
	if err != nil {
		panic(err)
	}
	return location
}()

var Periods = [...]string{"1-2", "3-4", "5-6", "7-8", "9-10", "11-12"}
var RoomName = regexp.MustCompile(`^(?:[A-Z]?[0-9]{2,5}[A-Z]?(?:-[A-Z0-9]{1,4}){0,2})(?:自习室[A-Z])?$`)

var publicEquipment = []string{
	"普通教室", "标准教室", "多媒体教室", "多媒体大教室", "多媒体中教室",
	"多媒体小教室", "录播教室", "普通多媒体教室", "阶梯教室",
}

func Equipment(label string) *string {
	if !slices.Contains(publicEquipment, label) {
		return nil
	}
	return &label
}

type RoomDetail struct {
	Capacity  *int    `json:"capacity"`
	Equipment *string `json:"equipment"`
}

type Slot struct {
	Status      string                `json:"status"`
	Rooms       []string              `json:"rooms"`
	RoomDetails map[string]RoomDetail `json:"roomDetails"`
	CollectedAt *time.Time            `json:"collectedAt"`
	ExpiresAt   *time.Time            `json:"expiresAt"`
}

type Day struct {
	Date      string                     `json:"date"`
	UpdatedAt *time.Time                 `json:"updatedAt"`
	Complete  bool                       `json:"complete"`
	Buildings map[string]map[string]Slot `json:"buildings"`
}

type Header struct {
	SchemaVersion int       `json:"schemaVersion"`
	Timezone      string    `json:"timezone"`
	Source        string    `json:"source"`
	GeneratedAt   time.Time `json:"generatedAt"`
}

type Today struct {
	Header
	Day
}

type Week struct {
	Header
	Days []Day `json:"days"`
}

type Status struct {
	Header
	Date             string `json:"date"`
	State            string `json:"state"`
	TodayComplete    bool   `json:"todayComplete"`
	FreshSlots       int    `json:"freshSlots"`
	StaleSlots       int    `json:"staleSlots"`
	UnavailableSlots int    `json:"unavailableSlots"`
}

func Date(t time.Time) string { return t.In(Beijing).Format(time.DateOnly) }

func NewDay(date string, buildings []string) Day {
	d := Day{Date: date, Buildings: make(map[string]map[string]Slot, len(buildings))}
	for _, name := range buildings {
		d.Buildings[name] = make(map[string]Slot, len(Periods))
		for _, period := range Periods {
			d.Buildings[name][period] = Slot{Status: Unavailable}
		}
	}
	return d
}

func NewSlot(details map[string]RoomDetail, at time.Time, ttl time.Duration) Slot {
	rooms := make([]string, 0, len(details))
	for name := range details {
		rooms = append(rooms, name)
	}
	slices.Sort(rooms)
	at = at.In(Beijing)
	expires := at.Add(ttl)
	return Slot{Success, rooms, details, &at, &expires}
}

// 仅从相同自然日读取历史时段。尝试失败的时段立即标记为过期。
func (d *Day) Carry(previous Day, attempted bool, now time.Time) {
	if previous.Date != d.Date {
		return
	}
	for name, slots := range d.Buildings {
		for period, current := range slots {
			old := previous.Buildings[name][period]
			if current.Status != Unavailable || old.CollectedAt == nil {
				continue
			}
			if attempted || !now.Before(*old.ExpiresAt) {
				old.Status = Stale
			}
			slots[period] = old
		}
	}
}

func (d *Day) Recalculate(now time.Time) {
	d.Complete, d.UpdatedAt = true, nil
	for _, periods := range d.Buildings {
		for period, slot := range periods {
			if slot.Status == Success && !now.Before(*slot.ExpiresAt) {
				slot.Status = Stale
				periods[period] = slot
			}
			d.Complete = d.Complete && slot.Status == Success
			if slot.CollectedAt != nil && (d.UpdatedAt == nil || slot.CollectedAt.After(*d.UpdatedAt)) {
				d.UpdatedAt = slot.CollectedAt
			}
		}
	}
}

func NewWeek(days []Day, now time.Time) Week {
	w := Week{Header{SchemaVersion, Timezone, Source, now.In(Beijing)}, days}
	for i := range w.Days {
		w.Days[i].Recalculate(now)
	}
	return w
}

func (w Week) Today() Today { return Today{w.Header, w.Days[0]} }

func (w Week) Status() Status {
	s := Status{Header: w.Header, Date: w.Days[0].Date, TodayComplete: w.Days[0].Complete}
	for _, day := range w.Days {
		for _, periods := range day.Buildings {
			for _, slot := range periods {
				switch slot.Status {
				case Success:
					s.FreshSlots++
				case Stale:
					s.StaleSlots++
				case Unavailable:
					s.UnavailableSlots++
				}
			}
		}
	}
	s.State = "complete"
	if s.UnavailableSlots+s.StaleSlots > 0 {
		s.State = "partial"
	}
	if s.FreshSlots+s.StaleSlots == 0 {
		s.State = Unavailable
	}
	return s
}

func (w Week) Validate() error {
	if w.SchemaVersion != SchemaVersion || w.Timezone != Timezone || w.Source != Source ||
		!beijingTime(w.GeneratedAt) || len(w.Days) == 0 || len(w.Days) > 14 {
		return fmt.Errorf("快照头部或日期数量无效")
	}
	first := w.Days[0]
	for i, d := range w.Days {
		if d.Date != Date(w.GeneratedAt.In(Beijing).AddDate(0, 0, i)) || len(d.Buildings) != len(first.Buildings) ||
			len(d.Buildings) == 0 || len(d.Buildings) > 20 {
			return fmt.Errorf("快照日期或楼栋范围无效")
		}
		var latest *time.Time
		complete := true
		for name, slots := range d.Buildings {
			if _, ok := first.Buildings[name]; !ok || !config.BuildingName.MatchString(name) || len(slots) != len(Periods) {
				return fmt.Errorf("楼栋或时段不完整")
			}
			for _, period := range Periods {
				slot := slots[period]
				if !slot.valid(w.GeneratedAt, d.Date) {
					return fmt.Errorf("时段数据无效（第 %d 天，%s）", i+1, period)
				}
				complete = complete && slot.Status == Success
				if slot.CollectedAt != nil && (latest == nil || slot.CollectedAt.After(*latest)) {
					latest = slot.CollectedAt
				}
			}
		}
		if d.Complete != complete || !sameTime(d.UpdatedAt, latest) {
			return fmt.Errorf("日期汇总与时段不一致")
		}
	}
	return nil
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return beijingTime(*a) && a.Equal(*b)
}

func beijingTime(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && offset == 8*60*60
}

func (s Slot) valid(now time.Time, targetDate string) bool {
	if s.Status == Unavailable {
		return s.Rooms == nil && s.RoomDetails == nil && s.CollectedAt == nil && s.ExpiresAt == nil
	}
	if (s.Status != Success && s.Status != Stale) || s.Rooms == nil || s.RoomDetails == nil ||
		s.CollectedAt == nil || s.ExpiresAt == nil || !beijingTime(*s.CollectedAt) || !beijingTime(*s.ExpiresAt) ||
		s.CollectedAt.After(now) || Date(*s.CollectedAt) > targetDate || !s.ExpiresAt.After(*s.CollectedAt) ||
		s.ExpiresAt.Sub(*s.CollectedAt) > 72*time.Hour || (s.Status == Success && !now.Before(*s.ExpiresAt)) ||
		len(s.Rooms) != len(s.RoomDetails) || !slices.IsSorted(s.Rooms) {
		return false
	}
	for i, name := range s.Rooms {
		detail, ok := s.RoomDetails[name]
		if !ok || !RoomName.MatchString(name) || (i > 0 && s.Rooms[i-1] == name) ||
			(detail.Capacity != nil && (*detail.Capacity < 1 || *detail.Capacity > 3000)) ||
			(detail.Equipment != nil && Equipment(*detail.Equipment) == nil) {
			return false
		}
	}
	return true
}
