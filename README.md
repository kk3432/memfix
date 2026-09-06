# memfix

小米AX3000T（联发科MT7981B版）路由器内存泄漏监控修复工具。

轻量级外挂程序，以守护进程方式后台运行，定期监控内存状态，自动执行回收操作，缓解官方固件内存泄漏导致的断流、卡顿问题。

**当前版本：v1.0.1**

---

## 技术细节

### 工作原理

```
定期检查 /proc/meminfo → 可用内存低于阈值 → 三级回收 → 记录日志 → 继续循环
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
- **路径自适应**：`/var/run`、`/var/log` 不可写时自动回退到 `/tmp`（适配小米固件tmpfs）
- **守护进程**：两次fork + setsid，SSH断开不影响运行
- **低资源占用**：ARM aarch64二进制1.7MB，运行时内存约2.3MB
- **完整日志**：所有操作记录到日志文件，便于排查
- **PID文件安全**：v1.0.1修复了status/stop命令误删PID文件的严重bug

### 技术栈

- 主版本：Go 1.23（静态编译，`CGO_ENABLED=0`）
- 同时提供：C语言版本源码（编译后约50KB，需aarch64交叉编译工具链）
- 目标架构：ARM aarch64（MT7981B / Filogic 820）
- 适配系统：小米官方固件 / OpenWrt / 标准Linux

---

## 安装

### 前置条件

- 小米AX3000T（联发科版）或其他aarch64架构路由器
- 已开启SSH，有root权限
- 确认架构：`uname -m` 应输出 `aarch64`

> **重要**：小米官方固件根文件系统是squashfs只读，`/usr/sbin` 等系统目录不可写。程序必须安装到持久化可写分区 `/userdisk/.memfix/`。

### 方法一：手动安装（推荐，小米固件专用）

```bash
# 1. 电脑端上传文件到路由器（scp或管道方式）
# 如果scp不可用（路由器无sftp-server），用管道：
# cat memfix | ssh root@192.168.1.58 'cat > /userdisk/.memfix/memfix'

# 2. SSH登录路由器
ssh root@192.168.1.58

# 3. 创建安装目录（持久化分区）
mkdir -p /userdisk/.memfix

# 4. 上传以下文件到 /userdisk/.memfix/：
#    - memfix          （主程序二进制）
#    - memfix.conf     （配置文件）
#    - memfixctl       （管理脚本）

# 5. 赋权
chmod +x /userdisk/.memfix/memfix /userdisk/.memfix/memfixctl

# 6. 启动（用memfixctl，自动复制配置到/etc）
/userdisk/.memfix/memfixctl start

# 7. 验证
/userdisk/.memfix/memfixctl status

# 8. 开机自启（crontab方式，每分钟检查并自动拉起）
echo '* * * * * /userdisk/.memfix/memfixctl start >/dev/null 2>&1' >> /etc/crontabs/root
/etc/init.d/cron restart
```

### 方法二：通用安装（标准OpenWrt）

```bash
# 1. 上传发布包
scp memfix-1.0.1-aarch64.tar.gz root@192.168.1.1:/tmp/

# 2. SSH登录并解压
ssh root@192.168.1.1
cd /tmp
tar xzf memfix-1.0.1-aarch64.tar.gz

# 3. 运行安装脚本
chmod +x install.sh
./install.sh
```

### 常用命令（推荐使用 memfixctl）

```bash
# 小米固件推荐用 memfixctl（直接用ps管理进程，更可靠）
/userdisk/.memfix/memfixctl start    # 启动
/userdisk/.memfix/memfixctl stop     # 停止
/userdisk/.memfix/memfixctl restart  # 重启
/userdisk/.memfix/memfixctl status   # 查看状态、内存、日志
/userdisk/.memfix/memfixctl once     # 单次检查回收

# 也可以直接调用memfix二进制（v1.0.1已修复PID文件bug）
/userdisk/.memfix/memfix start
/userdisk/.memfix/memfix status
/userdisk/.memfix/memfix stop
```

### 配置文件

路径：`/userdisk/.memfix/memfix.conf`（持久化），启动时自动复制到 `/etc/memfix.conf`

```ini
check_interval = 30          # 检查间隔（秒）
mem_threshold = 15           # 可用内存阈值（%），低于此值触发回收
restart_process =            # 泄漏进程名（留空不重启，如 wapi_assist）
enable_drop_caches = 1       # 启用页缓存回收
enable_slab_compact = 1      # 启用Slab压缩
verbose = 1                  # 详细日志
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
