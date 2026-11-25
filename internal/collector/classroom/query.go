package classroom

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/LTCpond/neuq-one-data/internal/model"
	"github.com/PuerkitoBio/goquery"
)

func query(date string, start, page int, buildingID string) url.Values {
	form := url.Values{
		"classroom.type.id": {""}, "classroom.campus.id": {""}, "classroom.building.id": {buildingID},
		"seats": {""}, "classroom.name": {""}, "cycleTime.cycleCount": {"1"}, "cycleTime.cycleType": {"1"},
		"cycleTime.dateBegin": {date}, "cycleTime.dateEnd": {date}, "roomApplyTimeType": {"0"},
		"timeBegin": {strconv.Itoa(start)}, "timeEnd": {strconv.Itoa(start + 1)},
	}
	if buildingID == "" {
		form.Set("pageSize", "1000")
	}
	if page > 1 {
		form.Set("pageNo", strconv.Itoa(page))
	}
	return form
}

var numberID = regexp.MustCompile(`^[0-9]{1,10}$`)

func buildingIDs(doc *goquery.Document, buildings []string) map[string]string {
	ids := make(map[string]string, len(buildings))
	users := make(map[string]int)
	for _, name := range buildings {
		options := map[string]bool{}
		doc.Find(`select[name="classroom.building.id"] option:not([disabled])`).Each(func(_ int, option *goquery.Selection) {
			id := option.AttrOr("value", "")
			label := compact(option.AttrOr("label", option.Text()))
			if numberID.MatchString(id) && (label == name || stripCampusLabel(label) == name) {
				options[id] = true
			}
		})
		if len(options) == 0 && name == "工学馆" {
			// 现有客户端已验证。若页面明确列出了其他用途的 1 则避免复用。
			collisions := doc.Find(`select[name="classroom.building.id"] option[value="1"]`)
			if collisions.Length() == 0 {
				options["1"] = true
			}
		}
		if len(options) == 1 {
			for id := range options {
				ids[name] = id
				users[id]++
			}
		}
	}
	for name, id := range ids {
		if users[id] > 1 {
			delete(ids, name)
		}
	}
	return ids
}

var specialRooms = []string{"体育教学场地", "体育馆", "机房", "实验室", "活动教室", "研讨室", "多功能", "智慧教室", "不排课教室", "语音室"}
var scienceStudy = regexp.MustCompile(`^自主学习室([A-Z])科技楼(.+)$`)
var noteSuffix = regexp.MustCompile(`[（(]?具体安排以开课部门通知为准[）)]?$`)

type room struct{ name, building, equipment, capacity string }

func compact(s string) string { return strings.Join(strings.Fields(s), "") }

const campusAlias = `(?:学校本部|校本部|本部)`
const campusSeparators = `[-－—_/／·:：]`

var campusPrefix = regexp.MustCompile(`^(?:[（(【\[]` + campusAlias + `[）)】\]]|` + campusAlias + `)` + campusSeparators + `*`)
var campusSuffix = regexp.MustCompile(`(?:[（(【\[]` + campusAlias + `[）)】\]]|` + campusSeparators + campusAlias + `)$`)

// 校区列样本使用“学校本部”和“校本部”。只清理这些校区别名及常见分隔符。
func stripCampusLabel(value string) string {
	label := compact(value)
	without := campusPrefix.ReplaceAllString(label, "")
	without = campusSuffix.ReplaceAllString(without, "")
	if without != label {
		return strings.Trim(without, "（）()【】[]")
	}
	return label
}

func excludedRoom(r room) bool {
	for _, label := range specialRooms {
		if strings.Contains(r.name, label) || strings.Contains(r.equipment, label) {
			return true
		}
	}
	capacity, err := strconv.Atoi(r.capacity)
	return err == nil && capacity == 0
}

func collectRoom(r room, building string) (string, model.RoomDetail, bool) {
	var detail model.RoomDetail
	if excludedRoom(r) {
		return "", detail, false
	}
	if capacity, err := strconv.Atoi(r.capacity); err == nil && capacity > 0 && capacity <= 3000 {
		detail.Capacity = &capacity
	}
	name := noteSuffix.ReplaceAllString(strings.ToUpper(stripCampusLabel(r.name)), "")
	if building == "科技楼" {
		if m := scienceStudy.FindStringSubmatch(name); m != nil {
			name = m[2] + "自习室" + m[1]
		}
	}
	name = strings.TrimPrefix(name, building)
	if !model.RoomName.MatchString(name) {
		return "", detail, false
	}
	detail.Equipment = model.Equipment(r.equipment)
	return name, detail, true
}
