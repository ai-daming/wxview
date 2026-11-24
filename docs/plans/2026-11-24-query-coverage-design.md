# #7：查询读取范围与缓存新鲜度

依据用户已确认的原始数据层边界与 #7 实施授权；提交、推送和后台进程操作分别按已有授权执行。

## 选择与边界

- 选择在共享 query 层记录本次依赖，CLI 的既有 JSON envelope 和 WebAPI 的 meta 添加同一 `data_coverage`；原始 items、分页、表格/CSV/JSONL 以及既有数组式 JSON 契约保持兼容。与事后调用全量 cache status 相比，这能限定依赖并检测查询期间替换；与为全部 SQLite 服务改造共享事务句柄相比，采用只读前后观察更小，发生变化时明确 unknown，不承诺查询级一致快照。
- 每个数据查询惰性解析一次账号并复用。通过查询上下文观察实际 SQLite 只读打开路径，记录缓存首次读取前的文件身份和已有 mtime.json 条目，完成读取后比较文件身份、大小、mtime 及对应条目。索引相对缓存的状态仍由既有 execution 字段描述，缓存相对微信源的状态另行报告。
- 给既有 mtime.json 增加与发布时临时缓存绑定的 size/mtime，避免新缓存已发布而旧元数据尚未更新的窗口被误报为 fresh。签名在 rename 前取得。旧条目缺少该绑定时为 unknown；不会因此自动刷新。无需新状态库、后台任务或逐查询内容哈希。
- 单库状态 fresh/stale/missing/unknown；报告 db 相对路径、依赖角色、是否必需、已知原因、绑定缓存的成功采集时间及已知 WAL 存在性。聚合状态 partial（依赖缺失）/stale/unknown/fresh，变化中的缓存或索引优先 unknown。fresh 仅表示本次依赖的本地源签名在观察时匹配，不代表手机全量同步或完整截至时间。
- 必需库、显式刷新失败继续返回既有错误，不能降级为成功。保留最后有效缓存与元数据；随后读取旧缓存按其真实源签名呈现 stale/unknown，不新建失败历史账本。辅助联系人/媒体读取失败可报告缺口，但无关数据库不纳入。
- 只做依赖文件 stat、既有刷新元数据读取与 WAL 头观察，不新增默认全库扫描或密钥扫描。GET 不刷新，new-messages 预览不推进检查点。

## 验证

先写失败测试再实现：无匹配记录、元数据未知、依赖缺失、WAL-only 变化、缓存替换/发布窗口、索引 ready 但源变化、失败保留旧缓存。随后覆盖 messages/timeline/search/new-messages 及联系人/会话依赖，CLI/HTTP 真实入口语义一致，既有分页与非 envelope 格式不变。受影响 key/decrypt/query/webapi/CLI 测试、race/vet 和 Windows 构建；不运行本地全仓回归。实机只在已有授权范围和可用环境内进行，不能用交叉编译替代 Windows 验收。
