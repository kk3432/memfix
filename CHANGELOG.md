# Changelog

所有重要的变更都将记录在此文件中。

格式基于 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，
本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 计划中
- [ ] 支持通过UCI配置（OpenWrt原生配置方式）
- [ ] 添加内存使用趋势统计和历史记录
- [ ] 支持Web管理界面（通过uhttpd）
- [ ] 添加更多设备的进程名预设
- [ ] 支持邮件/推送通知（内存严重不足时告警）

## [1.1.0] - 2026-09-12

### 新增
- **场景调控（密集监控模式）**: 触发内存回收后，自动进入5分钟密集监控期，期间每2秒检测一次内存状态，能更快捕捉回收后的内存变化和二次泄漏
- **单次强制回收**: `once` 命令现在不论当前内存是否充足，都强制执行完整回收流程（drop_caches + slab_compact + 可选进程重启），便于手动维护和测试
- **日志自动轮换**: 日志文件每3天自动轮换一次，旧日志保存为 `.log.1`，避免日志文件无限增长占用路由器有限存储空间
- **status统计信息**: `status` 命令新增近三天统计面板，显示回收次数和平均可用内存百分比（基于日志采样数据），同时显示密集监控模式状态

### 修复
- **时间戳时区bug**: 修复路由器系统时区为UTC时，日志时间戳显示比实际时间晚8小时的问题（如19:33显示为11:33）。现在使用固定CST(UTC+8)时区格式化时间戳，不依赖系统时区设置
- **日志轮换mtime bug**: 修复守护进程启动时以O_APPEND打开日志文件会更新mtime，导致日志轮换检测失效的问题。现在在打开文件之前先执行轮换检查

### 技术细节
- Go版本: go1.23.0，CGO_ENABLED=0 静态编译
- aarch64二进制大小: ~1.9MB（stripped）
- 密集监控参数: 持续5分钟(300秒)，间隔2秒
- 日志轮换周期: 3天
- 已在小米AX3000T(MT7981B) OpenWrt 18.06上实测验证

## [1.0.1] - 2026-09-06

### 修复
- **严重bug修复**: `getPidFilePath()` 函数在测试路径可写性时会创建并删除PID文件，导致 `status` 和 `stop` 命令每次调用都会删除正在运行的进程的PID文件
- 新增 `findExistingPidFile()` 函数，无副作用地查找已存在的PID文件
- `showStatus()`、`stopDaemon()`、`main()` start分支改用 `findExistingPidFile()`，不再触碰PID文件
- `getPidFilePath()` 改用临时文件测试目录可写性，不再触碰真实PID文件
- 增加 `stopDaemon()` 等待时间从3秒到5秒，给信号处理更多响应时间
- 修复 `restart` 命令的可靠性，现在会等待启动完成并报告错误

### 新增
- 新增 `memfixctl` 管理脚本（`scripts/memfixctl`），专为小米路由器设计
  - 直接用ps管理进程，不依赖PID文件
  - 兼容BusyBox sh（不使用local关键字）
  - 支持start/stop/restart/status/once
  - 自动复制配置文件到/etc（ramfs）
  - 可配合crontab实现开机自启和进程守护
- Go版本升级为主要交付版本（静态编译，无外部依赖）

### 技术细节
- Go版本: go1.23.0，CGO_ENABLED=0 静态编译
- aarch64二进制大小: ~1.7MB（stripped）
- 运行时RSS: ~2.3MB
- 已在小米AX3000T(MT7981B) OpenWrt 18.06上实测验证

## [1.0.0] - 2026-08-30

### 新增
- 初始版本发布
- 内存监控功能（读取/proc/meminfo）
- 三级内存回收策略：
  - drop_caches 释放页缓存
  - compact_memory 压缩Slab
  - 可选的进程重启
- 守护进程模式（daemonize）
- 配置文件支持（/etc/memfix.conf）
- 完整的日志系统（/var/log/memfix.log）
- 命令行接口：start/stop/restart/status/once/foreground
- OpenWrt/procd 启动脚本
- Makefile 构建系统（支持本地编译和交叉编译）
- 静态编译支持（musl工具链）
- 完整的文档（README、使用文档、开发文档）
- GitHub Actions CI/CD 工作流

### 技术细节
- 纯C实现，C11标准
- 无外部库依赖
- 静态编译后二进制约 50KB
- 运行时RSS < 200KB
- 支持aarch64和x86_64架构

[Unreleased]: https://github.com/kk3432/memfix/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/kk3432/memfix/releases/tag/v1.1.0
[1.0.1]: https://github.com/kk3432/memfix/releases/tag/v1.0.1
[1.0.0]: https://github.com/kk3432/memfix/releases/tag/v1.0.0
