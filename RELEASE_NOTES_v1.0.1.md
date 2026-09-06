# memfix v1.0.1 发布说明

> 小米AX3000T（联发科MT7981B版）内存泄漏监控修复工具

**发布日期**：2026-09-06
**版本类型**：Bug修复版本（推荐所有v1.0.0用户升级）
**仓库地址**：https://github.com/kk3432/memfix

---

## 概述

v1.0.1 修复了一个严重的PID文件管理bug，该bug会导致 `status` 和 `stop` 命令每次调用时删除正在运行的守护进程的PID文件，使进程状态检测失效。同时新增了专为小米路由器设计的 `memfixctl` 管理脚本，提升了部署和运维的可靠性。

---

## 重要修复

### 🔴 严重bug：PID文件被误删

**问题描述**：
`getPidFilePath()` 函数在测试路径可写性时，会创建并删除PID文件本身。导致每次调用 `memfix status` 或 `memfix stop` 时，正在运行的进程的PID文件被删除，后续状态检测显示"未运行"。

**影响范围**：
- 所有v1.0.0用户
- `status` 命令无法正确显示运行状态
- `stop` 命令可能无法找到进程PID
- 重复启动可能产生多个进程实例

**修复方案**：
- 新增 `findExistingPidFile()` 函数，无副作用地查找已存在的PID文件
- `getPidFilePath()` 改用临时文件（`.memfix_write_test`）测试目录可写性，不再触碰真实PID文件
- `showStatus()`、`stopDaemon()`、`main()` start分支全部改用 `findExistingPidFile()`

**验证结果**：
连续调用3次 `memfix status`，PID文件始终存在：
```
初始PID文件        ✅ 存在，PID=13227
第1次status后     ✅ PID文件还在
第2次status后     ✅ PID文件还在
第3次status后     ✅ PID文件还在
```

---

## 其他修复与改进

### 🟡 功能改进

| 改进项 | 说明 |
|--------|------|
| `stopDaemon()` 等待时间 | 从3秒增加到5秒，给信号处理更多响应时间，减少强制杀死的概率 |
| `restart` 命令重写 | 现在会等待启动完成并报告错误，之前的实现可能启动失败但无提示 |
| 错误处理增强 | 多处增加错误返回值检查，避免静默失败 |

### 🟢 新增功能

#### `memfixctl` 管理脚本

专为小米路由器（OpenWrt/BusyBox环境）设计的封装管理脚本，位于仓库根目录：

- **直接用ps管理进程**，不依赖PID文件，彻底绕开PID文件相关问题
- **兼容BusyBox sh**，不使用 `local` 关键字等bash特性
- **自动复制配置**到 `/etc/memfix.conf`（小米固件 `/etc` 是ramfs，重启丢失）
- **支持命令**：`start` / `stop` / `restart` / `status` / `once`
- **可配合crontab**实现开机自启和进程守护：
  ```bash
  * * * * * /userdisk/.memfix/memfixctl start >/dev/null 2>&1
  ```

---

## 下载资产

| 文件名 | 大小 | 说明 |
|--------|------|------|
| `memfix-1.0.1-aarch64.tar.gz` | 755 KB | 完整发布包（二进制+源码+文档+脚本） |
| `memfix-1.0.1-src.tar.gz` | 24 KB | 纯源代码包（Go+C双版本） |
| `memfix` | 1.7 MB | 单独的aarch64静态二进制（可直接上传路由器） |

下载地址：https://github.com/kk3432/memfix/releases/tag/v1.0.1

### SHA256 校验和

```
memfix-1.0.1-aarch64.tar.gz: ef47d344cc97f5eff1d2508f7abb4c45862fe4e5f69db4e67be9491da1007a41
memfix-1.0.1-src.tar.gz:     202c80d8d7a0e0e3fdf5f389bd5baccc43eb3cd10c6531d8c8bb5e04585baf73
memfix (aarch64):             5c7b0fb074b4e88ab205cb15217896551c1b3f9091e76b3a0e671fc313ad5331
```

---

## 升级指南

### 从 v1.0.0 升级

```bash
# 1. SSH登录路由器
ssh root@192.168.1.58

# 2. 停止旧进程
/userdisk/.memfix/memfix stop 2>/dev/null
pkill -f "/userdisk/.memfix/memfix" 2>/dev/null

# 3. 备份配置（可选，升级脚本会自动备份）
cp /userdisk/.memfix/memfix.conf /userdisk/.memfix/memfix.conf.bak

# 4. 上传新二进制（scp不可用时用管道）
# scp memfix root@192.168.1.58:/userdisk/.memfix/memfix
cat memfix | ssh root@192.168.1.58 'cat > /userdisk/.memfix/memfix'

# 5. 上传memfixctl管理脚本
cat memfixctl | ssh root@192.168.1.58 'cat > /userdisk/.memfix/memfixctl'

# 6. 赋权
ssh root@192.168.1.58 'chmod +x /userdisk/.memfix/memfix /userdisk/.memfix/memfixctl'

# 7. 启动
ssh root@192.168.1.58 '/userdisk/.memfix/memfixctl start'

# 8. 验证（连续调用多次，PID文件不应被删除）
ssh root@192.168.1.58 '/userdisk/.memfix/memfix status'
ssh root@192.168.1.58 '/userdisk/.memfix/memfix status'
ssh root@192.168.1.58 'ls -la /var/run/memfix.pid'
```

### 全新安装

参考项目README中的[安装指南](https://github.com/kk3432/memfix#安装)。

---

## 兼容性

| 项目 | 兼容情况 |
|------|----------|
| 小米AX3000T（联发科MT7981B） | ✅ 完全兼容，已实测 |
| 小米官方固件（OpenWrt 18.06魔改） | ✅ 完全兼容 |
| 标准OpenWrt | ✅ 兼容 |
| 架构 | aarch64 (ARM64) |
| 内核 | Linux 5.4+ |
| 内存 | 256MB（推荐阈值15%） |

---

## 技术细节

- **编译环境**：Go 1.23.0，`CGO_ENABLED=0` 静态编译
- **二进制格式**：ELF 64-bit LSB executable, ARM aarch64, statically linked, stripped
- **运行时内存**：RSS ~2.3 MB
- **虚拟内存**：~1.2 GB（Go运行时正常的地址空间预留，不占用实际物理内存）
- **配置文件**：兼容v1.0.0，无需修改

---

## 已知限制

1. **C版本二进制**：本次发布未包含C版本编译产物（交叉编译工具链获取困难），C版本源码已包含在发布包中，用户可自行编译。
2. **日志持久化**：日志默认写入 `/var/log/memfix.log`（tmpfs），路由器重启后日志丢失，属正常现象。
3. **进程重启功能**：`restart_process` 配置项默认留空，启用后重启WiFi相关进程会导致短暂断网（2-5秒）。

---

## 反馈与贡献

- 提交Issue：https://github.com/kk3432/memfix/issues
- 贡献代码：欢迎提交Pull Request
- 开发文档：见仓库 `docs/DEVELOPMENT.md`

---

## 许可证

MIT License — 详见 [LICENSE](https://github.com/kk3432/memfix/blob/main/LICENSE)
