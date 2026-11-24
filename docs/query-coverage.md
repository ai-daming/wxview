# 查询的数据覆盖与新鲜度

源码中的消息 JSON envelope 和 WebAPI 数据响应新增 `meta.data_coverage`。它描述本次查询使用或尝试使用的本地数据库缓存，供 AI、脚本及上层应用判断读取状态；不增加业务判断。

## 输出

以下为合成账号的消息查询示例，省略与本节无关的既有 meta 字段；`items` 仍使用原消息 schema：

```json
{
  "meta": {
    "schema_version": 1,
    "query_mode": "scan",
    "index_used": false,
    "data_coverage": {
      "scope": "local_synced_data",
      "account": "synthetic",
      "state": "fresh",
      "observed_at": "2026-11-24T02:00:01Z",
      "sources": [
        {"db":"contact/contact.db","state":"fresh","captured_at":"2026-11-24T02:00:00Z","source_wal_present":false,"wal_in_snapshot":false,"role":"contacts","required":false},
        {"db":"message/message_0.db","state":"fresh","captured_at":"2026-11-24T02:00:00Z","source_wal_present":false,"wal_in_snapshot":false,"role":"messages","required":true}
      ]
    }
  },
  "items": []
}
```

这个空结果表示：本次读取的缓存中没有匹配记录，且观察时其源签名仍匹配。它不证明手机上没有记录，也不证明微信已完整同步。

`scope=local_synced_data` 明确限于这台电脑上的本地数据。`account` 是本次解析并复用的账号。`observed_at` 是读取状态汇总完成的时间，各源的观察不是同一时刻。`captured_at` 是与本次读取缓存版本绑定的最后成功采集/刷新时间，**不是数据完整截至时间**；无可靠绑定时省略。

每个 source 的 `db` 是账号下的相对数据库路径，不包含磁盘绝对路径或密钥。`required` 表示依赖是否必需；联系人用于按名称筛选时必需，用于可选聊天名称补充时可选。媒体库仅在相应媒体查询实际尝试读取时出现；无关收藏、朋友圈或未使用媒体库不纳入。媒体文件是否找到、是否成功解码仍以消息的 `content_detail.media_status` 和 reason 为准，fresh 不保证每个媒体可用。

## 状态与缺口

| source.state | 含义 |
| --- | --- |
| `fresh` | 缓存版本绑定有效，观察到的本地源主库/WAL 签名与采集记录匹配 |
| `stale` | 缓存版本已知，但本地主库或 WAL 签名已变化 |
| `missing` | 本次尝试使用的缓存不存在；必需缓存沿用既有错误响应，不会伪装成成功 |
| `unknown` | 元数据缺失/不可读、旧条目没有版本绑定、源不可确认、可选读取失败，或查询期间缓存/元数据发生变化 |

聚合 `data_coverage.state` 通常按 `partial`（存在 missing）、`stale`、`unknown`、`fresh` 的顺序汇总；所有单库状态仍完整保留。查询期间缓存或已使用索引变化时，聚合优先为 `unknown`，并给出 `reason`。没有观察到依赖时也为 unknown。

例如，仅 WAL 改变、主库未变时，既有索引仍可能对旧缓存有效：

```json
{
  "query_mode":"index",
  "index_used":true,
  "index_status":"ready",
  "data_coverage":{
    "scope":"local_synced_data",
    "account":"synthetic",
    "state":"stale",
    "observed_at":"2026-11-24T02:01:00Z",
    "sources":[
      {"db":"message/message_0.db","state":"stale","reason":"source_changed","captured_at":"2026-11-24T02:00:00Z","source_wal_present":true,"wal_in_snapshot":false,"role":"messages","required":true}
    ]
  }
}
```

示例省略了其他依赖。`index_status`/`index_used` 只描述索引相对缓存的可用性；`data_coverage` 另行说明缓存相对微信源数据的状态。

