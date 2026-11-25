# 静态资源与校园地图

静态资源提交到 `assets/`，目录结构保留在 `public/assets/` 下。每次空教室采集、校验通过后，工作流把这些文件与生成的快照一起交给 Wrangler。定时任务和手动任务使用同一个发布流程。无图时仍可正常发布空教室快照。

## 地图来源与许可记录

客户端原入口为东北大学秦皇岛分校迎新网的「学校地图」页面。该页标注版权归东北大学秦皇岛分校迎新网，页面上未见明确的转载许可。计划发布的原始图片及授权状态待维护者确认。确认后在本节记录原图的具体链接、版权人、允许公开转载的许可依据，以及要求保留的署名。[东北大学秦皇岛分校迎新网 · 学校地图](https://yx.neuq.edu.cn/shgl/xxdt.htm)

第三方图片按图像自身的许可使用，仓库代码的 MIT 许可证仅覆盖代码。

## 上传和更新

1. 确认所选原图可公开转载，补齐上面的来源与许可记录。将原始高清 PNG 保存为 `assets/maps/campus-map.png`，保留原始分辨率，并在原尺寸检查建筑和道路文字。单文件上限为 25 MiB。客户端接受宽、高均不超过 8192 像素且总像素不超过 3200 万的地图。
2. 将图片及记录提交到默认分支。等待下一次定时成功部署，或在 GitHub Actions 手动运行 **Collect classrooms**，选择默认分支，设置 `mode=today`、`publish=true`、`bootstrap=false`。已有生产快照的项目可使用这组参数。
3. 部署成功后检查地图 URL 返回 `200`、`Content-Type: image/png` 和 `ETag`，用同一个 ETag 发送 `If-None-Match` 后应得到 `304`。替换地图文件并重新部署后，旧 ETag 对应的条件请求应得到新文件及新 ETag。[校园地图生产地址](https://neuq-one-data.pages.dev/assets/maps/campus-map.png)

`/assets/*` 使用 `Cache-Control: public, no-cache, no-transform`，允许缓存并在复用前重新验证。Cloudflare Pages 为静态文件提供 ETag。客户端先展示本地文件，同时检查服务器是否更新。[Cloudflare Pages 缓存和 ETag](https://developers.cloudflare.com/pages/configuration/serving-pages/#caching-and-performance)

仓库根目录执行 `bash scripts/test-stage-assets.sh` 可验证空目录、地图替换、重复发布和其他静态资源的复制行为。
