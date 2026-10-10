# wxview 新版微信升级：范围与整体设计（简化版）

已接受 v2 · 2026-10-10 · READY。用户明确要求“按这个简化版来，开始实现”。替代 v1。
基线：a4b2a2c791c3461e22742e3cc6c82d11eb18aef1。
关联：[总 TODO](wechat-new-version-upgrade-todo.md)、实机验证报告（含本地数据，不随仓库发布；脱敏结果见总 TODO）。

## 1. 最终交付与范围

用户通过发布版 wxview 完成新版取钥，随后正常查询和刷新缓存，不需要自己下载脚本或转换密钥。首次可能需要退出微信、授予权限和手机确认，日常读取不再调试微信。

- 新路线只做 macOS 原生 ARM64；首批正式验收为本机微信 4.1.13 / macOS 27.0.1。其他版本标明未验证，不按版本号假定密钥有效。
- 保留现有 scan、Windows、密钥格式和查询协议；不承诺 Windows 新版取钥支持。
- 依赖 Apple Command Line Tools 的 Python/LLDB，先检查可用性，缺失就指导安装，不自动安装。
- 复用 Go 解密器、缓存、CLI/WebAPI 查询。init 只准备密钥；解密与缓存更新仍由现有刷新流程负责。
- 不新增公开导入命令、后台服务、取钥 WebAPI、WAL 处理或搜索架构；不修改原版微信签名，不关闭 SIP，不使用 sudo 执行新路线。

仅导入密钥不能完成首次安装；自动按版本启动调试微信会隐藏影响。因此选择显式新入口，保持其他调用方式不变。

## 2. 用户流程：一次执行，失败后手动重跑

```sh
wxview init                         # 原有扫描/复用方式
wxview init --key-source lldb       # 新取钥方式
wxview init --key-source lldb --db-root /absolute/path/to/db_storage
```

1. 用户先正常退出微信。程序发现任何微信仍运行，就报错并退出，不强退、不等待回车。
2. 一个本地账号自动选择；多个账号必须指定 db-root，不在微信退出后猜账号。db-root 仅供 lldb 使用，须为当前用户微信目录内的真实账号 db_storage，绑定后不随登录切换。
3. 程序复制并只重签临时副本，通过 LLDB 启动，提示用户登录所选账号；只显示进度和脱敏诊断。
4. 全部本轮目标捕获完成，或到达固定 300 秒上限，即停止收集并判断结果。不做空闲计时、不自动重试。用户取消与异常退出均算失败；达到时限不一定失败，仍按必需库覆盖判断。
5. 校验并保存成功后关闭、清理本轮副本，提示用户手动打开原版。后续用现有查询/刷新命令读取。

`--key-source` 默认 scan。显式 lldb 总是请求一次捕获，输出区分新捕获和旧密钥复用；普通 init 可继续复用已有密钥。新入口不额外强制 TTY，用户仍需在本机完成窗口/手机操作，程序不会自动登录。

权限问题只报错、说明路径并退出。本机实测设置为“隐私与安全 → 文件与文件夹 → 微信.app → 微信”；若未出现授权入口，错误说明提供临时副本的确切路径，用户可手动打开它触发系统引导，授权后退出副本、清理临时目录，再重新运行。程序不自动开系统设置或重试；这条手工排障流程也必须在发布包上验收。

## 3. 实现边界与必要数据

流程：CLI init → internal/key 协调 → 内嵌 Python/LLDB helper → Go 校验/保存 → 现有解密与查询。

- Go 负责参数、账号固定、临时副本、候选复验、正式保存和收尾；helper 只启动自己的副本、捕获并写私有候选、结束自己的进程。
- helper 随二进制内嵌，运行时释放，不下载远端代码；采用上游代码须保留 MIT/NOTICE 来源。
- 复用 TargetDB、Store、Entry 和现有结果结构，只增加实际需要的来源/计数/清理结果；不预先设计通用事件框架。

