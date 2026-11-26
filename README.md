# NEUQ One Data

NEUQ One 的非官方公共数据与静态资源服务。Go Collector 从学校教务系统采集空教室数据，生成包含教室编号、容量、设备分类、目标日期、空闲时段和实际采集时间的 JSON。

空教室默认覆盖今天及未来六天、每天六个课节组，以及工学馆、科技楼和人文楼。两个 Cloudflare Pages 项目共用本仓库，分别发布。

| 生产地址 | 内容 | 发布入口 |
| --- | --- | --- |
| `https://neuq-one-data.pages.dev` | 空教室快照与采集状态，迁移期间兼容旧客户端的地图和政策请求 | [`classrooms.yml`](.github/workflows/classrooms.yml) |
| `https://neuq-one-assets.pages.dev` | 校园地图、学校政策目录及 PDF、App 版本清单 | [`assets.yml`](.github/workflows/assets.yml) |

- [API 文档](docs/api.md)及 [JSON Schema](docs/api-v1.schema.json)
- [部署架构](docs/architecture.md)
- [Flutter 接入设计](docs/flutter-integration.md)
- [静态资源与校园地图](docs/static-assets.md)
- [协议依据与待验证项](docs/upstream.md)

## 架构与更新频率

**Cloudflare Workers Cron Triggers → GitHub REST API `workflow_dispatch` → GitHub Actions → Go Collector → Cloudflare Pages → Flutter 客户端**

Cloudflare Worker `neuq-one-scheduler` 用加密 Secret `GITHUB_TOKEN` 调用 GitHub REST API，触发默认分支上的 [`classrooms.yml`](.github/workflows/classrooms.yml)。GitHub Actions 构建并运行 Go Collector，校验快照后把兼容资源复制到 `public/assets/`，再通过 Wrangler 发布至 `neuq-one-data`。

[`assets.yml`](.github/workflows/assets.yml) 在 `main` 的静态资源、构建脚本、响应头或工作流配置变更后运行，也可手动触发。App 仓库发布正式 GitHub Release 后，通过独立工作流触发同一次静态构建和发布。静态构建只读取仓库文件和公开的 Release 元数据。[静态资源部署与版本更新](docs/static-assets.md)

Worker 的 Cron 表达式为 `5 * * * *`，按 UTC 调度。每次请求都带 `publish=true` 和 `bootstrap=false`。

| 计划触发时间 | `mode` | 采集范围 |
| --- | --- | --- |
| UTC 16:05（北京时间次日 00:05） | `week` | 北京时间当天及未来六天（默认 `NEUQ_DAYS=7`） |
| 其余每小时第 5 分钟 | `today` | 采集北京时间当天，未来日期从已发布快照按同日期补齐 |

表中的时间是计划触发时间。新快照在 Actions 完成采集、校验和部署后上线。

## 本地使用

