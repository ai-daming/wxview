# Wxview

本地微信数据查询 CLI，面向 AI Agent、脚本和个人数据管理。

当前版本：[v0.4.0](https://github.com/ai-daming/wxview/releases/tag/v0.4.0)。新增 macOS Apple Silicon 临时副本取钥，已在微信 **4.1.13** 上完成实机验收。

联系人 · 会话 · 聊天记录 · 搜索 · 时间线 · 媒体 · 收藏 · 公众号 · 朋友圈

Wxview 和 WeChat CLI / wx-cli 属于同一类工具：从你自己电脑上的微信数据里读取信息，
把联系人、群、会话、消息、媒体、收藏、公众号文章和朋友圈数据变成可查询、可导出、
可交给 AI 使用的本地数据源。

Wxview 的重点是清楚的本地缓存边界和稳定的机器输出：`init` 负责一次性准备 key，
`daemon start` 负责持续解密缓存和维护消息索引，查询命令和 `api start` / `api serve` 直接读取
本地 cache / index。

## 核心特点

- **本地优先**：微信数据、数据库 key、解密缓存和消息索引都保存在本机 `~/.wxview/`。
- **常驻维护**：`wxview daemon start` 持续刷新解密缓存，并后台生成/更新消息索引。
- **AI 友好**：支持 `json`、`jsonl`、`csv` 和 `table` 输出；`messages` / `timeline` 的 JSON 输出带 `meta.next_args`，方便可靠分页。
- **身份清楚**：`wxview me` 和 WebAPI `GET /v1/me` 会返回当前账号身份，方便区分多个本地实例。
- **跨会话读取**：既能按明确 `username` 查询单个聊天，也能按联系人筛选条件合并成全局时间线。
- **本地 WebAPI**：`wxview api start` 提供 HTTP 数据接口，默认只监听本机；显式 `--addr` 可监听局域网地址。GET 查询只读，POST 用于本地 cache/new-messages action；`api serve` 用于前台调试。
- **媒体可用**：图片、视频、文件和语音在支持缓存可用时会自动解析成本地可打开路径；语音会内置解码为 WAV，不要求系统安装 ffmpeg。
- **可删除索引**：消息索引是派生加速层，可以删除重建，不改变源微信库和解密缓存。

## 支持范围

当前支持：

- macOS 微信 4.x 数据读取；v0.4.0 新取钥方式实测为 Apple Silicon / 微信 4.1.13，其他 4.1.x 版本尚未验证。
- Windows 微信 4.x 既有扫描/读取能力（`xwechat_files\<account>\db_storage` 数据布局）；本次不承诺 Windows 新版取钥支持。
- `contact/contact.db`、`session/session.db`、`message/message_*.db`
- 可选数据面：`message/biz_message_*.db`、`message/media_*.db`、`message/message_resource.db`、`message/message_revoke.db`、`head_image/head_image.db`、`favorite/favorite.db`、`sns/sns.db`

当前不支持：

- Linux
- macOS 微信 3.x
- WAL patch
- WebAPI 身份认证及微信业务写入（如发送消息）；显式设置 `--addr` 可开放局域网读取
- 语音转文字

## 快速开始

### 1. 下载或构建

无需 Go 开发环境可直接下载 [v0.4.0 发布包](https://github.com/ai-daming/wxview/releases/tag/v0.4.0)：

| 平台 | 下载 | 取钥方式 |
| --- | --- | --- |
| macOS Apple Silicon | [ARM64 tar.gz](https://github.com/ai-daming/wxview/releases/download/v0.4.0/wxview_0.4.0_darwin_arm64.tar.gz) | 旧 scan 或新 `--key-source lldb` |
| Windows x64 | [AMD64 zip](https://github.com/ai-daming/wxview/releases/download/v0.4.0/wxview_0.4.0_windows_amd64.zip) | 旧 scan；不支持 LLDB |

[SHA256SUMS](https://github.com/ai-daming/wxview/releases/download/v0.4.0/SHA256SUMS) 可用于核对下载文件。解压后进入对应目录，运行 `./wxview version`（Windows：`.\wxview.exe version`），应输出 `0.4.0`。
下载包直接使用 `./wxview` / `.\wxview.exe`；下面源码构建示例中的 `./bin/wxview` / `.\bin\wxview.exe` 请替换为解压后的路径。

已有有效密钥和缓存可直接继续查询，不必因为升级再次取钥。首次使用新版 macOS 取钥请先阅读 [初始化与恢复指南](docs/lldb-setup.md)；可能需要重新扫码登录。

从源码构建（Go 1.25 或以上）：

```sh
task build
```

构建产物：

```sh
./bin/wxview
```

确认版本：

```sh
./bin/wxview version
```

如果没有安装 `task`，可以直接用 Go 构建：

```sh
GOCACHE=$(pwd)/.cache/go-build go build -o ./bin/wxview ./cmd/wxview
```

Windows 构建产物是：

```powershell
.\bin\wxview.exe
```

### 2. 初始化账号和 key

原有扫描方式（微信保持运行）：

```sh
sudo ./bin/wxview init
```

Apple Silicon 上还可显式使用临时副本取钥（需要 Apple Command Line Tools；不使用 sudo）：

```sh
# 先保存草稿并从微信菜单正常退出
./bin/wxview init --key-source lldb
# 存在多个本地账号时，明确指定目标账号
./bin/wxview init --key-source lldb --db-root /absolute/path/to/account/db_storage
```

按提示在临时微信窗口登录指定账号、完成手机确认。最多收集 300 秒；成功或取消后会关闭本轮副本，
请自行打开原版 `/Applications/WeChat.app`。原版签名不变。临时副本仍访问真实微信数据，微信自身可能
更新登录状态和数据库。`--key-source lldb` 总会进行捕获；日常查询不重复取钥。

本次在 macOS 27.0.1 / 原生 ARM64 / 微信 4.1.13 上完成取钥、必需库解密、查询与媒体抽样验收，
正式入口与完整业务验收状态见 [升级 TODO](docs/plans/wechat-new-version-upgrade-todo.md)。其他版本、
Intel/Rosetta 及 Windows 的新捕获路线不在本次支持承诺内。

Windows 需要在管理员终端运行：

```powershell
.\bin\wxview.exe init
```

`init` 会检测当前微信账号目录，提取并验证支持数据库的 SQLCipher raw key，然后保存到：

```sh
~/.wxview/cache/<account>/keys.json
```

`init` 通常只需要运行一次。后续只有在换账号、微信数据库 salt 变化、缺少新数据面的 key，
或提示 key 缺失时才需要重新运行。

### 3. 启动 daemon

这是日常使用的关键步骤：

```sh
./bin/wxview daemon start
```

`daemon start` 会在后台持续做三件事：

1. 使用 `init` 准备好的 key 刷新本地解密缓存。
2. 监听联系人、会话、消息、媒体、头像、收藏和 SNS 数据库变化。
3. 后台维护 `~/.wxview/cache/<account>/index/messages.db` 消息索引。

六组 watcher 共享账号/数据库路径快照：账号探测间隔 20 秒，消息分片目录检查间隔
5 秒，已知文件仍每秒检查。账号切换及新增分片不是一秒内保证；刷新还需等待防抖和实际处理。
macOS 活跃账号探测失败时暂停本轮刷新，20 秒后重试，不回退到其他账号的 mtime。
各刷新入口串行写缓存；耗时 watcher 回调期间的新触发合并为至多一次待处理回调。
更新二进制不会热更新已运行的 daemon，需以后重启服务才会应用这些后台改动。

确认状态：

```sh
./bin/wxview daemon status
./bin/wxview cache status --format table
./bin/wxview index status
```

停止后台服务：

```sh
./bin/wxview daemon stop
```

### 4. 查询数据

先看最近会话：

```sh
./bin/wxview sessions --limit 20 --format table
```

找联系人或群：

```sh
./bin/wxview me --format json
./bin/wxview contacts --kind chatroom --query AI --limit 20 --format json
./bin/wxview contacts --kind friend --query 张三 --limit 20 --format json
```

启动本机 WebAPI：

```sh
./bin/wxview api start --addr 127.0.0.1:38765
./bin/wxview api status
curl http://127.0.0.1:38765/v1/me
curl 'http://127.0.0.1:38765/v1/contacts?kind=chatroom&limit=5'
./bin/wxview api stop
```

读取某个会话的聊天记录：

```sh
./bin/wxview messages --username wxid_xxx --date today --limit 100 --format json
./bin/wxview messages --username 123@chatroom --start "2026-05-01" --end "2026-05-19" --format json
```

读取一批会话的全局时间线：

```sh
./bin/wxview timeline --kind chatroom --query AI --date today --limit 200 --format json
```

搜索消息正文：

```sh
./bin/wxview search --query "合同" --kind chatroom --date today --format json
```

## 启动顺序

正常使用顺序是：

```sh
task build
sudo ./bin/wxview init
./bin/wxview daemon start
./bin/wxview daemon status
./bin/wxview sessions --limit 20 --format table
./bin/wxview messages --username wxid_xxx --date today --format json
```

各步骤职责：

| 步骤 | 命令 | 职责 |
| --- | --- | --- |
| 构建 | `task build` | 生成 `./bin/wxview` |
| 初始化 | `wxview init` | 找账号、提取/验证 DB key、写入 `keys.json` |
| 常驻维护 | `wxview daemon start` | 持续解密缓存，维护 message index |
| 状态检查 | `daemon status` / `cache status` / `index status` | 看 daemon、cache、index 是否健康 |
| 查询 | `sessions` / `contacts` / `messages` / `timeline` / `search` | 直接读取本地 cache / index |
| WebAPI | `wxview api start` | HTTP 数据接口，默认只监听本机 |

不要把 `init` 当成常驻服务。`init` 是一次性准备动作；`daemon start` 才是让 Wxview
在日常使用中持续接近实时的入口。

查询命令不会把 daemon 当查询服务。daemon 只负责后台刷新缓存和索引；`contacts`、
`sessions`、`messages`、`timeline`、`search` 等命令始终直接读取本地解密 cache
或派生 index。

实现上，CLI flags 和 HTTP params 只是不同入口；contacts、sessions、messages、
timeline、search、favorites、articles、sns、new-messages 和 cache refresh 的查询编排
复用同一个 `internal/query` 层。CLI 负责 table/json/jsonl/csv 输出，WebAPI 负责 HTTP
envelope 和错误响应。

WebAPI 也不复用 daemon socket。`wxview api start` 是独立 HTTP 后台入口，默认只监听
`127.0.0.1:38765`。如果需要给局域网内其他设备访问，可以显式传入监听地址，例如
`wxview api start --addr 0.0.0.0:38765` 或 `wxview api start --addr 192.168.1.10:38765`。
非本机监听没有内置认证，能访问该端口的客户端就能读取本地 WebAPI 数据。业务接口会返回
账号和 server 标识，方便确认当前实例读取的是哪个账号。
V1 只支持一个 WebAPI 实例；无论已有实例是 `api start` 还是 `api serve` 启动的，再执行
`wxview api start` 或 `wxview api serve --addr 其他端口` 都会报错。`wxview api serve`
保留为前台调试模式，运行时也会登记为单实例，退出时清理状态。

### WebAPI 查询接口

HTTP 接口固定返回 JSON。业务查询返回 `{meta, items}`，`meta` 里包含轻量账号身份和
`server_id`，用于确认当前请求读的是哪个微信账号和哪个本地 API 实例。`GET` 查询接口只解析
声明过的参数，未声明参数会被忽略，包括 `refresh=true`；刷新 cache 使用统一 action
`POST /v1/cache/refresh`。

消息类接口会在 `content_detail.path` / `thumbnail_path` 存在时追加 `media_url` /
`thumbnail_url`，也会处理引用消息的 `refer_path` / `refer_thumbnail_path`、合并聊天记录
`record_items_json` 内 item 的媒体路径，以及名片头像 `avatar_path`。媒体接口是内部辅助
接口，只服务当前账号微信数据目录和 `~/.wxview/cache/<account>` 下的绝对路径。

消息 `content_detail` 会尽量结构化常见类型：图片、视频、文件、语音、表情、小程序、链接/文章、
视频号/直播、引用、合并聊天记录、名片、位置、系统事件、转账和红包。图片/视频/文件/语音/封面/
表情会尽量解析本地媒体路径；找不到媒体时保留 `media_status=not_found` 和 `media_reason`，
查询本身不失败。输出不会复制 CDN AES key、fileuploadtoken、cdnthumbkey 等敏感 key。

V1 不做异步 job。宽查询和本地媒体读取仍是同步 HTTP 请求，服务端读写超时设置为 30 分钟。

| 接口 | 参数 |
| --- | --- |
| `GET /health` | health、版本、启动时间 |
| `GET /v1/me` | `detail=paths` 可选 |
| `GET /v1/media` | `path`，读取当前账号数据目录或 cache 内的媒体文件 |
| `GET /v1/contacts` | `kind`、`query`、`username`、`limit`、`offset`、`sort` |
| `GET /v1/contacts/detail` | `username` |
| `GET /v1/members` | `username` 或 `query` |
| `GET /v1/sessions` | `kind`、`query`、`limit`、`offset` |
| `GET /v1/unread` | `kind`、`query`、`limit`、`offset` |
| `GET /v1/messages` | `username`、`date`、`start`、`end`、`order`、`cursor`、`after_seq`、`limit`、`offset`、`source`、`no_index` |
| `GET /v1/search` | `query`、`kind`、`chat_query`、`username`、`date`、`start`、`end`、`limit`、`offset`、`source` |
| `GET /v1/timeline` | `username` 或 `kind/query`，再加 `date` 或 `start/end`、`limit`、`cursor`、`source`、`no_index` |
| `GET /v1/new-messages` | `limit`、`source`；只预览，不推进 checkpoint |
| `GET /v1/favorites` | `type`、`query`、`limit`、`offset` |
| `GET /v1/articles/accounts` | `query` |
| `GET /v1/articles` | `username`、`query`、`date`、`start`、`end`、`limit`、`offset` |
| `GET /v1/sns/feed` | `username`、`query`、`date`、`start`、`end`、`limit`、`offset` |
| `GET /v1/sns/search` | `query`、`username`、`date`、`start`、`end`、`limit`、`offset` |
| `GET /v1/sns/notifications` | `date`、`start`、`end`、`limit`、`offset`、`include_read` |

状态改变类接口：

| 接口 | 用途 |
| --- | --- |
| `POST /v1/cache/refresh` | 刷新当前账号 cache；可选 `scope=all|contacts|sessions|messages|favorites|articles|sns` |
| `POST /v1/new-messages/poll` | 返回新增消息并推进 checkpoint |
| `POST /v1/new-messages/reset` | 重置 new-messages checkpoint |

常用 curl：

```sh
curl http://127.0.0.1:38765/v1/me
curl 'http://127.0.0.1:38765/v1/contacts?kind=chatroom&limit=5&refresh=true'
curl 'http://127.0.0.1:38765/v1/sessions?limit=5'
curl 'http://127.0.0.1:38765/v1/messages?username=wxid_xxx&date=today&limit=20'
curl 'http://127.0.0.1:38765/v1/media?path=/Users/xxx/.wxview/cache/.../media/abc.jpg'
curl 'http://127.0.0.1:38765/v1/timeline?kind=chatroom&date=today&limit=20'
curl 'http://127.0.0.1:38765/v1/search?query=合同&limit=20'
curl 'http://127.0.0.1:38765/v1/favorites?limit=20'
curl 'http://127.0.0.1:38765/v1/articles/accounts'
curl 'http://127.0.0.1:38765/v1/sns/feed?limit=20'
curl -X POST http://127.0.0.1:38765/v1/cache/refresh
```

## 常用场景

### 今天某个群在聊什么

```sh
./bin/wxview contacts --kind chatroom --query "AI" --limit 20 --format json
./bin/wxview messages --username 123@chatroom --date today --limit 500 --format json
```

### 今天所有 AI 相关群聊的时间线

```sh
./bin/wxview timeline --kind chatroom --query AI --date today --limit 1000 --format json
```

### 给 AI Agent 读取新消息

```sh
./bin/wxview unread --limit 20 --format json
./bin/wxview new-messages --limit 100 --format json
```

### 搜索本地微信消息

```sh
./bin/wxview search --query "报价" --kind all --date today --format json
./bin/wxview search --query "合同" --username wxid_xxx --start "2026-05-01" --format json
```

### 导出机器可读数据

```sh
./bin/wxview contacts --kind friend --format csv
./bin/wxview contacts --kind chatroom --format jsonl
./bin/wxview messages --username wxid_xxx --date yesterday --format jsonl
./bin/wxview timeline --kind chatroom --query AI --date today --format csv
```

## 命令速查

| 命令 | 用途 |
| --- | --- |
| `wxview init` | 首次设置：检测微信、提取并验证支持数据库 key |
| `wxview me` | 查看当前 Wxview 会读取的微信账号身份 |
| `wxview daemon start` | 启动后台缓存刷新和索引维护 |
| `wxview daemon status` | 检查 daemon 是否响应 health |
| `wxview api start` | 后台启动 WebAPI，默认只监听本机 |
| `wxview api status` | 查看单个托管 WebAPI 实例状态 |
| `wxview api stop` | 停止单个托管 WebAPI 实例 |
| `wxview cache status` | 查看解密缓存 freshness 和 key 覆盖 |
| `wxview cache clean-tmp` | 清理解密 cache 层过期临时文件 |
| `wxview index status` | 查看消息索引是否 missing/building/ready/stale |
| `wxview index refresh` | 手动刷新或续跑消息索引 |
| `wxview index clean-tmp` | 清理 index 层旧临时文件 |
| `wxview contacts` | 查询联系人、群、公众号等联系人表记录 |
| `wxview members` | 查询群成员和群主 |
| `wxview sessions` | 查询最近会话 |
| `wxview unread` | 查询未读会话 |
| `wxview new-messages` | 查询自上次 checkpoint 以来的新消息 |
| `wxview messages` | 按明确 username 查询单个会话消息 |
| `wxview timeline` | 按时间范围合并多个会话消息 |
| `wxview search` | 搜索消息正文和 `content_detail` |
| `wxview favorites` | 查询微信收藏 |
| `wxview articles` | 查询公众号/订阅号 appmsg 文章 |
| `wxview sns` | 查询朋友圈 feed、搜索结果和通知 |

每个命令都有独立帮助：

```sh
./bin/wxview messages --help
./bin/wxview timeline --help
./bin/wxview index --help
```

## 输出格式

多数查询命令支持：

- `--format table`：人读。
- `--format json`：机器读，推荐给 AI Agent 和脚本。
- `--format jsonl`：一行一个 JSON 对象，适合管道。
- `--format csv`：适合表格和 shell 处理。

`messages`、`timeline`、`search` 和 `new-messages` 返回同一套消息 item schema。
JSON 输出使用 `{meta, items}` envelope：

```json
{
  "meta": {
    "schema_version": 1,
    "timezone": "Asia/Shanghai",
    "mode": "messages",
    "returned": 100,
    "has_more": true,
    "next_args": ["messages", "--username", "wxid_xxx", "--after-seq", "1773421988000", "--limit", "100", "--format", "json"]
  },
  "items": []
}
```

可靠分页时直接执行 `meta.next_args`。`--offset` 更适合临时人工查看，不建议作为长期
同步游标。

### 最近消息与稳定分页

```sh
./bin/wxview messages --username wxid_xxx --order desc --limit 100 --format json
./bin/wxview messages --username wxid_xxx --start '2026-10-01' --end '2026-10-21' --order desc --limit 100 --format json
```

显式传入 `--order asc|desc` 启用稳定分页，必须设置正数 `--limit`；后续请求直接使用
`meta.next_args`。CLI 新增 `meta.order` / `meta.next_cursor`，WebAPI 对应
`GET /v1/messages?username=wxid_xxx&order=desc&limit=100` 和 `meta.next_url`。
这些是 schema v1 的附加元数据，消息 item schema 不变。末页没有 `next_cursor` 或下一页参数。

排序键为 `(create_time, seq, local_id, source_db)`，desc 对完整键倒序。
游标绑定账号及数据目录身份、会话、方向、规范化起止范围；损坏、不支持的版本或
跨查询复用会报错。游标不含正文、密钥或原始账号路径，校验和只检测损坏，不是授权凭证。
不能与 `--offset` / `--after-seq` 混用（即使显式传 0 也拒绝）。未传 `--order` 的旧调用
继续默认 ASC，并保留旧 `next_after_seq` 协议；需要稳定翻页的新客户端应显式选择 order。

未提供结束时间时，第一页固定为请求时的 Unix 秒；`next_args` / `next_url` 使用固定的
Unix 秒边界，不延续 `today` 等相对日期。起止均包含边界，日期按本地日历解释。
固定 end 不是快照：新增消息若在 end 之后不会出现；回填在游标之后的排序区间可能出现，
回填在已翻过的区间不会补发；删除会缩短后续结果。缓存刷新保留源分片名和 local_id 时可续页，
若迁移重建改变行身份，应重新开始。索引失效回退扫描仍使用相同游标语义。

新分页普通浏览只读取已有缓存；缺缓存会提示初始化/刷新，不隐式获取密钥。
显式 `--refresh` 仍执行刷新。扫描路径每个分片最多取 `limit + 1` 条候选，逐分片有界合并，
仅对最终页做正文处理；媒体处理不包含用于判断 has_more 的额外记录。没有全量 count。
这限制了 Go 的加载和解析量，但缺少排序索引时 SQLite 仍可能扫描全表并临时排序。
详见 [实现与验证记录](docs/message-pagination-and-daemon.md)。

消息正文规则：

- `content` 保留原始解码正文；XML 仍然是 XML，群消息前缀也会保留。
- `content_detail` 放解析后的便利字段，例如文本摘要、链接标题、文件名、图片/视频元数据和本地媒体路径。
- CDN AES key 不会复制到 `content_detail`。
- 正常查询不输出 DB/table/local row 等来源字段；需要排查 cache/shard 时再加 `--source`。

## Daemon、Cache 和 Index

Wxview 的本地状态默认在：

```sh
~/.wxview/
```

关键路径：

| 类型 | 路径 |
| --- | --- |
| key store | `~/.wxview/cache/<account>/keys.json` |
| 联系人缓存 | `~/.wxview/cache/<account>/contact/contact.db` |
| 会话缓存 | `~/.wxview/cache/<account>/session/session.db` |
| 消息缓存 | `~/.wxview/cache/<account>/message/message_*.db` |
| 消息索引 | `~/.wxview/cache/<account>/index/messages.db` |
| 媒体缓存 | `~/.wxview/cache/<account>/media/` |
| 增量消息状态 | `~/.wxview/cache/<account>/state/new_messages.json` |
| 刷新元数据 | `~/.wxview/cache/<account>/mtime.json` |
| daemon socket | `~/.wxview/wxview.sock` |

daemon 是内部缓存维护服务，不是 WebAPI。WebAPI 由 `wxview api start` / `wxview api serve` 提供：

- 支持的 CLI 形式只有 `wxview daemon`、`wxview daemon start`、`wxview daemon stop`、`wxview daemon status`。
- `wxview daemon` 和 `wxview daemon --help` 一样，只显示帮助，不启动服务。
- socket `~/.wxview/wxview.sock` 是内部控制通道，不是 WebAPI。
- `wxview api start --addr 127.0.0.1:38765` 后台启动独立 HTTP 数据入口，默认只监听本机。
- `wxview api start --addr 0.0.0.0:38765` 或绑定具体局域网 IP 可以让其他设备访问；当前没有内置认证，不要暴露到不可信网络。
- `wxview api status` 和 `wxview api stop` 不接收 `--addr`；V1 只管理一个 API 实例。
- `wxview api serve --addr 127.0.0.1:38765` 以前台方式运行同一个 HTTP 服务，适合调试；它也参与单实例保护。
- 已有 WebAPI 实例运行时，再执行 `wxview api start` 或 `wxview api serve --addr 其他端口` 会报错。
- V1 不 patch WAL；刷新在微信写回或 checkpoint 到主 DB 后接近实时。

消息索引用来加速 `messages` 和 `timeline`：

- `ready` 且准实时：优先走索引。
- `missing`、`building`、`schema_mismatch`、过度 `stale`：自动回落到直接扫描解密缓存。
- `--no-index` 可以强制走扫描路径，便于对照排查。
- `index rebuild --reset` 会删除并重建派生索引，不影响源微信库和解密缓存。

## 权限和安全

默认 `wxview init --key-source scan` 在复用有效密钥后，扫描运行中的微信进程：macOS 可能需要 sudo
和终端的 Developer Tools 权限，Windows 需要管理员终端。Hardened Runtime 可能导致
`task_for_pid failed`，sudo 不能保证解决。原生 Apple Silicon 可以改用上面的临时副本入口。

### 临时副本取钥的依赖与恢复

- 依赖：Apple Command Line Tools 的 `/usr/bin/python3` 与 LLDB。缺失时自行运行
  `xcode-select --install`；wxview 会先检查，不自动安装或下载 helper。
- 账号：退出微信后仅有一个本地账号才自动选择；多个账号必须用 `--db-root`，不按最近修改时间猜测。
- 数据权限：若提示访问被拒绝，在“系统设置 → 隐私与安全 → 文件与文件夹 → 微信.app”中
  打开微信数据访问项（本机显示为“微信”）。无需为此修改 SIP。系统授权由用户完成。
- 没有授权入口时：失败会保留 `~/.wxview/bootstrap/WeChat-trial.app` 并给出路径；手动打开它触发
  提示。授权后正常退出该副本，再删除 `~/.wxview/bootstrap/` 并重新执行命令。不要删除正式
  `~/.wxview/cache/`。程序不会自动重试或修改系统授权。
- 正常 Ctrl-C：取消且不保存候选，等待最多 10 秒让 helper 关闭自己的微信子进程。强杀、断电或
  清理失败时，下次初始化拒绝使用残留工作目录。先正常退出临时微信，再手动删除该目录，重跑。
- 如果提示“密钥已保存，清理未完成”：只处理明确指出的残留，然后打开原版；不必重新取钥。
- 全部当前必需库必须有有效密钥，本轮也必须至少捕获一把有效新候选。否则正式密钥不变。
  辅助库缺钥只警告；固定截止时间不是一律失败。保存前再次检查账号、当前分片与首页 HMAC。
- `keys_saved: true` 只代表密钥保存，`cache_refreshed: false` 表示尚未更新解密缓存；之后运行
  查询命令的 `--refresh` 或启动 daemon。缓存失败保留旧解密副本，不能把旧内容误认成刷新成功。
- 同一新版 wxview 的 macOS 初始化互斥；不要同时运行不同版本的 init。密钥沿用 v1 格式，旧版
  可继续读取，回退不需要迁移缓存。

已知 FTS 限制：微信的 contact/favorite/message FTS 索引使用私有 `MMFtsTokenizer`，普通 SQLite 的
全文匹配与全库完整性检查可能报错。Wxview 的联系人、收藏、消息搜索读取正文或自己的派生索引，
不直接查询这些微信 FTS 索引；不能据此认定密钥错误。媒体和业务验收范围见升级清单。

Python helper 随二进制内嵌，不依赖项目源码或外部脚本下载。算法参考
[DaneelOlivaw1/macos-wechat-export](https://github.com/DaneelOlivaw1/macos-wechat-export)
的 `f6c5fb32306945fcb95740c3c2e70cbe548317dc`，按 MIT 许可保留
[LICENSE](internal/key/lldbhelper/LICENSE) 与 [NOTICE](internal/key/lldbhelper/NOTICE)，运行时一并释放。

Wxview 不会打印完整 DB key。`init --verbose` 只显示短 fingerprint 和逐库状态。

如果之前用 `sudo` 写出了 root-owned 状态，可以修复一次权限：

```sh
sudo chown -R "$USER":staff ~/.wxview
```

## 开发和验证

常用检查：

```sh
GOCACHE=$(pwd)/.cache/go-build go test ./...
GOCACHE=$(pwd)/.cache/go-build go vet ./...
```

帮助行为 smoke check：

```sh
GOCACHE=$(pwd)/.cache/go-build go run ./cmd/wxview --help
GOCACHE=$(pwd)/.cache/go-build go run ./cmd/wxview init --help
GOCACHE=$(pwd)/.cache/go-build go run ./cmd/wxview daemon --help
GOCACHE=$(pwd)/.cache/go-build go run ./cmd/wxview contacts
GOCACHE=$(pwd)/.cache/go-build go run ./cmd/wxview messages
GOCACHE=$(pwd)/.cache/go-build go run ./cmd/wxview timeline
```

如果遇到 Go 默认缓存目录权限问题，给命令加：

```sh
GOCACHE=$(pwd)/.cache/go-build
```

## 致谢与参考项目

感谢以下项目及其作者公开实现与研究成果，Wxview 的开发从中受益：

- [DaneelOlivaw1/macos-wechat-export](https://github.com/DaneelOlivaw1/macos-wechat-export)：新版 macOS 微信取钥的重要参考。v0.4.0 的内嵌 LLDB helper 改编自其 `capture_keys.py`（提交 `f6c5fb32306945fcb95740c3c2e70cbe548317dc`），用于临时副本启动、PBKDF 参数捕获与密钥验证；感谢作者分享这条技术路线。
- [acmerfight 的原始 Gist](https://gist.github.com/acmerfight/0a01249ef72970d07a2603dbb629e80f)：`macos-wechat-export` 在 NOTICE 中注明的上游来源，感谢原作者公开 macOS 微信取钥实现。
- [wechat-decrypt](https://github.com/ylytdeng/wechat-decrypt)：数据库 key 获取、SQLCipher 解密、图片 `.dat` 解码和媒体处理方向上的重要参考。
- [wechat-cli](https://github.com/huohuoer/wechat-cli)：本地微信数据 CLI 化、AI Agent 使用方式和查询体验上的参考。
- [wx-cli](https://github.com/jackwener/wx-cli)：命令设计、daemon/cache 思路、媒体解析和自动化输出上的参考。

Wxview 不要求用户另行安装这些项目。改编的 LLDB helper 随二进制内嵌；其上游 MIT [LICENSE](internal/key/lldbhelper/LICENSE) 和 [NOTICE](internal/key/lldbhelper/NOTICE) 保留在源码、发布包及运行时私有工作目录中。

## 免责声明

Wxview 只面向读取用户自己机器上的本地微信数据。请遵守当地法律、平台规则和隐私边界。
不要用它读取、导出或传播你无权处理的数据。

已观察到的登录副作用：本机微信 4.1.13 在临时副本取钥后，返回原版时要求重新扫码登录；用户已扫码进入。原版签名验证通过，但这不代表登录状态保持不变。不能承诺取钥后免扫码，后续日常使用正常，异常恢复测试后再次进入原版未要求扫码；不能据此保证所有环境的登录状态不变。