| 数据 | 生产者 → 消费者及用途 | 生命周期 |
| --- | --- | --- |
| 本轮目标清单 | Go → helper；账号、DBRelPath、必需标志、page1/salt，用于候选筛选；Go 提交前重新核对 | 内存或私有输入文件，本轮结束删除 |
| candidates.json | helper → Go；version=1，DBRelPath → salt/enc_key；只存 HMAC 通过的候选 | 0600，helper 原子替换；退出后 Go 读取，收尾删除 |
| keys.json v1 | Go → 现有缓存/查询；沿用当前账号级映射 | 仍在原路径，无格式迁移 |
| 初始化锁 | macOS CLI init 在读取/取钥/保存前取得，结束释放；排除 scan 与 lldb 并行 | 一个用户级 OS 文件锁，竞争立即报错，不排队 |

当前生产调用中，正式密钥写入都由 CLI init 驱动，因此只在该入口加锁，不改造整个 key 包的公开 API，不增加 Windows 新锁逻辑。普通查询不写密钥、不持锁。实现时复核调用点；发现新的写入入口才重新评估，不能无证据扩大改动。不同版本二进制的并行 init 不作保证，文档明确禁止。

使用固定私有工作目录 `~/.wxview/bootstrap/`（0700），副本为其下的 WeChat-trial.app。目录已存在则拒绝本轮启动并给出人工清理步骤，不自动接管旧工作、不续跑候选、不持久保存 PID。只有本轮成功创建的目录才由本轮自动清理，拒绝符号链接路径。

## 4. 取钥与保存规则

- 目标沿用当前 required/optional 发现规则：必需为 contact/contact.db 与数字 message 分片；不以磁盘全部 .db 都捕获作为成功条件。
- helper 观察 CCKeyDerivationPBKDF：按 ARM64 参数匹配 SHA512、16 字节盐、2/256000 轮；256000 轮派生 raw key，2 轮通过 salt XOR 0x3a 识别 raw key 输入，首页 HMAC 通过才记候选。
- helper 正常停止且本轮副本退出后，Go 重新发现固定账号的目标和首页；拒绝错误账号、重复路径、未知格式、非法密钥、路径逃逸；候选逐库重新 HMAC 验证。新增必需分片不能漏判。
- 用最新 Store 合并：有效候选替换同账号/data_dir/DBRelPath 的旧条目；未捕获但仍有效的旧密钥保留；salt 只作为验证属性，同路径不保留重复 salt 条目。
- 本轮至少捕获一把有效密钥，且所有当前必需库均由有效候选或有效旧密钥覆盖，才一次保存；辅助缺失警告。否则不改正式 store。
- 沿用现有 SaveStore 的私有临时文件＋rename 写法，保持 0600。不新增提交恢复协议；写入报错则原样报告，不自动重试。异常中断后以正式文件内容为准，普通 init 重新验证。
- 保存成功不等于缓存已刷新。输出新捕获数、复用数、缺失必需库、辅助警告，以及保存/清理是否成功；不得输出密钥或微信原始日志中的私密内容。

## 5. 失败与人工恢复

| 情况 | 首版处理 |
| --- | --- |
| 依赖缺失、原版未退出、账号不明确 | 报错退出，给出准确操作，不启动副本 |
| 权限失败 | 不保存候选；关闭本轮进程，删除候选文件，保留副本供用户按第 2 节触发授权；明确标注残留位置，用户退出并清理后重跑 |
| 零捕获、必需缺失、其他捕获错误 | 不保存，关闭本轮进程并清理；显示缺失项/原因，用户决定是否重跑 |
| 正常结束、Ctrl-C | helper 的 finally 关闭自己启动的副本；Go 等待收尾并清理。取消不提交。等待收尾最多 10 秒；失败明确提示人工退出，不按进程名批量杀进程 |
| helper/Go 强制退出、断电 | 不承诺自动恢复；下次见工作目录残留就拒绝启动，指导用户正常退出残留副本、删除该目录后重跑，不自动杀旧 PID |
| 保存失败 | 报错，不重试取钥；保留现有正式文件，不删除正式密钥或缓存 |
| 保存成功但清理失败 | 明确报告“密钥已保存，清理未完成”并返回错误；用户只处理残留，无须重新取钥 |

