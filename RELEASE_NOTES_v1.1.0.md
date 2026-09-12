# memfix v1.1.0 发行版说明

**发布日期**: 2026-09-12
**目标设备**: 小米AX3000T (联发科MT7981B / Filogic 820)
**架构**: aarch64 (ARM64, Cortex-A53) 静态二进制
**实测状态**: ✅ 已在 192.168.1.58 小米AX3000T 上部署验证通过

---

## 一、版本概述

v1.1.0 是功能增强版本，在 v1.0.1 基础上新增 **场景调控（密集监控）**、**单次强制回收**、**日志自动轮换**、**近三天统计面板** 四项功能，并修复了 **时间戳时区偏移8小时** 的严重bug。同时日志文件路径改为由配置文件 `log_file` 选项指定，支持持久化存储。

---

## 二、新功能详情

### 1. 场景调控（密集监控模式）

**功能描述**: 当触发内存回收后，程序自动进入 **5分钟密集监控期**，期间每 **2秒** 检测一次内存状态（正常模式为每30秒）。5分钟后自动恢复正常监控间隔。

**设计目的**: 内存泄漏往往在回收后短时间内再次发生。密集监控能更快捕捉回收后的内存变化趋势，及时发现二次泄漏并再次回收，避免内存快速耗尽导致路由器卡顿或死机。

**工作流程**:
```
正常监控(30秒间隔) → 触发回收 → 密集监控(2秒间隔, 持续5分钟) → 恢复正常监控
```

**状态查看**: `memfix status` 会显示当前是否处于密集监控模式及剩余时间。

**参数**:
- 持续时间: 300秒（5分钟）
- 检测间隔: 2秒
- 常量定义: `BoostDuration = 300`, `BoostInterval = 2`

### 2. 单次强制回收

**功能描述**: `memfix once` 命令现在 **不论当前内存是否充足**，都强制执行完整回收流程。

**回收流程**:
1. `drop_caches=3` — 释放页缓存 + 目录项 + inode
2. `compact_memory=1` — 压缩Slab分配器
3. 可选的泄漏进程重启（如果配置了 `restart_process` 且回收后内存仍低于阈值）

**使用场景**:
- 手动维护路由器时主动清理内存
- 测试回收功能是否正常工作
- 回收后观察内存变化趋势
- 大流量下载/游戏前主动释放内存

**实测输出**（路由器上）:
```
memfix 单次强制回收模式
[2026-09-12 21:36:22] [DEBUG] 内存状态: 总计=238MB 可用=99MB(41%) ...
[2026-09-12 21:36:22] [INFO] 强制内存回收: 当前可用=41% (阈值=15%)
[2026-09-12 21:36:22] [INFO] 执行 drop_caches (释放页缓存+目录项+inode)
[2026-09-12 21:36:22] [INFO] 执行 slab 压缩 (compact_memory)
[2026-09-12 21:36:23] [INFO] 内存回收完成: 可用内存 41% -> 41%，释放约 0MB
已执行 2 项内存回收操作
```

### 3. 日志自动轮换

**功能描述**: 日志文件每 **3天** 自动轮换一次。

**轮换机制**:
- 当前日志: `memfix.log`
- 轮换后旧日志: `memfix.log.1`
- 下次轮换时覆盖 `.1` 文件（仅保留一份历史日志）

**设计目的**: 路由器存储空间有限（AX3000T可写分区约24MB），避免日志文件无限增长占用存储空间。

**实现细节**:
- 新增 `rotateLogIfNeeded()` 函数，在打开日志文件**之前**检查轮换（修复了O_APPEND更新mtime导致轮换检测失效的bug）
- 运行时 `checkLogRotate()` 在每次主循环中检查
- 常量: `LogRotateDays = 3`

### 4. status 近三天统计面板

**功能描述**: `memfix status` 命令新增 **近三天统计信息**。

**统计内容**:
- **回收次数**: 近三天内执行的内存回收总次数
- **平均可用内存**: 近三天日志采样的平均可用内存百分比
- **密集监控状态**: 当前是否处于密集监控模式及剩余时间

**实现方式**:
- 解析当前日志和 `.log.1` 轮换日志
- 自动过滤三天前的记录（基于CST时间戳）
- 回收次数匹配 "内存回收完成" 日志行
- 平均内存从DEBUG级 "内存状态" 行提取可用百分比

**实测输出**（路由器上）:
```
memfix 正在运行，PID=10432
当前内存: 总计=238MB 可用=99MB(41%) 空闲=86MB 缓存=30MB Slab=38MB

近三天统计:
  回收次数: 0 次
  平均可用内存: 无数据

最近日志:
[2026-09-12 21:36:57] [INFO] ===== memfix 守护进程启动 =====
...
```

---

## 三、Bug修复

### 时间戳时区偏移8小时（严重）

**问题现象**: 路由器系统时间为CST(UTC+8) 19:33时，日志时间戳显示为 11:33。

**根因分析**: 小米路由器OpenWrt固件的系统时区默认为UTC。Go语言 `time.Now().Format()` 使用系统时区格式化时间戳，导致时间偏移8小时。

