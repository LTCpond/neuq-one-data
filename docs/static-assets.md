# 静态资源部署

静态资源提交到 `assets/`，构建后保留在 `.work/assets-public/assets/` 下。迁移期间 `classrooms.yml` 仍将同一份地图和政策文件复制到旧站的 `public/assets/`，供已安装的旧客户端使用。旧站复制时排除 APK 和 AAB。

## 配置静态站

1. 在原 Cloudflare 账户中新建名为 `neuq-one-assets` 的 Pages Direct Upload 项目，生产分支设置为 `main`。可在控制台创建，也可在获授权的环境中运行 `npx wrangler@4.168.0 pages project create neuq-one-assets --production-branch main`。[Cloudflare Pages Direct Upload](https://developers.cloudflare.com/pages/get-started/direct-upload/)
2. 在本仓库 **Settings → Secrets and variables → Actions** 确认现有 `CLOUDFLARE_API_TOKEN` 和 `CLOUDFLARE_ACCOUNT_ID` 对该账户有效。使用同一账户下具有 Pages 编辑权限的 Token。`CLOUDFLARE_ASSETS_PROJECT` 默认是 `neuq-one-assets`，可在 **Variables** 中设为实际项目名。若实际生产域名不同，构建客户端时同时配置 `NEUQ_ONE_ASSETS_ORIGIN`。
3. 将 `assets.yml` 合入 `main` 后，在 **Actions → Deploy static assets → Run workflow** 选择 `main`，或提交 `assets/**`、构建脚本、响应头配置或静态资源工作流的变更。检查生产域名上的地图、政策目录、PDF 与版本清单。[生产接口路径](api.md#静态资源)

静态站生产发布通过 Wrangler 上传独立构建目录。构建验证政策目录和 PDF 的 SHA-256，并拒绝 APK、AAB、超限文件和符号链接。`/assets/*` 保留 `Cache-Control: public, no-cache, no-transform`，Cloudflare Pages 提供 ETag。[Cloudflare Pages 缓存和 ETag](https://developers.cloudflare.com/pages/configuration/serving-pages/#caching-and-performance)

## 正式 Release 触发版本更新

1. 在 GitHub 创建仅限 `LTCpond/neuq-one-data`、具有 **Actions → Read and write** 的细粒度 PAT。在 `LTCpond/neuq-one` 的 Actions Secrets 中保存为 `NEUQ_ASSETS_DISPATCH_TOKEN`。[GitHub 工作流触发 API](https://docs.github.com/en/rest/actions/workflows#create-a-workflow-dispatch-event)
2. 在 App 仓库准备使用 `vMAJOR.MINOR.PATCH` 标签的 Release 草稿，先上传已签名 APK，再发布为正式版本并标记为 Latest。App 仓库的 **Update app version manifest** 仅在正式 Release 发布后触发静态部署。预发布不会触发。
3. 在 Data 仓库检查 **Deploy static assets** 执行结果，并验证 `/assets/app/latest.json` 中的 `version` 与 Release 标签一致。手动重跑静态工作流会重新读取 GitHub 当时的最新正式 Release。尚无正式版本时清单返回 `release: null`。[GitHub 最新 Release API](https://docs.github.com/en/rest/releases/releases#get-the-latest-release)

如由其他工作流发布 Release，使用能触发后续 Actions 的 GitHub App 令牌或 PAT，或在成功发布后显式 dispatch `assets.yml`。APK 留在 GitHub Releases。[使用 `GITHUB_TOKEN` 触发工作流](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication)

**Deploy static assets** 的构建步骤使用 GitHub Actions 内置 `GITHUB_TOKEN`，以 `contents: read` 权限读取公开的最新正式 Release。GitHub Actions 会自动提供该令牌。本地使用 `--github-release` 联网构建时，也可通过同名环境变量提供令牌。响应为 `404` 时生成 `release: null`。[GitHub Actions 令牌认证](https://docs.github.com/en/actions/security-for-github-actions/security-guides/automatic-token-authentication)

遇到限流、网络超时或 GitHub 临时服务错误时，构建最多发出三次请求。限流重试遵守 `Retry-After` 和 `x-ratelimit-reset`，缺少明确等待时间时从 60 秒开始退避。单次需要等待超过 120 秒时结束本次构建，生产站保留上一版。权限类 `403` 会直接失败。排查时查看构建步骤的 GitHub 错误原因、请求 ID 和限流响应头。日志中的令牌会被遮蔽。[GitHub REST API 限流说明](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)

## 地图来源与许可记录

客户端原入口为东北大学秦皇岛分校迎新网的「学校地图」页面。该页标注版权归东北大学秦皇岛分校迎新网，页面上未见明确的转载许可。计划发布的原始图片及授权状态待维护者确认。确认后在本节记录原图的具体链接、版权人、允许公开转载的许可依据，以及要求保留的署名。[东北大学秦皇岛分校迎新网 · 学校地图](https://yx.neuq.edu.cn/shgl/xxdt.htm)

第三方图片按图像自身的许可使用，仓库代码的 MIT 许可证仅覆盖代码。

## 上传和更新

1. 确认所选原图可公开转载，补齐上面的来源与许可记录。将原始高清 PNG 保存为 `assets/maps/campus-map.png`，保留原始分辨率，并在原尺寸检查建筑和道路文字。单文件上限为 25 MiB。客户端接受宽、高均不超过 8192 像素且总像素不超过 3200 万的地图。
2. 将图片及记录提交到 `main`，并检查 **Deploy static assets** 的运行结果。更新学校政策时，将 PDF 与更新过 `sha256` 的 `assets/policies/index.json` 一并提交。
3. 部署成功后检查地图 URL 返回 `200`、`Content-Type: image/png` 和 `ETag`，用同一个 ETag 发送 `If-None-Match` 后应得到 `304`。替换地图文件并重新部署后，旧 ETag 对应的条件请求应得到新文件及新 ETag。[校园地图生产地址](https://neuq-one-assets.pages.dev/assets/maps/campus-map.png)

仓库根目录执行 `node scripts/build-assets.mjs` 可离线构建，默认输出 `.work/assets-public`，版本清单先使用 `release: null`。重跑时可指定新的输出目录，例如 `node scripts/build-assets.mjs .work/assets-recheck`。`node --test scripts/test-build-assets.mjs` 验证独立构建、文件完整性及正式版本清单，`bash scripts/test-stage-assets.sh` 验证迁移期间的旧站复制行为。
