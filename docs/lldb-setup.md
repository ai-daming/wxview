# macOS 临时副本取钥

v0.4.0 新增此入口。已验证环境：原生 Apple Silicon、macOS、微信 4.1.13。
当前源码增加实验性 Intel x86_64 支持，需从源码构建；v0.4.0 发布包不包含此适配。
Intel 已验证环境：macOS 26.5.1、Intel Core i9、微信 4.1.15。Rosetta 未验证，Windows 不支持此入口。

## 第一次初始化

1. 安装 Apple Command Line Tools（缺失时运行 `xcode-select --install`），确保其 Python/LLDB 可用。
2. 保存微信草稿，从微信菜单正常退出。不要使用 sudo 运行新入口。
3. 运行 `./wxview init --key-source lldb`。多个本地账号时增加
   `--db-root /absolute/path/to/account/db_storage`，路径必须在当前用户的
   `~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/` 下。
4. 在临时微信窗口登录该账号，必要时完成手机确认。等待最多 300 秒。
5. 检查输出 `keys_saved: true` 和 `cleanup_completed: true`，再自行打开原版
   `/Applications/WeChat.app`。
6. 执行 `./wxview contacts --format json --limit 1 --refresh` 或启动 `./wxview daemon start`
   更新缓存。`cache_refreshed: false` 是初始化的正常结果：保存密钥不等于缓存已更新。

程序只重签 `~/.wxview/bootstrap/WeChat-trial.app`；不修改原版签名、不关闭 SIP。
副本使用真实微信数据，因此微信自身可能更新登录状态或数据库。
密钥保存在 `~/.wxview/cache/<account>/keys.json`，权限为 0600，格式仍是 v1。
全体必需库（联系人、数字消息分片）必须通过 HMAC 校验，且本轮至少捕获一把有效候选，才保存。
辅助库缺钥只给警告；`--verbose` 显示短指纹，不输出完整密钥。

## 权限与中断

- 若系统请求访问微信数据，由你确认。在“系统设置 → 隐私与安全 → 文件与文件夹 → 微信.app”
  中启用微信数据项；本机显示为“微信”。不要为了本流程重签原版或关闭 SIP。
- 若没有授权入口，程序会保留临时副本并给出其路径。手动打开该副本触发系统提示；授权后从菜单
  正常退出副本，删除 `~/.wxview/bootstrap/`，再重新运行。程序不自动更改权限或重试。
- Ctrl-C 取消捕获，不提交候选；程序给 helper 最多 10 秒关闭自己的子进程。
- 如果强杀、断电或清理失败留下工作目录，先正常退出临时微信，再手动删除
  `~/.wxview/bootstrap/`。**不要删除 `~/.wxview/cache/`**。下次运行不会自动接管残留。
- 若明确提示密钥已保存但清理失败，只处理残留，不必重新取钥。正式缓存不会被取钥失败覆盖。
- 如果多个 init 同时运行，新版 macOS 入口会拒绝竞争；不要并行运行不同版本的 init。

## 日常使用与回退

日常读取和刷新复用保存的密钥，不再启动 LLDB。新账号、数据库换钥或新增分片时才需要重新初始化。
`./wxview init` 默认仍是 scan：微信保持运行，macOS 缺钥扫描可能需要 sudo，Windows 需要管理员终端。
显式 `--key-source lldb` 每次都会捕获，即使已有有效密钥。
回退旧版可继续使用 v1 密钥与原缓存，不需要迁移数据。

helper 已内嵌在二进制中，无需源码、额外 Python 包或在线下载脚本。
参考 macos-wechat-export 提交 f6c5fb32306945fcb95740c3c2e70cbe548317dc；其 MIT LICENSE 和 NOTICE
随包提供，并在运行时释放到私有工作目录。

已观察到的登录副作用：本机微信 4.1.13 在临时副本取钥后，返回原版时要求重新扫码登录；用户已扫码进入。原版签名验证通过，但这不代表登录状态保持不变。不能承诺取钥后免扫码，后续日常使用正常，异常恢复测试后再次进入原版未要求扫码；不能据此保证所有环境的登录状态不变。

## Intel 实验性支持验证记录

2026-10-19，在上述 Intel 环境完成：

- 原有 scan 在普通用户及 sudo 下均返回 `task_for_pid=5`。
- 临时副本成功捕获并保存 22/22 个数据库密钥，报告 `cleanup_completed: true`。
- daemon 生成全部 22 个解密缓存；联系人、群数量、最近会话和指定群当天消息 JSON 查询通过。
- Python 合成测试及 LLDB 的完整捕获、超时、取消测试通过；Go 测试和 vet 通过。

该结果不代表所有 Intel 微信版本兼容；媒体完整验收及索引完成后的查询性能尚未验证。
取钥会使用真实微信数据，仍须遵守前面的正常退出、手机确认及恢复步骤。

Intel 在函数入口读取 x86_64 参数寄存器，并从返回地址之后的栈槽读取第七个参数（rounds）。
该架构禁用 LLDB 的跳过函数序言行为，以保证栈偏移正确；ARM64 保持原有寄存器读取方式。