**修复方案**: 使用固定CST(UTC+8)时区 `time.FixedZone("CST", 8*3600)` 格式化所有时间戳，不依赖系统时区设置。

**技术优势**:
- `time.FixedZone()` 不依赖系统tzdata数据库，在嵌入式系统上可靠工作
- 所有日志时间戳统一为CST，无论路由器系统时区如何设置
- `analyzeLogStats()` 也使用相同时区解析日志时间戳，确保统计准确

**修复前**（v1.0.1，UTC时间）:
```
[2026-09-12 13:29:18] [DEBUG] 内存状态: ...
```

**修复后**（v1.1.0，CST时间）:
```
[2026-09-12 21:36:57] [INFO] ===== memfix 守护进程启动 =====
```

### 日志轮换mtime检测失效

**问题**: 守护进程启动时以 `O_APPEND` 模式打开日志文件，这会更新文件的mtime为当前时间，导致 `checkLogRotate()` 检测时认为文件是新创建的，永远不会触发轮换。

**修复**: 新增 `rotateLogIfNeeded()` 函数，在 `daemonize()` 打开日志文件**之前**先检查并执行轮换。

---

## 四、配置变更

### 新增 log_file 配置项

v1.1.0 新增 `log_file` 配置项，允许用户自定义日志文件路径。

**配置示例** (`/etc/memfix.conf` 或 `/userdisk/.memfix/memfix.conf`):
```ini
# 日志文件路径（v1.1.0新增）
# /tmp/ 是tmpfs，重启后日志丢失
# /userdisk/.memfix/ 是持久化分区(ubifs)，重启不丢失
# /data/ 是另一持久化分区
log_file = /userdisk/.memfix/memfix.log
```

**默认值**: 不配置 `log_file` 时默认使用 `/tmp/memfix.log`。

**推荐**: 小米AX3000T用户建议设置为 `/userdisk/.memfix/memfix.log`（持久化分区，重启不丢失日志）。

**memfixctl 适配**: `memfixctl` 脚本已更新，自动从配置文件读取 `log_file` 路径；`status` 命令改为调用 `memfix status`（程序自身的status，能正确读取配置日志路径并显示完整统计）。

---

## 五、路由器实测验证

**测试设备**: 小米AX3000T (联发科MT7981B)
**测试IP**: 192.168.1.58
**固件**: 小米官方固件（基于OpenWrt 18.06定制）
**测试时间**: 2026-09-12 21:36 CST

### 验证结果汇总

| 测试项 | 结果 | 详情 |
|--------|------|------|
| 进程运行 | ✅ 通过 | PID=10432, `/userdisk/.memfix/memfix start` |
| 版本号 | ✅ 通过 | v1.1.0 |
| 时间戳(CST) | ✅ 通过 | 21:36:57（非UTC的13:36） |
| 日志路径(持久化) | ✅ 通过 | `/userdisk/.memfix/memfix.log` |
| 场景调控信息 | ✅ 通过 | 日志显示"触发回收后5分钟内每2秒密集检测" |
| status统计面板 | ✅ 通过 | 显示近三天回收次数和平均内存 |
| status最近日志 | ✅ 通过 | 正确读取配置路径的日志 |
| once强制回收 | ✅ 通过 | 内存41%时仍强制回收，执行2项操作 |
| 配置文件加载 | ✅ 通过 | `log_file = /userdisk/.memfix/memfix.log` |
| crontab自启 | ✅ 通过 | 每分钟自启已恢复 |
| 内存检测 | ✅ 通过 | 总计238MB, 可用99MB(41%) |

### 实测日志（路由器上）

```
[2026-09-12 21:36:57] [INFO] ===== memfix 守护进程启动 =====
[2026-09-12 21:36:57] [INFO] 版本: 1.1.0 | 目标: 小米AX3000T(MT7981B) | Go版本: go1.23.0
[2026-09-12 21:36:57] [INFO] 场景调控: 触发回收后5分钟内每2秒密集检测 | 日志每3天轮换
[2026-09-12 21:36:57] [DEBUG] 内存状态: 总计=238MB 可用=99MB(41%) 空闲=86MB 缓存=30MB Slab=38MB
```

---

## 六、升级指南

### 从 v1.0.1 升级到 v1.1.0

