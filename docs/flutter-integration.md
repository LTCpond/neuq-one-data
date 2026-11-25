# NEUQ One 客户端接入

Flutter 的入口是 `classroomsProvider`。它从 `SchoolClassroomsDataSource.fetchClassrooms(day)` 获取结果，调用方使用 `ClassroomSchedules`、`CampusBuilding` 和 `ClassroomSchedule`。当前 Provider 和页面限制当日查询。首期只需读取 `today.json`，将来放开日期选择时，可从 `week.json` 以北京时间自然日筛选。

1. 在 services feature 的数据层增加公共 API 适配器，由 Riverpod 提供接口。使用 `date` 和版本索引本地缓存，保留完整时段状态、`collectedAt` 和 `expiresAt`。缓存按目标日期查询，先显示可用缓存，再按进入页面和手动刷新触发条件 GET。每次更新从同一份完整快照构建。
2. 校验 `schemaVersion == 1`、响应日期、时区和时间字段。网络失败时继续使用同日期缓存，并按 `expiresAt` 更新状态。跨天时按北京时间重算日期。HTTP 404、未知版本和非法响应均视为公共数据不可用。
3. 将已知楼栋名映射到 `CampusBuilding`，将 `1-2` 等键转换成 `1, 3, 5, 7, 9, 11`，用 `periodCount: 12, periodsPerGroup: 2` 构造 `ClassroomSchedule`。有效 `success` 的 `rooms: []` 映射为空集合。`unavailable` 与已过期时段从传给可用性推导的时段映射中省略，避免误判全天空闲或下节有课。
4. 通过 Provider 返回值或配套状态对象把每时段时间戳和状态交给现有页面。现有 `syncedAt` 可承载 `updatedAt`，时段详情用自己的 `collectedAt` 展示。过期时段可显示带明确提示的旧列表，成功空数组显示暂无空教室，缺失状态显示查询不可用。
5. 保留现有 WebView 教务适配器。公共数据不可用时提供用户主动操作的「通过教务查询」入口，沿用登录及会话校验。拉取公共 API 时使用普通 HTTP 客户端，学校凭据留在原有学校认证链路。
6. 接入测试应覆盖跨北京时间午夜、旧版本和畸形 JSON、HTTP 缓存、部分时段失败、过期的空数组以及下一时段缺失时的标签行为。

接口契约见 [空教室 API v1](api.md)。Flutter 数据源及过滤规则见 [NEUQ One 客户端仓库](https://github.com/LTCpond/neuq-one)。