安装 Go 1.27.4 或该系列更新的安全版本。当前工具链版本固定在 `go.mod`。依赖通过 `go.sum` 校验。[Go 官方发行记录](https://go.dev/doc/devel/release)

```sh
go test ./...
go build ./...
```

在仓库根目录用 Node.js 24 独立构建并测试静态资源。

```sh
node scripts/build-assets.mjs
node --test scripts/test-build-assets.mjs
```

实际采集前，请确认学校允许该账号自动查询、允许所选部署环境访问教务入口，并允许公开分发空教室数据。使用获授权的专用账号，通过进程环境注入凭据。程序直接读取环境变量，`.env.example` 提供配置清单。

在获授权的网络内，设置 `NEUQ_USERNAME` 和 `NEUQ_PASSWORD` 后执行：

```sh
go run ./cmd/collector -mode week -output public
go run ./cmd/collector -verify -output public
bash scripts/stage-assets.sh
```

`-mode today` 只查询当天，仍生成配置天数的快照，未来日期从历史快照按同日期补齐。`-previous` 接受 HTTPS 站点根地址或本地 `week.json` 文件。首次生成时可省略，本地重跑时请给 `-output` 一个尚未创建的目录。学校响应、Cookie 和认证表单只在进程内存中处理。日志记录查询日期、楼栋、时段、错误码和经过脱敏的诊断字段。并行运行受本地工作目录锁和同仓库工作流并发组限制。

## 配置

### Cloudflare Worker

| 配置项 | 值与用途 |
| --- | --- |
| Worker 名称 | `neuq-one-scheduler` |
| Cron Trigger | `5 * * * *`（UTC） |
| `GITHUB_TOKEN` | Worker 的加密 Secret。使用限定到目标仓库、具有 **Actions → Read and write** 权限的细粒度 GitHub PAT，由 Worker 通过 `env.GITHUB_TOKEN` 读取 |

### GitHub Actions Secrets

| Secret | 用途 |
| --- | --- |
| `NEUQ_USERNAME`、`NEUQ_PASSWORD` | 获授权的教务专用账号凭据 |
| `CLOUDFLARE_API_TOKEN` | 具有 **Account → Cloudflare Pages → Edit** 权限的部署令牌 |
| `CLOUDFLARE_ACCOUNT_ID` | Pages 项目所在的 Cloudflare 账户 ID |

### GitHub Actions Variables

采集器也接受下表中的采集参数作为本地进程环境变量。本地采集使用 `NEUQ_USERNAME` 和 `NEUQ_PASSWORD` 注入账号凭据。

| 环境变量 | 默认值与用途 |
| --- | --- |
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
| `NEUQ_SLOT_RETRIES` | `2`，每个楼栋时段因疑似临时的完整性异常而额外补偿的次数，可配 0 到 4，`0` 关闭补偿 |
| `NEUQ_TODAY_TTL` | `2h`，当天时段从实际采集起的有效期 |
| `NEUQ_FUTURE_TTL` | `26h`，未来时段从实际采集起的有效期 |
| `NEUQ_PUBLIC_ORIGIN` | Actions 默认从 `https://<项目名>.pages.dev` 恢复历史。以 Pages 实际生产地址为准 |
| `CLOUDFLARE_PROJECT` | `neuq-one-data`，空教室 Pages 项目名 |
| `CLOUDFLARE_ASSETS_PROJECT` | `neuq-one-assets`，静态资源 Pages 项目名，须使用独立项目 |

自动调度由 Worker 的 Cron Trigger 控制。旧变量 `NEUQ_COLLECTION_ENABLED` 属于迁移前的 Actions 定时配置，可从 Actions Variables 移除。

查询按日期和时段串行执行。每个时段按 `NEUQ_BUILDINGS` 的配置顺序逐栋查询，默认依次查询工学馆、科技楼、人文楼。首轮结束后，页数、记录总数或分页大小在页间变化，跨页重复或记录数不符的时段按原顺序逐轮补偿。默认两轮分别在等待 1 秒和 2 秒后开始，每次从第一页重新查完整个楼栋时段，沿用同一个请求预算、请求间隔和任务截止时间。补偿成功按实际采集时间计算有效期。

楼栋编号缺失、范围不符、分页上限及确定性的表格或分页元数据解析错误会直接保留失败状态。临时网络和服务器错误沿用请求层的指数退避、抖动和 `Retry-After`。认证失败、交互验证、访问限制、请求预算耗尽或任务取消会停止采集。部分失败时，同日期的历史数据可标为 `stale` 并保留原始采集时间。若整体失败、跨北京时间午夜或校验失败，保留上一版线上快照。

## 部署

静态站首次部署需创建独立 Direct Upload 项目，沿用本仓库的 Cloudflare Secrets。新版客户端上线前，先运行 **Deploy static assets** 并验证生产 URL。旧版客户端的地图和政策继续随空教室快照发布，确认兼容期结束后再移除旧工作流的静态复制步骤。[静态站和正式 Release 配置](docs/static-assets.md)

### 建立第一个生产快照

1. 在 Cloudflare 控制台的 **Workers & Pages** 创建 Direct Upload 项目。记录项目名和账户 ID。也可运行 `npx wrangler@4.168.0 pages project create neuq-one-data --production-branch main`。生产分支应与 GitHub 默认分支一致。默认 Pages 子域名即可使用。[Direct Upload 创建指南](https://developers.cloudflare.com/pages/get-started/direct-upload/)
2. 在该账户创建自定义 API Token，权限设为 **Account → Cloudflare Pages → Edit**，资源范围限定为该账户。[Cloudflare CI 权限配置](https://developers.cloudflare.com/pages/how-to/use-direct-upload-with-continuous-integration/)
3. 在 GitHub **Settings → Secrets and variables → Actions** 添加上表的四项 Secrets，通过 **Variables** 设置项目名、生产地址和获授权的访问方式。Pages 分配带后缀的子域名时，将 `NEUQ_PUBLIC_ORIGIN` 设为实际生产站点的 HTTPS 根地址。
4. 在 **Actions → Collect classrooms → Run workflow** 选择仓库默认分支。生产站尚无快照时勾选 `bootstrap`，跳过读取历史。可先用 `mode=today`、`publish=false` 检查采集和校验日志，再用 `mode=week`、`publish=true` 完成首次发布。首个生产快照上线后，后续运行使用 `bootstrap=false`。
5. 在生产域名分别请求 `/api/v1/classrooms/today.json`、`/api/v1/classrooms/week.json` 和 `/api/v1/status.json`。检查三份响应的 `generatedAt` 一致，`today.json` 的 `date` 为北京时间当天，各时段状态与房间列表匹配。

### 启用 Worker 调度

1. 在 Cloudflare **Workers & Pages** 创建或打开 `neuq-one-scheduler`。在 **Settings → Variables and Secrets** 中将 GitHub PAT 添加为名为 `GITHUB_TOKEN` 的 **Secret**。PAT 的仓库范围限定为 `LTCpond/neuq-one-data`，并授予 **Actions → Read and write**。 [Workers Secrets](https://developers.cloudflare.com/workers/configuration/secrets/) · [GitHub 工作流触发 API](https://docs.github.com/en/rest/actions/workflows#create-a-workflow-dispatch-event)
2. 部署带 `scheduled()` 处理器的 Worker。按计划触发时间 `controller.scheduledTime` 选择模式，UTC 16:05 传 `week`，其余时段传 `today`。向 `https://api.github.com/repos/LTCpond/neuq-one-data/actions/workflows/classrooms.yml/dispatches` 发送 JSON `POST` 请求，请求头使用 `Authorization: Bearer <GITHUB_TOKEN>`、`Content-Type: application/json`、`Accept: application/vnd.github+json` 和 `User-Agent: neuq-one-scheduler`。以下是每日整周任务的请求体，`ref` 使用默认分支 `main`。 [Scheduled Handler](https://developers.cloudflare.com/workers/runtime-apis/handlers/scheduled/) · [GitHub REST API 请求头](https://docs.github.com/en/rest/using-the-rest-api/getting-started-with-the-rest-api#headers)

   ```json
   {
     "ref": "main",
     "inputs": {
       "mode": "week",
       "publish": true,
       "bootstrap": false
     }
   }
   ```

3. 在 Worker 的 **Settings → Triggers → Cron Triggers** 添加 `5 * * * *`。首次触发后，在 Cron Events 检查执行结果，在 GitHub Actions 确认 `Collect classrooms` 收到 `workflow_dispatch` 并成功发布，再检查生产 API 的 `generatedAt`。触发器变更最长可能需要 15 分钟传播。 [Cron Triggers 配置与执行记录](https://developers.cloudflare.com/workers/configuration/cron-triggers/)

在 Worker 的 Cron Trigger 中调整自动采集频率，暂停时移除触发器。调整时间时同时核对 Worker 的 `week` 判定和北京时间换日。通过 `NEUQ_DAYS` 调整快照覆盖天数。

## 免费额度与运行限制

截至 2026-11-25，默认计划每天产生 24 次 Worker 调用。每次调用发送一次 GitHub API 请求。按 31 天计，每月最多安排 744 次自动发布，手动运行另计。

- **Cloudflare Workers Free** 每账户可配置 5 个 Cron Trigger，每天 100,000 次请求。每次 Cron 调用有 10 ms CPU 时间和 50 次外部子请求额度，等待网络响应的时间不计入 CPU 时间。 [Workers 平台限制](https://developers.cloudflare.com/workers/platform/limits/)
- **GitHub** 的公开仓库使用标准 GitHub 托管 Runner 时计算用量免费。私有仓库的计算和存储按账户计划计量。Worker 发出的请求受 GitHub REST API 限流约束。 [Actions 计费](https://docs.github.com/en/billing/concepts/product-billing/github-actions) · [REST API 限流](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- **Cloudflare Pages Free** 的每月 500 次构建额度适用于 Git 集成。此项目由 GitHub Actions 生成静态文件，经 Wrangler Direct Upload 发布。Wrangler 上传限制为 20,000 个文件、单文件 25 MiB。静态文件请求免费且不限次数。按账户实际限制和用量核查发布频率，遇到部署配额限制时可降低 Worker 触发频率，生产域名继续提供上一版快照。 [Pages 平台限制](https://developers.cloudflare.com/pages/platform/limits/) · [Direct Upload](https://developers.cloudflare.com/pages/get-started/direct-upload/) · [静态请求计费](https://developers.cloudflare.com/pages/functions/pricing/)

## 故障排查

| 现象 | 检查项 |
| --- | --- |
| 到点后 Worker 没有触发记录 | 检查 `neuq-one-scheduler` 的 Cron Trigger 和 Cron Events。按 UTC 核对 `5 * * * *`，为新建或修改的触发器留出传播时间 |
| Worker 已触发，GitHub Actions 没有对应运行 | 查看 Worker 日志中的 GitHub 响应状态。`401` 检查 `GITHUB_TOKEN` 绑定和令牌有效期。`403` 检查仓库授权和 Actions 写权限。`403` 或 `429` 也要检查限流响应头。`404` 检查令牌能否访问目标仓库及 `classrooms.yml` 是否位于默认分支。`422` 检查 `ref` 和输入值。收到 `2xx` 后到 Actions 查看排队和运行状态 |
| Actions 已启动但未发布 | 检查工作流使用默认分支，自动运行的输入为 `publish=true`、`bootstrap=false`，并查看采集与校验日志。部署步骤会核对快照日期与北京时间当天是否一致 |
| 认证失败、要求交互验证或学校拒绝访问 | 检查授权网络、账号状态和学校入口。若 WebVPN 要求设备校验或验证码，暂停 Worker 调度，向学校确认受支持的自动化接入方式 |
| 楼栋时段查询失败或长期部分可用 | 对照官方界面与完整性日志。日期、时段、分页元数据或楼栋选项变化时，用新的脱敏样本更新解析规则 |
| 历史快照读取失败 | 检查生产地址、HTTP 状态和 Schema。初次发布时 HTTP 404 表示无历史；其他读取错误会中止以保护线上旧数据 |
| Wrangler 部署失败 | 检查 Token 权限、账户 ID、项目类型、项目名、生产分支和账户配额。校验通过后可手动重跑一次 |
| 接口显示旧日期或未来日期未更新 | 依次检查 Worker 的触发记录、北京时间 00:05 请求的 `mode=week`、Actions 运行结果和 Pages 最后一次成功的生产部署。客户端按响应日期与各时段的 `expiresAt` 判断可用性 |

在 Actions 日志中按 `building`、`date`、`period` 定位失败查询，结合 `attempt`、`error_code`、`page` 及分页计数判断发生在哪次尝试和哪一页。`楼栋采集统计` 汇总各日期各楼栋的最终结果。`采集统计` 中的 `first_success`、`retry_attempts`、`retry_success`、`success`、`failed` 分别表示首轮成功时段、实际补偿尝试、补偿成功时段、最终成功时段和最终失败时段。`requests` 包含认证、重定向、查询及各类重试对学校发送的 HTTP 请求。`failures_by_code` 统计最终失败时段，任务提前停止时尚未查询的时段计入 `not_attempted`，停止原因见 `stop_code`。`attempt_failures_by_code` 包含后来补偿成功的时段曾发生的失败。

## 许可证

项目采用 MIT。依赖版权及许可见 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。参考仓库提供流程和协议线索，Go 实现独立编写。
