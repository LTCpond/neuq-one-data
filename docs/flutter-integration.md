# NEUQ One 客户端接入

Flutter 通过 Riverpod 提供 `PublicDataClient`，公共请求不使用学校会话。默认根地址可分别用 `--dart-define=NEUQ_ONE_DATA_ORIGIN` 和 `--dart-define=NEUQ_ONE_ASSETS_ORIGIN` 覆盖。[客户端接口和构建示例](https://github.com/LTCpond/neuq-one/blob/main/docs/public-data.md)

## 空教室

`classroomsProvider` 从 `https://neuq-one-data.pages.dev/api/v1/classrooms/week.json` 读取七日快照，保留日期、每个时段的状态、`collectedAt` 和 `expiresAt`。快照与 `ETag` 等验证信息保存在设备级缓存，刷新失败时保留最近一次有效的快照。客户端按北京时间匹配日期，按时段有效期判断是否过期。

有效时段的 `rooms: []` 表示暂无空教室。过期数据可带提示显示，缺失时段显示不可用。过期或缺失的时段不参与全天空闲和下节有课的计算。[空教室 API v1](api.md#空教室-api-v1)

## 静态资源

新版客户端从 `https://neuq-one-assets.pages.dev` 读取静态资源。

- `/assets/maps/campus-map.png` 提供原分辨率 PNG。客户端先展示已校验的本地文件，联网校验更新时使用同一域名的 HTTP 验证信息。`304` 或请求和解码失败时继续使用已缓存的地图。
- `/assets/policies/index.json` 提供政策目录，`/assets/policies/<id>.pdf` 提供对应原件。缓存目录可离线使用，PDF 按目录中的 SHA-256 校验后存储。
- `/assets/app/latest.json` 提供两个发布渠道的版本、GitHub Release 页面和纯文本摘要。客户端按安装包的构建渠道选择候选版本。手动和启动检查读取同一清单，APK 从 GitHub Releases 下载。[清单格式](api.md#静态资源)

旧版客户端可在 `neuq-one-data` 的原路径访问地图和政策。[静态资源接口](api.md#静态资源)

## 后续 Azure 主源接入

通过国内网络验证后，未来客户端可优先请求 Azure，并在响应失败、结构无效或找不到有效快照时回退到 Cloudflare。空教室切换时需要同时切换域名和路径前缀，Azure 使用 `/data/v1/`，Cloudflare 使用 `/api/v1/`。静态资源切换域名即可。HTTP 验证值及缓存版本须按源地址和路径保存，备用站的较旧快照不应替换设备上更新且有效的数据。[路径对照](api.md) · [部署与响应验证](static-assets.md)