收尾先确认本轮副本退出，注销 LaunchServices 注册，再删除本轮目录。无法确认退出或无法清理时保留具体路径供人工处理，不隐藏失败。检查原版签名与开始前一致；变化则停止，不尝试自动修复原版。外部准备命令设置 120 秒上限，不无限等待。

临时副本仍访问真实微信数据，微信自身可能更新登录状态和数据库；wxview 不主动写微信源库。系统权限由用户管理，不自动撤销可能被同应用其他运行方式共用的权限。

以上没有跨重启任务恢复：流程中的输入清单只用于筛选，候选只用于本轮合并，正式 store 只供现有查询消费，锁只防并发，残留目录只触发人工处理。删除其中任何一项都会失去对应的账号校验、密钥交接或失败保护。

## 6. 完成标准和推进安排

10 步 TODO 保持不变。实现与测试由当前执行代理负责并逐项更新；用户参与系统授权、登录、测试会话选择及原版使用观察。观察尚未完成，不代替用户宣布通过。

- TODO 4–6：实现上述正常流程与报错/人工恢复，验证错误账号、部分缺钥、取消、超时、并发 init、写入和清理失败；正常截止与异常退出必须分开测试。
- TODO 7：从正式密钥经缓存刷新验收联系人、私聊/群聊、sessions/unread/new-messages、timeline/search、既有图片/语音/视频/文件，检查 CLI/WebAPI 共用路径；辅助能力按可用库验证。不自动发送测试消息。
- TODO 7：发布前查明 3 个 FTS 库检查异常的实际影响；影响承诺功能则修复，不影响则以证据说明限制，不能直接忽略。
- TODO 8：运行 go test ./...、go vet ./... 和旧 scan/Windows 相关回归与构建；最终原版恢复、收发和重启观察需用户确认并记录范围。
- TODO 9：用实际构建包验证初始化、权限排障、查询及 version 无副作用；无项目源码依赖；帮助、许可、依赖和人工恢复说明齐全。
- TODO 10：分别记录代码审查、CI、合并、发布、实机验收。当前未见仓库 CI 配置，届时核实而不虚报。Git 操作、合并、发布仍按单独授权执行。

以上均在总 TODO 保留，触发检查为对应实现完成、合并前、发布前和最终验收；未执行的实机验收不阻止编写实现，但阻止发布支持声明。旧版仍能读取 v1 密钥，既有缓存不迁移；范围变更再评估设计。

## 7. 门禁状态

简化的是自动化程度，不是完整交付目标。与 v1 相比，删除自动权限重试、空闲计时、跨重启进程恢复、全模块写入 API 重构和预设通用数据结构。范围文稿已完成，简化设计等待维护者接受。

```text
Implementation Gate: AWAITING_ACCEPTANCE
Work: wechat-new-version-upgrade
Baseline: a4b2a2c791c3461e22742e3cc6c82d11eb18aef1
Verification: full（简化后的新候选）
Architecture source: 本候选 v2、AGENTS.md、独立验证报告
Algorithm coverage: 取钥、验证、保存、正常清理；少见异常显式转人工
Data-structure coverage: 复用目标/存储结构，增加私有候选与入口锁
Open material items: 维护者接受简化后的范围与设计
Implementation authority: 当前仅简化设计；不开始改运行代码
Next action: 维护者接受后复核 READY，进入 TODO 第 4 步
```


## 实现准入回执（2026-10-10）

