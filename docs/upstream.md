# 协议依据与待验证项

| 来源 | 观察到的行为 |
| --- | --- |
| Flutter 的 `neuq_login_protocol.dart`、`neuq_web_client.dart` | 官方教务入口为 `/eams/homeExt.action`；统一身份认证经 `ids.neuq.edu.cn/authserver/login`；空教室使用表单页面和 AJAX 结果页面，复用 Cookie 会话。客户端会将已见的直连 HTTP 文档跳转升级为 HTTPS |
| Flutter 的查询实现和测试 | `free!search.action` 使用 POST。查询体包含日期、起止节次、楼栋编号、分页字段。全校查询请求 `pageSize=1000`。结果可为 `gridtable` 或 HTML 片段，分页可能通过 `pageInfo` 或文字总量表示 |
| Flutter 的领域模型和页面 | 按两节分组，区分缺失时段与空集合。全天空闲需要全部时段的交集，下一时段缺失时无法推出是否有课 |
| 参考 Rust 项目 | WebVPN 门户 CAS 后进入教务 CAS SSO，表单提供 `lt`、`execution`、AES 盐及提交字段。空教室全校 POST 同样使用 `pageSize=1000` 和空的楼栋参数。Rust 用表头读取网格并过滤异常场地，查询回显和分页校验的依据来自 Flutter |

参考资料：[NEUQ One](https://github.com/LTCpond/neuq-one)、[neuq-classroom-query](https://github.com/wy723161060/neuq-classroom-query)、[学校 WebVPN 使用说明](https://xxhb.neuq.edu.cn/info/1018/1434.htm)。

## 本次验证范围

模拟服务覆盖 CAS 表单、Cookie 复用、权限停止、会话过期与有限续期、教务查询及分页。实际学校环境中的下列行为仍待用获授权的账号和低频查询验证。

- 部署网络对直连入口和 WebVPN 的可达性、CAS 表单字段与回调路径、WebVPN 官方链接格式与非交互访问条件。参考程序包含模拟设备指纹的步骤，本实现仅使用学校正常发出的页面、表单和重定向。需要设备校验时任务停止。
- Flutter 的脱敏样本将带 `timeBegin` 的 `_paramstring` 放在结果网格的 `pageInfo` 之前，日期使用 `YYYY-MM-DD`。Go 优先核对该网格的回显和实际页码，回显缺少网格关联时仍检查带 `timeBegin` 的候选。出现冲突时记录字段、预期值和脱敏后的实际值。回显缺失时，校验依赖 POST 请求和响应的对应关系、楼栋列及分页元数据。
- 确认分页总数、短页和空结果的全部真实形态。首个非空页必须能确定页数，后续 AJAX 片段可用首个页面确定的计数校验。首个页面仅返回无分页线索的片段时会标记为无法确认。
- 2026-11-25 第 1–2 节全校查询的浏览器首屏有 100 行，分页元数据报告共 287 行。九行教学楼为空，均有零容量或明确场地类型可用于排除。首屏未发现合并单元格。Actions 记录的第 138 行超出这份首屏样本。见 [采集运行记录](https://github.com/LTCpond/neuq-one-data/actions/runs/45569565479)。
- 教学楼缺失时，可用显式 `rowspan`、已知楼栋的标准教室名，或已经校验的楼栋查询回显确认归属。已确认排除的行保留在原始分页计数里。其余归属不明的行阻止全校时段标为成功，保留按楼栋补查。诊断输出楼栋单元格形态、列数及字段类别。
- 确认科技楼和人文楼的编号在静态表单选项中可见。工学馆编号 `1` 来自现有客户端的验证记录。校区或楼栋名称变化时，使用脱敏样本更新规则。
- 确认发布所需的房间编号格式和设备分类。发布层只接受已验证的编号格式和已列出的设备分类，未分类的设备输出 `null`。新类别先用脱敏数据检查再增补。
