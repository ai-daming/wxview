# 已提交 WAL 的只读缓存快照

此能力在源码中实现，尚未发布。它解决微信事务已提交到 WAL、但尚未 checkpoint 到主库时，Wxview 刷新仍读到旧消息的问题。不改变消息原文、输出 schema 或增量检查点语义。

## 使用

已有有效账号密钥即可使用现有刷新入口：

```sh
wxview messages --username '<明确的测试会话 username>' --date today --limit 20 --refresh --format json
```

也可使用已有 daemon 的自动刷新或 WebAPI `POST /v1/cache/refresh?scope=messages`。GET 查询不会因为 `refresh=true` 刷新；增量消息 GET 预览仍不推进检查点。本次实现和验证没有启动后台进程；实际使用时是否启动 daemon/API 仍遵守各自的交互确认。

## 处理过程

1. 只读打开主库与同名 `-wal`，记录文件身份、大小、纳秒 mtime 和 WAL 头指纹。
2. 校验 WAL 格式、头和帧校验和，逐事务维护已提交页面的偏移。未提交尾部、未完成帧和重置前旧代际尾部不进入结果；提交中的增长和截断决定最终页数。
3. 按最后已提交页映射逐页读取、验证 SQLCipher HMAC 并解密到临时文件。内存保存页面偏移，不一次载入整个数据库。
4. SQLite 验证与源稳定性检查通过后原子替换缓存；仅源变化最多重试三次。错误、取消或持续变化保留旧缓存，临时文件会清理。
5. `mtime.json` 保存实际采集的主库/WAL 签名及 `snapshot_version=1`。旧条目需刷新一次。WAL 创建、追加、重置或删除使下一次刷新重新采集。
6. daemon 监视 DB、WAL 和 journal 路径，不增加账号探测频率。新快照缓存与旧索引不匹配时回退直接扫描，索引更新后恢复加速。

整个过程不修改源 DB、WAL、SHM，不强制 checkpoint，也不对源库执行 SQL。缓存、元数据和派生索引仍写入 Wxview 自己的账号目录。

## 边界

- 支持现有 4096 字节 SQLCipher 页和 SQLite WAL 格式 3007000，两种校验和字节序；不支持的格式明确失败。
- rollback journal 存在时停止采集，等待微信自身完成；Wxview 不进行源库恢复。
- 检测依赖文件身份、大小、mtime 和 WAL 头；不为每次状态查询对整个源文件计算哈希。
- 持续繁忙的文件可能三次都无法稳定采集，此时需要稍后刷新；不会把失败标记成已刷新。
- 每个 DB 独立采集，不保证多个 DB 的同一事务时刻；缓存刷新也不提供跨分页快照。
- WAL 更早可见不等于所有会话始终实时。#7 的覆盖范围和新鲜度接口不纳入本项。

## 验证记录

合成数据由真实 SQLite 生成主库、第一笔及第二笔 WAL，再独立加密为测试夹具；不包含微信原始数据。测试覆盖：

- 已提交/未提交事务、大小端校验和、旧代际、部分尾帧、增长、截断再增长缺页、损坏、journal、源替换和取消/有界重试。
- 主库不变、仅 WAL 改变时，缓存消息依次从 1 条到 2 条再到 3 条；失败保持旧缓存及元数据。
- daemon 文件监视触发刷新；CLI `messages --refresh`；HTTP POST 刷新及 GET 只读预览。
- 旧索引拒用、扫描读取新消息、索引追上后与扫描一致。

受影响的 decrypt/key/daemon/msgindex/query/webapi 模块测试、CLI 相关测试、相关 race 和 macOS/Windows vet 通过。macOS arm64 与 Windows amd64 主程序构建通过；Windows 受影响测试包交叉编译通过；没有 Windows 实机环境，不能据此声称完成 Windows 运行验收。默认未运行全仓回归。

## macOS 实机验证

2026-11-24，macOS 27.2.1 / Apple Silicon arm64 / 微信 4.1.13：只读采集当前账号 33 个消息分片的主库加 WAL，均完成解密及 SQLite 验证。临时解密数据在退出时清除，公开记录不包含会话身份或消息正文。

使用授权会话的两条不同真实新消息即时对照，未使用人工标记。更早的最近 5 条消息已写回主库，因此没有将它们计为 WAL-only 验收成功。

实机对照摘要（Asia/Shanghai）：

| 消息时间 | 采集完成 | 主库 + WAL | 只读主库 | 主库采集期间稳定 |
| --- | --- | --- | --- | --- |
| 2026-11-24 01:00:51 | 01:00:57 | 存在 | 不存在 | 是 |
| 2026-11-24 01:01:44 | 01:01:51 | 存在 | 不存在 | 是 |

第二轮读取主库时，第一轮消息已存在于主库。两轮使用不同的新消息，没有发送原先约定的人工标记。daemon 状态为 stopped；未启动或重启 daemon/API。实际 `messages --refresh --order desc --limit 5 --format json` 已返回第一轮新消息（01:00:51），并报告 `query_mode=scan`、`index_status=stale`，没有用旧索引掩盖新缓存。第二次刷新遇到持续源变化，三次采集后明确报错；随后确认文件连续 3 秒未变化，再执行同一正常刷新命令成功。最后以 01:00:50–01:01:45 的明确时间范围从 CLI 回读，两条目标消息均存在，仍使用 scan 回退；索引追上后的恢复已由合成链路测试覆盖，没有为实机验收启动后台索引构建。

## 来源和许可

WAL 提交与截断处理参考并改写自 [freanocank/wechat-decrypt 的 wal_snapshot.py](https://github.com/freanocank/wechat-decrypt/blob/f0b3f9f95dda2d954ee3dbe1d139fb1944a5ecfd/scripts/common/wal_snapshot.py)，固定 commit `f0b3f9f95dda2d954ee3dbe1d139fb1944a5ecfd`。MIT 版权声明保存在 [wechat-decrypt.LICENSE](third-party/wechat-decrypt.LICENSE)。Go 运行时不调用该 Python 项目。

格式依据：[SQLite WAL 文件格式](https://sqlite.org/fileformat2.html#wal_file_format)、[SQLCipher 设计](https://www.zetetic.net/sqlcipher/design/)。
