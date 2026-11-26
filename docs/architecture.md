# 公共数据服务架构

两个生产站点从同一个 `neuq-one-data` 仓库部署。Flutter 各功能使用对应的 HTTPS 根地址。

| 数据 | 生成与发布 | 失败时的行为 |
| --- | --- | --- |
| 空教室快照和采集状态 | Worker `neuq-one-scheduler` 按既有计划触发 `classrooms.yml`。Go Collector 读取历史数据、采集并验证 `public/`，随后发布至 `neuq-one-data` | 采集或校验失败时保留上一份生产快照，客户端以目标日期和时段有效期判断是否可用 |
| 地图、政策原件与目录、App 版本清单 | `assets.yml` 从 `main` 构建 `.work/assets-public/`，生成 Release 清单并发布至 `neuq-one-assets` | 构建或校验失败时保留上一份静态站部署。地图和政策功能继续使用有效的设备缓存 |

两个工作流分别使用各自的生产并发组和输出目录。静态资源提交与 App 的 Release 更新只触发静态发布，无需教务账号或采集结果。每次静态部署重新读取 GitHub Release 列表，生成正式及公测两个渠道各自的版本和摘要。

迁移期中，`classrooms.yml` 继续在快照校验后复制 `assets/` 到旧站。新版客户端应在静态站完成首次部署后发布。待维护者确认已安装旧版客户端的兼容要求，再移除旧站的复制步骤。工作流配置和接口详见 [静态资源部署](static-assets.md) · [公共数据服务接口](api.md)。
