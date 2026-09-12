# memfix

小米AX3000T（联发科MT7981B版）路由器内存泄漏监控修复工具。

轻量级外挂程序，以守护进程方式后台运行，定期监控内存状态，自动执行回收操作，缓解官方固件内存泄漏导致的断流、卡顿问题。

**当前版本：v1.1.0**

---

## 快速安装（一键脚本）

SSH登录路由器后，复制粘贴以下命令即可自动下载、安装、配置自启并启动：

```bash
wget -qO- https://gh-proxy.com/https://raw.githubusercontent.com/kk3432/memfix/main/install_xiaomi.sh | sh
```

**指定版本安装：**

```bash
MEMFIX_VERSION=v1.1.0 wget -qO- https://gh-proxy.com/https://raw.githubusercontent.com/kk3432/memfix/main/install_xiaomi.sh | sh
```

> 下载通过 `gh-proxy.com` 加速，解决国内访问 GitHub 慢的问题。
> 脚本自动完成：检测架构 → 下载发布包 → 解压安装 → 配置crontab自启 → 启动服务 → 验证运行。

安装完成后验证：

```bash
/userdisk/.memfix/memfixctl status
```

---

## 技术细节

### 工作原理

```
定期检查 /proc/meminfo → 可用内存低于阈值 → 三级回收 → 记录日志 → 继续循环
                          ↓
              触发回收后进入密集监控模式
              （5分钟内每2秒检测一次）
```

### 三级回收策略

| 级别 | 操作 | 原理 | 安全性 |
|------|------|------|--------|
| 1 | `echo 3 > /proc/sys/vm/drop_caches` | 释放页缓存、目录项、inode缓存（干净缓存可随时重建） | 极高 |
| 2 | `echo 1 > /proc/sys/vm/compact_memory` | 压缩Slab分配器，整理内存碎片，提升利用率 | 高 |
| 3 | 重启指定进程（可选） | 杀掉泄漏进程，释放其堆内存，由init系统自动重启 | 中（短暂断网） |

> 前两级回收缓存和碎片，第三级（需手动配置）才能释放进程堆内存中真正泄漏的部分。默认不启用进程重启。

### 程序特性

- **静态编译**：单文件，无外部依赖，直接复制即可运行
- **场景调控**：触发回收后5分钟内每2秒密集检测，快速捕捉二次泄漏
- **单次强制回收**：`once` 命令不论内存多少都强制执行完整回收
- **日志自动轮换**：每3天自动轮换，避免日志占满存储空间
- **近三天统计**：`status` 显示近三天回收次数和平均可用内存
- **CST时区修复**：固定使用UTC+8时间戳，不依赖路由器系统时区设置
- **日志路径可配置**：通过 `log_file` 配置项指定，支持持久化分区存储
- **路径自适应**：`/var/run` 不可写时自动回退到 `/tmp`（适配小米固件tmpfs）
- **守护进程**：两次fork + setsid，SSH断开不影响运行
- **低资源占用**：ARM aarch64二进制约1.9MB，运行时内存约2.5MB
- **PID文件安全**：v1.0.1修复了status/stop命令误删PID文件的严重bug

### 技术栈

- 主版本：Go 1.23（静态编译，`CGO_ENABLED=0`）
- 同时提供：C语言版本源码（编译后约50KB，需aarch64交叉编译工具链）
- 目标架构：ARM aarch64（MT7981B / Filogic 820）
- 适配系统：小米官方固件 / OpenWrt / 标准Linux

---

## 手动安装

### 前置条件

- 小米AX3000T（联发科版）或其他aarch64架构路由器
- 已开启SSH，有root权限
- 确认架构：`uname -m` 应输出 `aarch64`

> **重要**：小米官方固件根文件系统是squashfs只读，`/usr/sbin` 等系统目录不可写。程序必须安装到持久化可写分区 `/userdisk/.memfix/`。

### 安装步骤

```bash
# 1. SSH登录路由器
ssh root@192.168.1.58

# 2. 创建安装目录（持久化分区）
mkdir -p /userdisk/.memfix

# 3. 下载发布包（通过gh-proxy加速）
cd /tmp
wget https://gh-proxy.com/https://github.com/kk3432/memfix/releases/latest/download/memfix-aarch64.tar.gz

# 4. 解压
tar xzf memfix-aarch64.tar.gz
cd memfix-* 2>/dev/null || cd .

# 5. 复制文件到安装目录
cp memfix memfix.conf memfixctl /userdisk/.memfix/
chmod +x /userdisk/.memfix/memfix /userdisk/.memfix/memfixctl

# 6. 启动（用memfixctl，自动复制配置到/etc）
/userdisk/.memfix/memfixctl start

# 7. 验证
/userdisk/.memfix/memfixctl status

# 8. 开机自启（crontab方式，每分钟检查并自动拉起）
echo '* * * * * /userdisk/.memfix/memfixctl start >/dev/null 2>&1' >> /etc/crontabs/root
/etc/init.d/cron restart
```

### 常用命令（推荐使用 memfixctl）

```bash
/userdisk/.memfix/memfixctl start    # 启动
/userdisk/.memfix/memfixctl stop     # 停止
/userdisk/.memfix/memfixctl restart  # 重启
/userdisk/.memfix/memfixctl status   # 查看状态、内存、近三天统计、日志
/userdisk/.memfix/memfixctl once     # 单次强制回收（不论内存多少）

# 也可以直接调用memfix二进制
/userdisk/.memfix/memfix start
/userdisk/.memfix/memfix status
/userdisk/.memfix/memfix stop
/userdisk/.memfix/memfix once        # 单次强制回收
```

### 配置文件

路径：`/userdisk/.memfix/memfix.conf`（持久化），启动时自动复制到 `/etc/memfix.conf`

```ini
# 内存检查间隔（秒）
check_interval = 30

# 可用内存阈值（%），低于此值触发回收
# 256MB内存建议15-20
mem_threshold = 15

# 内存不足时重启的进程名（留空不重启）
# 小米固件常见泄漏进程: wapi_assist, mcpd, hostapd
# 注意: 重启WiFi进程会导致短暂断网(2-5秒)
restart_process =

# 是否释放页缓存（1=启用，0=禁用）
enable_drop_caches = 1

# 是否压缩slab（1=启用，0=禁用）
enable_slab_compact = 1

# 详细日志（1=启用，0=仅警告）
verbose = 1

# 日志文件路径（v1.1.0新增）
# /tmp/ 是tmpfs，重启后日志丢失
# /userdisk/.memfix/ 是持久化分区(ubifs)，重启不丢失
log_file = /userdisk/.memfix/memfix.log
```

修改后执行 `/userdisk/.memfix/memfixctl restart` 生效。

### 卸载

```bash
/userdisk/.memfix/memfixctl stop
rm -rf /userdisk/.memfix
rm -f /etc/memfix.conf
sed -i '/memfix/d' /etc/crontabs/root
```

---

## 许可证

MIT License

Copyright (c) 2026 memfix contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
