# 静态资源与双平台部署

静态资源提交到 `assets/`，构建后保留在 `.work/assets-public/assets/` 下。`assets.yml` 的 Azure 和 Cloudflare 发布任务分别下载同一份构建产物。空教室工作流的 Cloudflare 发布任务也会将校园地图、政策目录及其列出的 PDF 复制到旧站的 `public/assets/`，供已安装的旧客户端使用。

## 配置 Cloudflare 静态站

1. 在原 Cloudflare 账户中新建名为 `neuq-one-assets` 的 Pages Direct Upload 项目，生产分支设置为 `main`。可在控制台创建，也可在获授权的环境中运行 `npx wrangler@4.168.0 pages project create neuq-one-assets --production-branch main`。[Cloudflare Pages Direct Upload](https://developers.cloudflare.com/pages/get-started/direct-upload/)
2. 在本仓库 **Settings → Secrets and variables → Actions** 确认现有 `CLOUDFLARE_API_TOKEN` 和 `CLOUDFLARE_ACCOUNT_ID` 对该账户有效。使用同一账户下具有 Pages 编辑权限的 Token。`CLOUDFLARE_ASSETS_PROJECT` 默认是 `neuq-one-assets`，可在 **Variables** 中设为实际项目名。若实际生产域名不同，构建客户端时同时配置 `NEUQ_ONE_ASSETS_ORIGIN`。
3. 将 `assets.yml` 合入 `main` 后，在 **Actions → Deploy static assets → Run workflow** 选择 `main`，或提交 `assets/**`、构建脚本、响应头配置或静态资源工作流的变更。检查生产域名上的地图、政策目录、PDF 与版本清单。[生产接口路径](api.md#静态资源)

Cloudflare 仍通过 Wrangler 上传独立构建目录。静态资源和旧站兼容资源共用文件清单与 PDF 的 SHA-256 校验，仅允许公开地图、目录及目录列出的 PDF。独立静态站还包含生成的版本清单。`/assets/*` 使用 `Cache-Control: public, no-cache, no-transform`，Cloudflare Pages 提供 ETag。[Cloudflare Pages 缓存和 ETag](https://developers.cloudflare.com/pages/configuration/serving-pages/#caching-and-performance)

## Azure Static Web Apps 生产部署

为独立静态资源和空教室各使用一个 Azure Static Web Apps **Free** 应用。未来客户端可将 Azure 配为主源、Cloudflare 配为备用源，现有客户端沿用已发布版本的地址与路径。

1. 若已创建 Azure 静态资源测试应用，可直接复用。否则在 Azure 门户新建静态资源应用。另建一个空教室应用。两个应用的 **Plan type** 都选 **Free**，**Source** 都选 **Other**。在各自的 **Overview** 记下实际的 `*.azurestaticapps.net` 地址。两个应用都按纯静态站配置。[Azure 创建示例](https://learn.microsoft.com/en-us/azure/static-web-apps/gitlab#create-a-static-web-app)
2. 在各应用的 **Overview → Manage deployment token** 复制对应令牌。在 `LTCpond/neuq-one-data` 的 **Settings → Secrets and variables → Actions** 将静态资源令牌保存在 `AZURE_STATIC_WEB_APPS_API_TOKEN`，将空教室令牌保存在 `AZURE_DATA_SCHEDULE_API_TOKEN`。[Azure 部署令牌](https://learn.microsoft.com/en-us/azure/static-web-apps/deployment-token-management) · [GitHub 仓库 Secret](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets#creating-secrets-for-a-repository)
3. 等待旧的 Azure 测试部署结束，再合入双平台工作流。静态发布统一通过 [`assets.yml`](../.github/workflows/assets.yml)。在 **Actions → Deploy static assets → Run workflow** 选择 `main` 可刷新两站。请使用这个入口重试发布，避免从 Actions 历史重跑旧 `azure-assets.yml`。
4. 空教室沿用 [`classrooms.yml`](../.github/workflows/classrooms.yml)。首次发布 Azure 空教室站点时，可在 **Actions → Collect classrooms → Run workflow** 选择默认分支，设置 `mode=week`、`publish=true`、`bootstrap=false`，继续从既有 Cloudflare 站读取历史。只有两站都尚未发布过快照时才按首次采集流程设置 `bootstrap=true`。

Azure 静态资源任务在自己的部署副本生成首页、`staticwebapp.config.json` 并移除 `_headers`。Azure 空教室任务将三份 JSON 原样复制到 `/data/v1/`，避免与 Azure 的 `/api` 后端路由冲突。[Azure 跳过内置构建](https://learn.microsoft.com/en-us/azure/static-web-apps/build-configuration#skip-building-front-end-app) · [Azure API 路由](https://learn.microsoft.com/en-us/azure/static-web-apps/apis-overview#api-constraints)

## 发布状态与重试

每次运行分别显示 Azure 与 Cloudflare 发布任务的结果。构建或采集校验失败时，两个任务都跳过。发布失败时，对应站点保留最后成功版本。另一个平台可独立完成发布。

两个工作流各自串行运行。发布任务只下载本次尝试生成的产物，避免旧执行尝试覆盖新内容。失败后从默认分支重新手动触发整个工作流，以便重新读取 Release 或重新采集。空教室两个发布任务分别检查快照日期是否仍为北京时间当天。

Azure 上传失败时，在对应发布任务核对令牌是否来自预期应用、令牌是否有效，以及发布日志中的站点地址是否与门户一致。Azure 空教室路径应从 `/data/v1/` 开始。某个平台漏发时，两个站点的 `generatedAt` 或版本清单可以暂时不同。[API 路径对照](api.md) · [Cloudflare 发布故障排查](../README.md#故障排查)

## 验证生产响应

在四个站点分别用 GET 请求 [API 路径对照表](api.md) 中的资源。成功响应应是 HTTP `200`，并符合下列检查。空教室站点的三份 JSON 应有同一个 `generatedAt`，且同一次发布的两站快照文件应逐字节相同。采集仍在进行时，可用 Actions 的对应运行确认比较的是同一批。

| 资源 | Content-Type | 内容与缓存 |
| --- | --- | --- |
| 空教室和状态 JSON | `application/json`，可带 UTF-8 字符集 | `schemaVersion=1`，`Cache-Control: public, max-age=60, must-revalidate`，`Access-Control-Allow-Origin: *` 和 `X-Content-Type-Options: nosniff` |
| 政策目录和版本清单 JSON | `application/json`，可带 UTF-8 字符集 | 政策条目含 `sha256`，版本清单含 `release` 和 `prerelease` |
| 校园地图 PNG | `image/png` | 图片可打开，内容与发布产物相同 |
| 政策 PDF | `application/pdf` | PDF 可打开，`sha256sum` 与对应目录条目相同 |

静态资源使用 `Cache-Control: public, no-cache, no-transform`。下面以地图为例，替换域名及路径即可检查其他文件。

```sh
url=https://neuq-one-assets.pages.dev/assets/maps/campus-map.png
curl -fsS -D map.headers -o map.png "$url"
grep -iE '^(HTTP/|content-type:|cache-control:|etag:|last-modified:)' map.headers
# 复制当前响应的 ETag，保留其引号和可能存在的 W/ 前缀
etag='替换为响应中的 ETag'
curl -sS -D - -o /dev/null -H "If-None-Match: $etag" "$url"
```

文件未更新时，检查同一个 `If-None-Match` 是否返回 `304`。若仍返回 `200`，检查响应体是否与首次请求一致，并记录该站点的实际行为。只提供 `Last-Modified` 时，用原值发送 `If-Modified-Since`。更新资源后，用旧验证值确认收到 `200` 和新内容。验证值始终取自同一个域名和路径。以生产域名的响应为准，Azure 模拟器使用固定 ETag 且可能仍返回完整响应。在国内网络验证可达性、下载速度及条件响应后，再调整客户端数据源。[Azure 响应头配置](https://learn.microsoft.com/en-us/azure/static-web-apps/configuration#global-headers)

## Release 触发版本更新

1. 在 GitHub 创建仅限 `LTCpond/neuq-one-data`、具有 **Actions → Read and write** 的细粒度 PAT。在 `LTCpond/neuq-one` 的 Actions Secrets 中保存为 `NEUQ_ASSETS_DISPATCH_TOKEN`。[GitHub 工作流触发 API](https://docs.github.com/en/rest/actions/workflows#create-a-workflow-dispatch-event)
2. 在 App 仓库使用 `vMAJOR.MINOR.PATCH` 标签，上传已验签的 APK，再按 APK 的构建渠道发布公测或正式 Release。在 Release Notes 的 `## 本次更新` 下填写 3～5 条简短的中文纯文本列表。生成脚本提取该节摘要，保存最多五条、每条 160 个 Unicode 码点。没有摘要时生成空数组。
3. App 仓库的 **Update app version manifest** 会在两种渠道的发布、编辑、撤回或删除事件后触发 `assets.yml`。Data 每次从公开 Release 列表重新选择两个渠道中版本号最高且已有 APK 的非草稿发布。检查 **Deploy static assets** 下的两个部署任务，并核对两站的 `/assets/app/latest.json`。撤回后可重跑任一仓库的清单刷新工作流。[清单格式](api.md#静态资源) · [GitHub Release 列表 API](https://docs.github.com/en/rest/releases/releases#list-releases)

如由其他工作流发布 Release，使用能触发后续 Actions 的 GitHub App 令牌或 PAT，或在成功发布后显式 dispatch `assets.yml`。APK 留在 GitHub Releases。[使用 `GITHUB_TOKEN` 触发工作流](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication)

**Deploy static assets** 的构建步骤使用 GitHub Actions 内置 `GITHUB_TOKEN`，以 `contents: read` 权限读取公开 Release 列表。本地使用 `--github-release` 联网构建时，也可通过同名环境变量提供令牌。公开列表为空时两个渠道均为 `null`，读取失败时保留上一份部署。[GitHub Actions 令牌认证](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication)

遇到限流、网络超时或 GitHub 临时服务错误时，每页最多请求三次。限流重试遵守 `Retry-After` 和 `x-ratelimit-reset`，缺少明确等待时间时从 60 秒开始退避。单次需要等待超过 120 秒时结束本次构建，生产站保留上一版。权限类 `403` 会直接失败。排查时查看构建步骤的 GitHub 错误原因、请求 ID 和限流响应头。日志中的令牌会被遮蔽。[GitHub REST API 限流说明](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)

## 地图来源与许可记录

客户端原入口为东北大学秦皇岛分校迎新网的「学校地图」页面。该页标注版权归东北大学秦皇岛分校迎新网，页面上未见明确的转载许可。计划发布的原始图片及授权状态待维护者确认。确认后在本节记录原图的具体链接、版权人、允许公开转载的许可依据，以及要求保留的署名。[东北大学秦皇岛分校迎新网 · 学校地图](https://yx.neuq.edu.cn/shgl/xxdt.htm)

第三方图片按图像自身的许可使用，仓库代码的 MIT 许可证仅覆盖代码。

## 上传和更新

1. 确认所选原图可公开转载，补齐上面的来源与许可记录。将原始高清 PNG 保存为 `assets/maps/campus-map.png`，保留原始分辨率，并在原尺寸检查建筑和道路文字。单文件上限为 25 MiB。客户端接受宽、高均不超过 8192 像素且总像素不超过 3200 万的地图。
2. 将图片及记录提交到 `main`，并检查 **Deploy static assets** 的运行结果。更新学校政策时，将 PDF 与更新过 `sha256` 的 `assets/policies/index.json` 一并提交。
3. 部署成功后检查地图 URL 返回 `200`、`Content-Type: image/png` 和 `ETag`，用同一个 ETag 发送 `If-None-Match` 后应得到 `304`。替换地图文件并重新部署后，旧 ETag 对应的条件请求应得到新文件及新 ETag。[校园地图生产地址](https://neuq-one-assets.pages.dev/assets/maps/campus-map.png)

仓库根目录执行 `node scripts/build-assets.mjs` 可离线构建，默认输出 `.work/assets-public`，两个发布渠道均为 `null`。重跑时可指定新的输出目录，例如 `node scripts/build-assets.mjs .work/assets-recheck`。`node --test scripts/test-*.mjs` 验证静态构建、版本清单和双平台产物，`bash scripts/test-stage-assets.sh` 验证旧站兼容资源复制。
