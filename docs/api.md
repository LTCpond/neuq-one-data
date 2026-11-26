# 公共数据服务接口

## 空教室 API v1

生产根地址为 `https://neuq-one-data.pages.dev`。

三个接口返回 UTF-8 JSON，允许跨域请求。日期按 `Asia/Shanghai` 解释，时间为带 `+08:00` 偏移的 RFC 3339。三份文件来自同一批次，可用 `generatedAt` 核对版本。静态响应缓存 60 秒。

| GET 路径 | 内容 |
| --- | --- |
| `/api/v1/classrooms/today.json` | 当天全部已配置楼栋、六个时段和采集时间，一次 GET |
| `/api/v1/classrooms/week.json` | `days` 自然日数组，按日期升序，首项等同当天接口的日期对象 |
| `/api/v1/status.json` | 快照的北京时间日期、当天完整性、整周状态及三种时段数量 |

三个接口的共同字段是 `schemaVersion`（当前为 `1`）、`timezone`、`source` 和 `generatedAt`。`generatedAt` 记录快照生成时间。`updatedAt` 是目标日所含数据的最新实际采集时间。响应的每个时段都有独立的 `collectedAt` 和 `expiresAt`。全部不可用的日期的 `updatedAt` 为 `null`。

按 `buildings[楼栋][时段]` 读取。每个楼栋有 `1-2`、`3-4`、`5-6`、`7-8`、`9-10`、`11-12` 六个键。

| `status` | `rooms` | 含义 |
| --- | --- | --- |
| `success` | 字符串数组；空数组表示已确认没有空教室 | 生成时在有效期内且查询成功 |
| `stale` | 上次成功查询的字符串数组 | 同日期的旧数据，已到期或此次查询失败 |
| `unavailable` | `null` | 暂无已确认的查询结果 |

`roomDetails` 以 `rooms` 中的房间名为键。每项含 `capacity`（座位数）和 `equipment`（教室配置分类）。缺失或未知的字段为 `null`。空数组对应空对象。不可用时段的详情和两个时间字段均为 `null`。

以下仅为合成示例，展示一个时段对象。

```json
{
  "status": "success",
  "rooms": ["201"],
  "roomDetails": {"201": {"capacity": 80, "equipment": "普通教室"}},
  "collectedAt": "2026-11-25T13:19:12+08:00",
  "expiresAt": "2026-11-25T15:19:12+08:00"
}
```

`complete` 表示目标日所有配置楼栋和时段在快照生成时均为 `success`。`status.json` 的 `state` 可为 `complete`、`partial` 或 `unavailable`，分别表示整周全部时段成功、存在部分不可用或过期数据、所有时段均不可用。其余字段为 `date`、`todayComplete`、`freshSlots`、`staleSlots` 和 `unavailableSlots`。统计以 `generatedAt` 为准。

客户端应先检查响应日期及结构，再用当前时间比较每个 `expiresAt`，将到期时段视为过期。整次采集失败时线上仍指向最后发布的快照，因此跨天后 `today.json` 可能仍是旧日期。此时只有 `week.json` 中日期精确匹配的条目可以作为候选，找不到目标日期或收到 HTTP 404 时展示不可用。

JSON Schema 可直接校验任一接口，也可分别引用 `#/$defs/Today`、`#/$defs/Week` 和 `#/$defs/Status`。Go 校验器还验证自然日连续性、时间先后、三个文件一致性、指标汇总和房间详情键的对应关系。见 [api-v1.schema.json](api-v1.schema.json)。

## 静态资源

生产根地址为 `https://neuq-one-assets.pages.dev`。

| GET 路径 | 内容 |
| --- | --- |
| `/assets/maps/campus-map.png` | 全分辨率校园地图 PNG |
| `/assets/policies/index.json` | 政策目录，`version` 为 `1`，`policies` 条目包含 `id`、`title`、`category` 和 `sha256` |
| `/assets/policies/<id>.pdf` | 目录对应的政策原件，可按目录中的 SHA-256 校验 |
| `/assets/app/latest.json` | App 正式版本清单，`schemaVersion` 为 `1` |

Cloudflare Pages 提供 `ETag`，`/assets/*` 使用 `Cache-Control: public, no-cache, no-transform`。条件请求可得到 `304`。迁移期间地图、政策目录和 PDF 同时保留在旧数据域名，客户端使用相同的路径。

尚无正式 Release 时，版本清单返回 `{"schemaVersion":1,"release":null}`。有正式 Release 时，`release.version` 为 `MAJOR.MINOR.PATCH`，每段为无前导零的十进制整数（零除外），`release.url` 指向同一版本的 GitHub Release 页面。示例版本为虚构值。

```json
{
  "schemaVersion": 1,
  "release": {
    "version": "1.2.3",
    "url": "https://github.com/LTCpond/neuq-one/releases/tag/v1.2.3"
  }
}
```

Android 安装包在 GitHub Release 页面提供。[App 发布页](https://github.com/LTCpond/neuq-one/releases)