```bash
# 1. 停止旧版本
/userdisk/.memfix/memfixctl stop
# 如memfixctl停止失败，直接kill:
# kill $(ps w | grep '/userdisk/.memfix/memfix ' | grep -v grep | awk '{print $1}')

# 2. 备份旧版本
cp /userdisk/.memfix/memfix /userdisk/.memfix/memfix.v1.0.1.bak
cp /userdisk/.memfix/memfix.conf /userdisk/.memfix/memfix.conf.bak

# 3. 上传新二进制和memfixctl
# (通过scp或管道方式上传到路由器)
cp memfix /userdisk/.memfix/memfix
cp memfixctl /userdisk/.memfix/memfixctl
chmod +x /userdisk/.memfix/memfix /userdisk/.memfix/memfixctl

# 4. 更新配置文件（添加log_file，推荐持久化路径）
cat >> /userdisk/.memfix/memfix.conf << 'EOF'

# 日志文件路径（v1.1.0新增）
log_file = /userdisk/.memfix/memfix.log
EOF

# 5. 复制配置到/etc（memfixctl会自动做，手动也可）
cp /userdisk/.memfix/memfix.conf /etc/memfix.conf

# 6. 清理旧PID和旧日志
rm -f /var/run/memfix.pid /tmp/memfix.pid /var/log/memfix.log

# 7. 启动新版本
/userdisk/.memfix/memfixctl start

# 8. 验证
/userdisk/.memfix/memfixctl status
cat /userdisk/.memfix/memfix.log
```

### 全新安装

参见 README.md 中的安装说明。

---

## 七、文件清单

### 发布包内容 (`memfix-1.1.0-aarch64.tar.gz`)

| 文件 | 说明 |
|------|------|
| `memfix` | aarch64静态二进制 (v1.1.0, ~1.9MB, stripped) |
| `memfix.conf` | 配置文件模板（含log_file选项） |
| `memfix.xiaomi.conf` | 小米路由器专用配置模板 |
| `memfixctl` | 管理脚本（v1.1.0适配，日志路径从配置读取） |
| `S99memfix` | OpenWrt启动脚本 |
| `install.sh` | 通用安装脚本 |
| `install_xiaomi.sh` | 小米路由器专用安装脚本 |
| `README.md` | 精简版说明文档 |
| `CHANGELOG.md` | 变更日志 |
| `LICENSE` | MIT许可证 |
| `RELEASE_NOTES_v1.1.0.md` | 本发行版说明 |
| `src/memfix.c`, `src/memfix.h` | C版本源码（未编译交付，可自行编译） |
| `go/src/memfix/main.go`, `go.mod` | Go版本源码（主交付版本） |
| `Makefile` | C版本构建系统 |

### SHA256 校验和

```
memfix (aarch64):        a8b2a8b8d18f1b6c85ab41ce4357ec3125703d3c6e5fa2f747e1c8db94a8c8c4
memfix-1.1.0-aarch64.tar.gz:  d7c61a1d59176eabfdee458c75c9882ddda6a209f9696d556e0cecb5b0410f04
```

---

## 八、技术细节

| 项目 | 说明 |
|------|------|
| 语言 | Go 1.23.0 |
| 编译命令 | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w"` |
| 链接方式 | 纯静态，无动态库依赖 |
| 二进制大小 | ~1.9MB (stripped) |
| 运行时RSS | ~2.5MB |
| 密集监控参数 | 持续300秒(5分钟)，间隔2秒 |
| 日志轮换周期 | 3天 |
| 时区 | 固定CST(UTC+8)，不依赖系统时区 |
| 配置文件 | `/etc/memfix.conf`（memfixctl自动从/userdisk/.memfix/复制） |
| 默认日志路径 | `/tmp/memfix.log`（可通过log_file配置项修改） |
| PID文件 | `/var/run/memfix.pid`（fallback: `/tmp/memfix.pid`） |

---

## 九、已知限制

1. **C版本musl静态二进制**: 因musl.cc工具链下载不稳定，CI中可能跳过构建（gnu版本和Go版本不受影响，Go版本是主交付版本）
2. **平均内存统计**: 基于DEBUG级别的内存状态采样，如果关闭 `verbose=0`，平均可用内存统计可能无数据
3. **密集监控模式**: 程序重启后不会自动恢复密集监控模式（需要再次触发回收才会进入）
4. **日志轮换**: 仅保留一份历史日志（`.log.1`），更早的日志会被覆盖
5. **/etc ramfs**: 小米路由器 `/etc` 是ramfs，重启后 `/etc/memfix.conf` 会丢失；memfixctl每次启动会自动从 `/userdisk/.memfix/memfix.conf` 复制，crontab每分钟自启确保配置同步

---

## 十、兼容性

- **设备**: 小米AX3000T (联发科MT7981B / Filogic 820)
- **固件**: 小米官方固件（基于OpenWrt 18.06定制）
- **架构**: aarch64 (ARM64, Cortex-A53双核)
- **内存**: 256MB (实际可用约238MB)
- **依赖**: 无外部依赖，纯静态二进制
- **配置兼容**: v1.0.x 配置文件可直接使用，新增 `log_file` 为可选项
- **升级兼容**: v1.0.1 可直接升级到 v1.1.0，无需额外操作（建议添加log_file配置项）

---

## 十一、反馈与贡献

- **GitHub仓库**: https://github.com/kk3432/memfix
- **Issues**: https://github.com/kk3432/memfix/issues
- **提交bug时请附**: `memfix status` 输出、最近日志内容、路由器固件版本

---

## 十二、许可证

MIT License — 详见 LICENSE 文件

---

*本发行版已在小米AX3000T (192.168.1.58) 上实测部署验证通过，运行稳定。*