查询期间缓存被替换时，该 source 返回 `state=unknown, reason=cache_changed_during_query`，省略无法绑定的 captured_at；聚合 reason 同样指出变化。元数据条目变化为 `snapshot_metadata_changed_during_query`，无版本绑定为 `cache_revision_unbound`，元数据不可得为 `snapshot_metadata_unavailable`，源不可确认或已移除为 `source_unavailable`。已使用索引的主库/WAL 变化为聚合 `index_changed_during_query`。已知可选读取错误为 `cache_read_failed`，依赖路径解析失败为 `cache_path_unavailable`。

## 兼容性与错误

- CLI `messages`、`timeline`、`search`、`new-messages` 的 JSON envelope 与 HTTP 复用同一结构和状态语义，schema_version 仍为 1，属于新增 meta 字段。
- `jsonl`、`csv`、`table` 保持 item-only；原本直接返回数组/对象的其他 CLI JSON 保持契约。WebAPI 已有 envelope 的其他数据查询也采用相同记录机制。
- 必需库缺失或显式刷新失败继续返回原错误，不为给出 coverage 而沿用旧缓存并报告成功。解密、校验或稳定性检查失败时保留旧缓存与有效刷新元数据。若缓存已发布但刷新元数据保存失败，同样返回错误，该文件没有可靠绑定时报告 unknown。之后选择不刷新而读取旧缓存，会按实际源状态报告 stale/unknown；本项不新建刷新失败历史账本。
- GET 的 `refresh=true` 仍被忽略。`GET /v1/new-messages` 不推进检查点；POST poll 和 CLI new-messages 的既有推进语义不变。元数据读取不触发取钥、缓存初始化或隐式刷新。
- 旧 mtime.json 条目缺少 cache_size/cache_mtime_ns 时返回 unknown。下一次**显式或原有维护流程的刷新**会生成绑定；观察本身不会升级缓存或重新取钥。

## 实现边界

在共享查询上下文观察实际只读 SQLite 打开路径，并记录已声明的必需缓存及可选缺失依赖。读取前后比较缓存文件身份、大小、mtime 和对应刷新条目，复用现有 mtime.json 与主库/WAL 签名；元数据按文件版本复用，不为每个依赖重复解析未变化文件。发布时从临时缓存取得 size/mtime，再原子替换并保存绑定，避免事后 stat 到另一发布者的文件。

这不是 SQL 快照事务，也不是完整性扫描：不计算每次查询的全文件哈希，不增加全库内容扫描、后台任务或高频账号探测；无法观察到的同尺寸、同 mtime 原地改写不在签名保证内。正常 Wxview 缓存发布使用原子替换，查询期间可观察的替换会明确报告 unknown。多个库分别观察，分页之间也可能刷新，不能据此承诺跨库或跨分页一致快照。

## 验证

合成 SQLite/WAL 数据覆盖：正常空结果、无关辅助库不影响、WAL-only 源变化、旧元数据 unknown 与显式升级、可选联系人/媒体缺失、必需库失败、旧缓存/元数据保留、索引 ready 而源 stale、查询期间缓存或索引变化、源无法确认，以及只读查询不初始化状态。测试通过四个 CLI JSON 入口与对应 HTTP 入口核对语义，并保留原分页、item 格式及增量预览检查点。

本轮运行受影响模块测试、相关 CLI 测试、race、macOS/Windows vet 与双平台构建；未运行本地全仓回归。Windows 仅交叉编译，未做 Windows 微信实机验收。

本机只读回读（2026-11-24，macOS ARM64）：授权测试会话返回 1 条消息，`data_coverage.scope=local_synced_data`，35 个本次依赖因旧条目没有版本绑定而明确报告 `unknown/cache_revision_unbound`；查询前后刷新元数据 SHA-256 一致。该验证没有刷新缓存或重新取钥，没有保存聊天正文。WAL-only 的 fresh→stale、刷新后恢复 fresh 与四种入口的一致性由合成真实 SQLite/WAL 链路验证。
