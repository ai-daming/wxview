# Windows Config.Cipher 取钥（未发布，待实机验收）

对应 [issue #5](https://github.com/ai-daming/wxview/issues/5)。本轮授权完成本地实现、合成测试和构建检查；用户目前没有可用的 Windows 验收环境。

## 使用与支持边界

源码构建后仍使用 `wxview init`，无需新增参数。微信保持运行且登录；进程读取权限不足时按错误提示使用管理员终端。`init` 只保存通过验证的密钥，缓存仍由已有刷新命令维护。

这不是已发布 v0.4.2 包所包含的能力，也不是微信 4.2 兼容声明。尚未通过真实 Windows 微信验证，4.1.12.24 的原始故障报告 #1 也仍未回查。ARM64 客户端的对象布局及普通用户权限环境均不能从合成测试推断为支持。

## 处理链路与设计选择

1. 沿用现有数据库发现、有效密钥复用、账号级 `keys.json` 保存，以及必需库失败／辅助库警告的行为。
2. 初始化确实缺钥时，只读枚举微信进程的可读内存区域，分块扫描旧 SQLCipher 字符串和 `com.Tencent.WCDB.Config.Cipher` 名称。
3. 用名称地址和长度找到运行时节点，再读取 Config 对象中的候选数据。当前布局为节点 `+0x28` 的 Config 指针，以及 Config `+0x90/+0x98` 的数据指针／长度；这是假定的兼容布局，不是稳定公开接口。
4. 候选解码优先尝试已知 32 字节 XOR 掩码。对于 99 字节的 `x'<key64><salt32>'`，可利用本库已知的 32 个 ASCII salt 字节推导一个完整掩码周期；结构检查和最终本库首页 HMAC 都必须通过。
5. 运行时候选未通过时仍尝试旧格式候选。每把密钥按所查询数据库的首页 HMAC 验证；不以“高熵”或“像密钥”作为成功依据。
6. 一次 init 内共享每个 PID 已采集的候选材料及扫描失败，不按数据库反复全扫。每个数据库仍分别验证；候选材料仅驻本轮内存，不增加持久化文件，查询不进入采集路径。

采用独立的纯 Go 内存遍历／解码核心，Windows 文件仅负责系统调用。因此在 Mac 上可以运行真实算法的合成测试，但这些测试不能替代 Windows API 与微信对象布局的实机验收。保留现有密钥格式和解密／查询链路，不引入 Python、Frida、注入或新后台服务。

## 资源与失败处理

- 一次 init 共享 90 秒采集期限及累计 6 GiB 的内存读取请求预算；不会为每个数据库或 PID 重置预算。采集完成后验证已缓存候选不再次遍历进程。
- 内存读取每块最多 2 MiB；不再整段跳过大于 500 MiB 的可读区域。名称、字面量及运行时对象候选各自有数量界限，单个对象数据不超过 1024 字节，以限制内存占用。达到界限明确报告，不能据此宣称完整扫描。
- 连续块及地址相邻的可读区域之间保留重叠；读取失败、短读和地址缺口会清空重叠，不将缺口两侧字节拼成候选。不可读范围可跳过，其他范围中通过 HMAC 的候选仍可采用。
- 权限失败、不可读范围、预算耗尽、无候选和候选验证失败保留不同的错误原因；错误不包含完整密钥或进程内存内容。
- Ctrl-C 可取消枚举／采集与后续验证。候选返回后再次检查取消状态，避免在该边界收到取消后仍保存候选。已经在本轮较早完成保存的有效数据库条目不会回滚；这不是整个 init 的事务。
- 保持有效密钥复用和失败不覆盖有效条目的行为，不改变已有的多账号选择、WAL、索引或消息输出契约。

## 本地验证与待办

测试数据全部人工构造，不读取本机微信数据。`internal/key/testdata/windows_contact.b64` 是独立生成、带真实 80 字节 SQLite 保留区的合成联系人库；通过候选取钥、密钥保存／复用、正式缓存解密和联系人查询验证数据可用，而不仅验证返回一个字符串。生成器仅用于重新制作夹具，运行 Go 测试不需要 Python。

2026-11-21 本地验证（macOS ARM64）：

- key、decrypt、contacts 三个受影响模块测试通过；取钥、初始化、解密缓存和联系人查询的合成链路通过。
- 新内存遍历／解码核心语句覆盖率 87.6%；Windows 原生系统调用不在此覆盖率内，尚未运行。
- 新增取钥／初始化用例的 race 检查通过；CGO 关闭时相关合成测试通过。
- CLI 初始化／版本用例、正式 `init --help` 入口通过；macOS 与 Windows AMD64 的受影响包 vet 通过。
- Windows AMD64 CLI 与 key 测试程序交叉编译通过；测试程序包含仅读取自身人工构造数据的原生适配器用例，但本机不能执行 Windows 程序，不能记为已通过该用例。

针对性检查命令：

```sh
GOCACHE=$(pwd)/.cache/go-build go test ./internal/key ./internal/decrypt ./internal/contacts -timeout=90s
GOCACHE=$(pwd)/.cache/go-build go test ./cmd/wxview -run 'TestInit|TestVersion' -timeout=60s
GOCACHE=$(pwd)/.cache/go-build go vet ./internal/key ./internal/decrypt ./internal/contacts ./cmd/wxview
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 GOCACHE=$(pwd)/.cache/go-build go build -o bin/wxview-issue5-windows-amd64.exe ./cmd/wxview
```

未执行全仓回归。以下仍待真实 Windows 环境完成，不勾选 issue 完成：

- [ ] 记录操作系统、微信版本、客户端／进程架构和权限；从正式 init 完成必需库取钥与缓存查询。
- [ ] 在授权会话内两次人工发送不同标记，逐次刷新并确认新消息出现。当前不处理 WAL，须区分等待主库 checkpoint 与取钥失败。
- [ ] 回查 #1 的微信 4.1.12.24 环境；没有该版本则保留未验证状态。
- [ ] 验证 Windows 原生枚举、分块读取、Ctrl-C、无权限及退出进程的实际行为。

## 2026-11-21 本地 review 修复

本轮按 #5 的初始化、扫描复用、内存遍历、HMAC、取消／错误处理及保存链路复核；发现并修复两项 P2 问题：

1. **相邻可读区域的边界漏扫。** 原遍历在每个 VirtualQueryEx 区域开始时清空重叠，导致跨区域的运行时名称或旧格式密钥被漏掉。Windows 区域按页面属性划分，并非对象边界。现在仅在地址不连续或出现读取缺口时清空重叠；运行时名称、旧格式密钥跨区域用例先复现失败后通过，原有不可读／短读／非相邻区域用例仍通过。
2. **枚举失败被当作成功结束。** 原 Windows 枚举丢弃 VirtualQueryEx 的错误，将任意失败都当作遍历完成。现在仅把地址空间结束的 ERROR_INVALID_PARAMETER 当作终止，其余错误保留原因。枚举循环与系统调用分离；首轮和中途查询失败、正常结束、取消以及无推进／溢出元数据的合成用例通过。另有无效原生句柄测试随 Windows 测试程序编译，尚未实际运行。

API 依据：[VirtualQueryEx 的区域与返回语义](https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-virtualqueryex)、[ReadProcessMemory 对可读范围的要求](https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-readprocessmemory)。

review 修复后的测试和构建结果不改变前述实机验收边界。未运行全仓回归，未修改 GitHub issue 状态或执行提交／发布。

## 来源与许可证

- 运行时对象布局及已知掩码参考并改编自 [TANGandXUE/wcdb-key-tool](https://github.com/TANGandXUE/wcdb-key-tool/blob/79f1b5b92e12c66aa281b4a60a3c478b5f547dfa/wcdb_key_tool_windows.py)，MIT，Copyright (c) 2025 CloudDreamAI / TANGandXUE。核对源文件 SHA-256：`9de22ccc5070bfc5325ece1bdf4da8ca537eea594b821fc2acd46a62c0e41958`。保留完整 [MIT 许可证](third-party/wcdb-key-tool.LICENSE)。
- 调研对照 [yichen-wechat-local-vault](https://github.com/mcncarl/yichen-skills/tree/c9f47ecbcda513c0ce160bb55bfaf01060765053/yichen-wechat-local-vault) 的已知 salt 解码思路、Windows 验收和来源记录；未执行或安装其脚本。扫描会话、Go 解码／遍历、HMAC 集成和测试在 Wxview 内实现。
- SQLite 合成夹具使用公开的 [SQLITE_FCNTL_RESERVE_BYTES](https://sqlite.org/c3ref/c_fcntl_begin_atomic_write.html) 接口生成。

后续若分发包含此实现的二进制，发布包需同时携带上述完整 MIT 许可证及来源说明；本轮不发布或改变源码版本号，本地 Windows 验收包使用 `0.5.0-issue5-local` 构建标识。
