# NEUQ One Data

NEUQ One 的非官方公共数据采集工具。Go 命令按计划从学校教务系统采集空教室数据，生成静态 JSON，由 GitHub Actions 通过 Wrangler 发布到 Cloudflare Pages。Pages 提供 `.pages.dev` 地址。

第一阶段默认覆盖今天及未来六天，每天六个课节组、工学馆、科技楼和人文楼。默认每小时更新当天数据，并在北京时间每日 00:19 的任务中更新整个日期范围。API 包含教室编号、容量、设备分类、目标日期、空闲时段和每个时段的实际采集时间。发布目录只含三份 JSON 和 Pages 响应头配置。

- [API 文档](docs/api.md)及 [JSON Schema](docs/api-v1.schema.json)
- [Flutter 接入设计](docs/flutter-integration.md)
- [协议依据与待验证项](docs/upstream.md)

## 本地使用

安装 Go 1.27.4 或该系列更新的安全版本。当前工具链版本固定在 `go.mod`。依赖通过 `go.sum` 校验。[Go 官方发行记录](https://go.dev/doc/devel/release)

```sh
go test ./...
go build ./...
```

实际采集前，请确认学校允许该账号自动查询、允许所选部署环境访问教务入口，并允许公开分发空教室数据。使用获授权的专用账号，通过进程环境注入凭据。程序直接读取环境变量，`.env.example` 提供配置清单。

在获授权的网络内，设置 `NEUQ_USERNAME` 和 `NEUQ_PASSWORD` 后执行：

```sh
go run ./cmd/collector -mode week -output public
go run ./cmd/collector -verify -output public
```

`-mode today` 只查询当天，仍生成配置天数的快照，未来日期从历史快照按同日期补齐。`-previous` 接受 HTTPS 站点根地址或本地 `week.json` 文件。首次生成时可省略，本地重跑时请给 `-output` 一个尚未创建的目录。学校响应、Cookie 和认证表单只在进程内存中处理。日志只记录查询日期、楼栋、时段和归类后的失败原因。并行运行受本地工作目录锁和同仓库工作流并发组限制。

## 配置

| 环境变量 | 默认值与用途 |
| --- | --- |
| `NEUQ_USERNAME`、`NEUQ_PASSWORD` | 专用账号凭据，必填 |
| `NEUQ_USE_WEBVPN` | `false`，直连教务。设置为 `true` 时使用官方 WebVPN 的 CAS 路径 |
| `NEUQ_WEBVPN_EAMS_URL` | WebVPN 模式必填，从官方门户获授权的教务资源链接中获取以 `/eams/` 结尾的 HTTPS 基址 |
| `NEUQ_BUILDINGS` | `工学馆,科技楼,人文楼`，中文楼栋名以逗号分隔 |
| `NEUQ_DAYS` | `7`，包含当天，可配 1 到 14 天 |
| `NEUQ_REQUEST_DELAY` | `2s`，串行请求的间隔 |
| `NEUQ_REQUEST_TIMEOUT` | `20s`，每个 HTTP 请求含读取响应的超时 |
| `NEUQ_TASK_TIMEOUT` | `12m`，读取历史、认证和采集的总时限 |
| `NEUQ_MAX_REQUESTS` | `200`，限制认证、重定向、采集和重试的请求数 |
| `NEUQ_MAX_PAGES` | `4`，每个查询范围的页数上限 |
| `NEUQ_RETRIES` | `2`，每次临时请求失败后最多重试次数 |
| `NEUQ_TODAY_TTL` | `2h`，当天时段从实际采集起的有效期 |
| `NEUQ_FUTURE_TTL` | `26h`，未来时段从实际采集起的有效期 |
| `NEUQ_PUBLIC_ORIGIN` | Actions 默认从 `https://<项目名>.pages.dev` 恢复历史。以 Pages 实际生产地址为准 |
| `NEUQ_COLLECTION_ENABLED` | 设置为 `true` 后启用定时任务 |
| `CLOUDFLARE_PROJECT` | `neuq-one-data`，Cloudflare Pages 项目名 |

查询按日期和时段串行执行。每个时段按 `NEUQ_BUILDINGS` 的配置顺序逐栋查询，默认依次查询工学馆、科技楼、人文楼。楼栋编号缺失时，该楼栋时段保持不可用。部分失败时，同日期已有的有效历史数据可标为 `stale` 并保留原始采集时间。临时网络和服务器错误用指数退避、抖动和 `Retry-After` 控制重试。认证失败、交互验证和访问限制会终止任务。若整体失败、跨北京时间午夜或校验失败，保留上一版线上快照。

## Cloudflare Pages 与 GitHub Actions

1. 在 Cloudflare 控制台的 **Workers & Pages** 创建 Direct Upload 项目。记录项目名和账户 ID。也可运行 `npx wrangler@4.168.0 pages project create neuq-one-data --production-branch main`。生产分支应与 GitHub 默认分支一致。默认 Pages 子域名即可使用。[Direct Upload 创建指南](https://developers.cloudflare.com/pages/get-started/direct-upload/)
2. 在该账户创建自定义 API Token，权限设为 **Account → Cloudflare Pages → Edit**，资源范围限定为该账户。[Cloudflare CI 权限配置](https://developers.cloudflare.com/pages/how-to/use-direct-upload-with-continuous-integration/)
3. 在 GitHub **Settings → Secrets and variables → Actions** 添加 `NEUQ_USERNAME`、`NEUQ_PASSWORD`、`CLOUDFLARE_API_TOKEN`、`CLOUDFLARE_ACCOUNT_ID` 四项 Secrets。通过 **Variables** 设置项目名、生产地址和获授权的访问方式。Pages 分配带后缀的子域名时，应设置 `NEUQ_PUBLIC_ORIGIN` 为实际生产站点根地址。
4. 在默认分支打开 **Actions → Collect classrooms → Run workflow**。生产站尚无部署时勾选 `bootstrap`，跳过读取历史。首次可用 `mode=today` 且关闭 `publish`，查看查询和验证日志。确认学校返回的结果和访问条件后，选择 `mode=week` 并启用 `publish`，完成第一次发布。首个生产快照上线后，后续运行保留默认的 `bootstrap=false`。
5. 在生产域名分别 GET `/api/v1/classrooms/today.json`、`/api/v1/classrooms/week.json`、`/api/v1/status.json`。检查三个接口的 `generatedAt` 相同、当天日期为北京时间、状态字段与房间列表匹配。确认后将 `NEUQ_COLLECTION_ENABLED` 设为 `true`。
6. 修改 `.github/workflows/classrooms.yml` 的两个 UTC cron 可调整周期。修改每日整周任务的 cron 时，同时更新该工作流识别整周任务的 `MODE` 条件。用 `NEUQ_DAYS` 调整覆盖天数。

### 运行限制

截至 2026-11-25，Cloudflare Free 的 Pages 文档列出 Git 集成每月 500 次构建、每个站点 20,000 个文件、单文件 25 MiB。静态文件请求免费。Direct Upload 在 GitHub 上生成文件，Cloudflare 的 Direct Upload 文档列出文件与大小限制。每小时安排一次最多约 744 次月度部署，应在实际账户核查上传限制及用量。如果账户限制无法容纳默认频率，可调低定时频率。部署因配额失败时生产站保留原快照。见 [Pages 平台限制](https://developers.cloudflare.com/pages/platform/limits/)、[Direct Upload](https://developers.cloudflare.com/pages/get-started/direct-upload/)和 [静态请求计费](https://developers.cloudflare.com/pages/functions/pricing/)。

公开仓库使用标准 GitHub 托管 Runner 的计算用量免费。私有仓库按计划计算用量及存储额度。定时任务可能延迟或被丢弃，公开仓库连续 60 天无活动时定时工作流会自动停用，可在 Actions 中重新启用。见 [Actions 计费](https://docs.github.com/en/billing/concepts/product-billing/github-actions)和 [定时触发规则](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule)。

### 排查

| 现象 | 检查项 |
| --- | --- |
| 认证失败、要求交互验证或学校拒绝访问 | 检查授权网络、账号状态和学校入口。若 WebVPN 要求设备校验或验证码，停用任务，向学校确认受支持的自动化接入方式 |
| 楼栋时段查询失败或长期部分可用 | 对照官方界面与完整性日志。日期、时段、分页元数据或楼栋选项变化时，用新的脱敏样本更新解析规则 |
| 历史快照读取失败 | 检查生产地址、HTTP 状态和 Schema。初次发布时 HTTP 404 表示无历史；其他读取错误会中止以保护线上旧数据 |
| Wrangler 部署失败 | 检查 Token 权限、账户 ID、项目类型、项目名、生产分支和账户配额。校验通过后可手动重跑一次 |
| 接口显示旧日期 | 检查最后一次成功部署和 Actions 定时状态。客户端应按响应日期与时段 `expiresAt` 判断可用性 |

## 许可证

项目采用 MIT。依赖版权及许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。参考仓库提供流程和协议线索，Go 实现独立编写。