当前 HEAD 与设计基线一致；重新核对 init→EnsureSupportedKeys→EnsureDBKey/CompactStoreFile，查询刷新只走 LoadDBKey。本轮用户接受 v2 并授权实现，异常人工恢复属于接受范围。前文候选门禁块保留为历史，以下为当前有效回执。实机操作在需要时请用户配合，提交/发布不在本次授权内。

```yaml
kind: VerifiedDesignReceipt
version: 1
work_id: wechat-new-version-upgrade
baseline_sha: a4b2a2c791c3461e22742e3cc6c82d11eb18aef1
scope: 显式 LLDB 取钥、合并保存、正常清理与人工恢复、验证和文档
architecture:
  artifacts: [docs/plans/2026-10-10-wechat-upgrade-scope-design.md（已接受 v2）]
  acceptance_source: 用户本轮明确接受简化版并要求开始实现
  acceptance_evidence: 好，就按这个简化版来，开始实现，做完一项就更新 todo
coverage:
  algorithms: 第2、4、5节：正常流程、捕获/保存判定、取消与异常人工恢复
  data_structures: 第3节：复用目标/store、私有候选、macOS init入口锁
  cross_trace: 第3节结构表及第5节末尾消费者/失败保护说明
  invariants: 第4节账号、HMAC、必需覆盖与正式文件保护
  failures_recovery: 第5节；强杀残留拒绝续跑，人工恢复
  migration: 第1、6节；keys.json v1不迁移，旧查询路径保留
open_material_items: []
verified_by: Codex
verified_at: 2026-10-10
verdict: READY
```

## 发布阶段增量复核（2026-10-10）

Implementation Gate: READY。基线仍为 a4b2a2c791c3461e22742e3cc6c82d11eb18aef1；复用上面的 VerifiedDesignReceipt。本阶段增量仅版本号、说明、CI与发布验证，不改变取钥算法、数据结构或生命周期。用户明确授权“先做代码审查，没问题就提交推送，然后需要发新版本”。执行代理负责在第10步记录CI、发布与发布资产回读结果；本地证据和公开摘要分开保存。

最小CI只运行已有Go测试/vet、Python合成测试及平台构建，不读用户微信数据、不取钥、不自动发布。新版本使用当前已验收实现；发布资产在下载回读后运行版本、已有密钥初始化与限定会话查询验收，完整LLDB捕获与异常恢复沿用同一运行实现的实机证据，不为版本号重复要求退出/登录。

### CI跨平台修复增量准入

Implementation Gate: READY。当前基线685034ffbc4814b4d5a705887da3465d77c78337。相对前次回执，CI发现JSON候选的斜杠路径在Windows不被filepath.IsAbs判为绝对路径；修复为所有平台显式拒绝根斜杠，沿用第4节非法路径拒绝不变量，生产消费者与文件格式不变。另外修正两项旧测试的Windows用户目录隔离与JSON转义断言，不改缓存清理或媒体运行逻辑。用户已授权修复、复验和发布。

```yaml
kind: VerifiedDesignReceipt
version: 1
work_id: wechat-new-version-upgrade
baseline_sha: 685034ffbc4814b4d5a705887da3465d77c78337
scope: 候选路径跨平台校验与测试移植性修复
architecture:
  artifacts: [docs/plans/2026-10-10-wechat-upgrade-scope-design.md]
  acceptance_source: 已接受简化v2及本轮完成TODO授权
  acceptance_evidence: 继续第10步，先做代码审查，没问题就提交推送，然后需要发新版本；完成todo
coverage:
  algorithms: 第4节候选非法路径拒绝；Windows根斜杠仍必须拒绝
  data_structures: 第3节candidates.json，格式不变
  cross_trace: readCaptureFile校验JSON路径，mergeCapturedKeys再核对目标白名单
  invariants: 不接受绝对路径、路径逃逸或其他账号目标
  failures_recovery: 校验失败不提交正式密钥；沿用第5节
  migration: 无格式迁移，Windows取钥仍为原scan
open_material_items: []
verified_by: Codex
verified_at: 2026-10-10
verdict: READY
```
