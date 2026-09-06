/*
 * memfix.c - 小米AX3000T(联发科版) 内存泄漏监控修复工具
 *
 * 功能说明：
 *   1. 作为守护进程后台运行，定期监控系统内存使用
 *   2. 当可用内存低于阈值时，自动执行内存回收：
 *      - 写入 drop_caches 释放页缓存/目录项/inode缓存
 *      - 压缩 slab 分配器缓存
 *      - 重启已知存在内存泄漏的进程（可选）
 *   3. 记录操作日志，支持配置文件
 *   4. 轻量级，自身内存占用 < 200KB
 *
 * 编译：aarch64-linux-musl-gcc -static -O2 -o memfix memfix.c
 */

#define _GNU_SOURCE
#define _POSIX_C_SOURCE 200809L

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <signal.h>
#include <time.h>
#include <errno.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <fcntl.h>
#include <stdarg.h>

/* ========== 配置常量 ========== */
#define DEFAULT_CHECK_INTERVAL  30      /* 默认检查间隔(秒) */
#define DEFAULT_MEM_THRESHOLD   15      /* 默认可用内存阈值(百分比) */
#define DEFAULT_RESTART_PROC    ""      /* 默认不重启进程 */
#define LOG_FILE                "/var/log/memfix.log"
#define PID_FILE_PATH           "/var/run/memfix.pid"
#define PID_FILE                (g_pid_file)
#define CONFIG_FILE             "/etc/memfix.conf"

/* 全局PID文件路径（运行时确定，优先/var/run，回退/tmp） */
static char g_pid_file[256] = "/var/run/memfix.pid";

/* 获取可用的PID文件路径（优先/var/run，回退/tmp） */
const char *get_pid_file_path(void) {
    FILE *fp = fopen("/var/run/memfix.pid", "a");
    if (fp) {
        fclose(fp);
        unlink("/var/run/memfix.pid");
        snprintf(g_pid_file, sizeof(g_pid_file), "%s", "/var/run/memfix.pid");
    } else {
        snprintf(g_pid_file, sizeof(g_pid_file), "/tmp/memfix.pid");
    }
    return g_pid_file;
}
#define MAX_LINE_LEN            512
#define MAX_PROC_NAME           64

/* ========== 全局配置 ========== */
typedef struct {
    int  check_interval;     /* 检查间隔(秒) */
    int  mem_threshold;      /* 可用内存阈值(%) */
    char restart_proc[MAX_PROC_NAME];  /* 需要重启的进程名 */
    int  enable_drop_caches; /* 是否启用drop_caches */
    int  enable_slab_compact;/* 是否启用slab压缩 */
    int  verbose;            /* 详细日志 */
} memfix_config_t;

static memfix_config_t g_config;
static volatile sig_atomic_t g_running = 1;
static FILE *g_logfp = NULL;

/* ========== 日志函数 ========== */
void log_msg(const char *level, const char *fmt, ...) {
    if (!g_logfp) return;
    time_t now = time(NULL);
    struct tm *tm = localtime(&now);
    char timebuf[32];
    strftime(timebuf, sizeof(timebuf), "%Y-%m-%d %H:%M:%S", tm);

    va_list ap;
    va_start(ap, fmt);
    fprintf(g_logfp, "[%s] [%s] ", timebuf, level);
    vfprintf(g_logfp, fmt, ap);
    fprintf(g_logfp, "\n");
    va_end(ap);
    fflush(g_logfp);
}

/* ========== 信号处理 ========== */
void signal_handler(int sig) {
    if (sig == SIGTERM || sig == SIGINT) {
        g_running = 0;
        log_msg("INFO", "收到退出信号(%d)，正在停止...", sig);
    } else if (sig == SIGHUP) {
        log_msg("INFO", "收到SIGHUP，重新加载配置");
        /* 重新加载配置 */
    }
}

/* ========== 读取内存信息 ========== */
typedef struct {
    long total_kb;
    long available_kb;
    long free_kb;
    long cached_kb;
    long buffers_kb;
    long slab_kb;
} mem_info_t;

int read_mem_info(mem_info_t *info) {
    FILE *fp = fopen("/proc/meminfo", "r");
    if (!fp) {
        log_msg("ERROR", "无法打开 /proc/meminfo: %s", strerror(errno));
        return -1;
    }

    char line[256];
    memset(info, 0, sizeof(*info));

    while (fgets(line, sizeof(line), fp)) {
        if (strncmp(line, "MemTotal:", 9) == 0) {
            sscanf(line + 9, "%ld", &info->total_kb);
        } else if (strncmp(line, "MemAvailable:", 13) == 0) {
            sscanf(line + 13, "%ld", &info->available_kb);
        } else if (strncmp(line, "MemFree:", 8) == 0) {
            sscanf(line + 8, "%ld", &info->free_kb);
        } else if (strncmp(line, "Cached:", 7) == 0) {
            sscanf(line + 7, "%ld", &info->cached_kb);
        } else if (strncmp(line, "Buffers:", 8) == 0) {
            sscanf(line + 8, "%ld", &info->buffers_kb);
        } else if (strncmp(line, "Slab:", 5) == 0) {
            sscanf(line + 5, "%ld", &info->slab_kb);
        }
    }
    fclose(fp);

    if (info->total_kb == 0) {
        log_msg("ERROR", "读取内存信息失败: MemTotal为0");
        return -1;
    }
    return 0;
}

/* ========== 执行内存回收 ========== */
int write_sysctl(const char *path, const char *value) {
    int fd = open(path, O_WRONLY);
    if (fd < 0) {
        log_msg("ERROR", "无法打开 %s: %s", path, strerror(errno));
        return -1;
    }
    ssize_t len = write(fd, value, strlen(value));
    close(fd);
    if (len < 0) {
        log_msg("ERROR", "写入 %s 失败: %s", path, strerror(errno));
        return -1;
    }
    return 0;
}

/* 释放页缓存、目录项和inode缓存 */
int do_drop_caches(void) {
    log_msg("INFO", "执行 drop_caches (释放页缓存+目录项+inode)");
    /* 先同步，确保脏页写回 */
    sync();
    usleep(100000); /* 100ms */
    /* 3 = 释放页缓存 + 目录项 + inode */
    return write_sysctl("/proc/sys/vm/drop_caches", "3");
}

/* 压缩slab分配器 */
int do_slab_compact(void) {
    log_msg("INFO", "执行 slab 压缩 (compact_memory)");
    return write_sysctl("/proc/sys/vm/compact_memory", "1");
}

/* 通过pid查找进程名 */
int get_process_name_by_pid(int pid, char *name, size_t len) {
    char path[64];
    snprintf(path, sizeof(path), "/proc/%d/comm", pid);
    FILE *fp = fopen(path, "r");
    if (!fp) return -1;
    if (fgets(name, len, fp) == NULL) {
        fclose(fp);
        return -1;
    }
    fclose(fp);
    /* 去掉换行符 */
    name[strcspn(name, "\n")] = '\0';
    return 0;
}

/* 查找指定名称进程的PID */
int find_process_pid(const char *proc_name) {
    char comm[64];
    int pid;

    /* 遍历 /proc 下的数字目录 */
    FILE *fp = popen("ls /proc | grep -E '^[0-9]+$'", "r");
    if (!fp) return -1;

    char pidstr[16];
    while (fgets(pidstr, sizeof(pidstr), fp)) {
        pid = atoi(pidstr);
        if (pid <= 0) continue;
        if (get_process_name_by_pid(pid, comm, sizeof(comm)) == 0) {
            if (strcmp(comm, proc_name) == 0) {
                pclose(fp);
                return pid;
            }
        }
    }
    pclose(fp);
    return -1;
}

/* 重启指定进程（通过发送SIGTERM，依赖init系统重启） */
int restart_process(const char *proc_name) {
    if (strlen(proc_name) == 0) return 0;

    int pid = find_process_pid(proc_name);
    if (pid < 0) {
        log_msg("WARN", "未找到进程: %s", proc_name);
        return -1;
    }

    log_msg("INFO", "重启进程 %s (PID=%d)", proc_name, pid);

    /* 发送SIGTERM优雅终止 */
    if (kill(pid, SIGTERM) < 0) {
        log_msg("ERROR", "发送SIGTERM失败: %s", strerror(errno));
        return -1;
    }

    /* 等待进程退出，最多5秒 */
    for (int i = 0; i < 50; i++) {
        usleep(100000); /* 100ms */
        if (kill(pid, 0) < 0 && errno == ESRCH) {
            log_msg("INFO", "进程 %s 已终止", proc_name);
            break;
        }
    }

    /* 如果还在，强制杀死 */
    if (kill(pid, 0) == 0) {
        log_msg("WARN", "进程 %s 未响应SIGTERM，发送SIGKILL", proc_name);
        kill(pid, SIGKILL);
    }

    /* 等待init/procd重启进程 */
    usleep(2000000); /* 2秒 */

    int new_pid = find_process_pid(proc_name);
    if (new_pid > 0) {
        log_msg("INFO", "进程 %s 已重启，新PID=%d", proc_name, new_pid);
        return 0;
    } else {
        log_msg("WARN", "进程 %s 未自动重启，请检查init配置", proc_name);
        return -1;
    }
}

/* ========== 内存检查与修复主逻辑 ========== */
int check_and_recover(void) {
    mem_info_t info;
    if (read_mem_info(&info) < 0) return -1;

    int avail_percent = (int)((info.available_kb * 100) / info.total_kb);

    if (g_config.verbose) {
        log_msg("DEBUG",
                "内存状态: 总计=%ldMB 可用=%ldMB(%d%%) 空闲=%ldMB 缓存=%ldMB Slab=%ldMB",
                info.total_kb / 1024, info.available_kb / 1024, avail_percent,
                info.free_kb / 1024, info.cached_kb / 1024, info.slab_kb / 1024);
    }

    if (avail_percent <= g_config.mem_threshold) {
        log_msg("WARN",
                "可用内存过低: %d%% (阈值=%d%%)，开始内存回收",
                avail_percent, g_config.mem_threshold);

        int recovered = 0;

        /* 步骤1: 释放页缓存 */
        if (g_config.enable_drop_caches) {
            if (do_drop_caches() == 0) recovered++;
            usleep(500000); /* 500ms间隔 */
        }

        /* 步骤2: 压缩slab */
        if (g_config.enable_slab_compact) {
            if (do_slab_compact() == 0) recovered++;
            usleep(500000);
        }

        /* 步骤3: 重启泄漏进程（如果配置了） */
        if (strlen(g_config.restart_proc) > 0) {
            /* 先检查回收后内存是否仍然不足 */
            mem_info_t after;
            if (read_mem_info(&after) == 0) {
                int after_percent = (int)((after.available_kb * 100) / after.total_kb);
                if (after_percent <= g_config.mem_threshold) {
                    log_msg("INFO", "缓存回收后可用内存仍为%d%%，尝试重启进程 %s",
                            after_percent, g_config.restart_proc);
                    restart_process(g_config.restart_proc);
                    recovered++;
                } else {
                    log_msg("INFO", "缓存回收后可用内存恢复到%d%%，无需重启进程", after_percent);
                }
            }
        }

        /* 记录回收后状态 */
        mem_info_t final;
        if (read_mem_info(&final) == 0) {
            int final_percent = (int)((final.available_kb * 100) / final.total_kb);
            long freed_kb = final.available_kb - info.available_kb;
            log_msg("INFO", "内存回收完成: 可用内存 %d%% -> %d%%，释放约 %ldMB",
                    avail_percent, final_percent, freed_kb / 1024);
        }

        return recovered;
    }

    return 0;
}

/* ========== 配置文件加载 ========== */
void load_config(const char *path) {
    FILE *fp = fopen(path, "r");
    if (!fp) {
        log_msg("INFO", "未找到配置文件 %s，使用默认配置", path);
        return;
    }

    char line[MAX_LINE_LEN];
    while (fgets(line, sizeof(line), fp)) {
        /* 去掉注释和空白 */
        char *p = line;
        while (*p == ' ' || *p == '\t') p++;
        if (*p == '#' || *p == '\n' || *p == '\0') continue;

        char key[64], value[256];
        if (sscanf(p, "%63[^= \t]=%255[^\n]", key, value) >= 2) {
            /* 去掉value两端空白 */
            char *vstart = value;
            while (*vstart == ' ' || *vstart == '\t') vstart++;
            char *vend = vstart + strlen(vstart) - 1;
            while (vend > vstart && (*vend == ' ' || *vend == '\t' || *vend == '\r')) {
                *vend = '\0';
                vend--;
            }

            if (strcmp(key, "check_interval") == 0) {
                g_config.check_interval = atoi(vstart);
            } else if (strcmp(key, "mem_threshold") == 0) {
                g_config.mem_threshold = atoi(vstart);
            } else if (strcmp(key, "restart_process") == 0) {
                snprintf(g_config.restart_proc, MAX_PROC_NAME, "%s", vstart);
            } else if (strcmp(key, "enable_drop_caches") == 0) {
                g_config.enable_drop_caches = atoi(vstart);
            } else if (strcmp(key, "enable_slab_compact") == 0) {
                g_config.enable_slab_compact = atoi(vstart);
            } else if (strcmp(key, "verbose") == 0) {
                g_config.verbose = atoi(vstart);
            }
        }
    }
    fclose(fp);
    log_msg("INFO", "配置已加载: 间隔=%ds 阈值=%d%% 重启进程=%s drop_caches=%d slab_compact=%d verbose=%d",
            g_config.check_interval, g_config.mem_threshold,
            strlen(g_config.restart_proc) ? g_config.restart_proc : "(无)",
            g_config.enable_drop_caches, g_config.enable_slab_compact, g_config.verbose);
}

/* ========== 默认配置 ========== */
void init_default_config(void) {
    g_config.check_interval = DEFAULT_CHECK_INTERVAL;
    g_config.mem_threshold = DEFAULT_MEM_THRESHOLD;
    memset(g_config.restart_proc, 0, sizeof(g_config.restart_proc));
    g_config.enable_drop_caches = 1;
    g_config.enable_slab_compact = 1;
    g_config.verbose = 1;
}

/* ========== 守护进程化 ========== */
int daemonize(void) {
    pid_t pid = fork();
    if (pid < 0) {
        fprintf(stderr, "fork失败: %s\n", strerror(errno));
        return -1;
    }
    if (pid > 0) {
        /* 父进程退出 */
        printf("memfix 已启动，PID=%d\n", pid);
        exit(0);
    }

    /* 创建新会话 */
    if (setsid() < 0) {
        fprintf(stderr, "setsid失败: %s\n", strerror(errno));
        return -1;
    }

    /* 第二次fork，确保无法重新获取控制终端 */
    pid = fork();
    if (pid < 0) return -1;
    if (pid > 0) exit(0);

    /* 改变工作目录 */
    if (chdir("/") < 0) {
        /* 非致命错误 */
    }

    /* 重设文件权限掩码 */
    umask(0);

    /* 关闭标准文件描述符 */
    close(STDIN_FILENO);
    close(STDOUT_FILENO);
    close(STDERR_FILENO);

    /* 打开日志 */
    g_logfp = fopen(LOG_FILE, "a");
    if (!g_logfp) {
        /* 如果/var/log不可写，尝试/tmp */
        g_logfp = fopen("/tmp/memfix.log", "a");
    }

    /* 写入PID文件 */
    const char *pidpath = get_pid_file_path();
    FILE *pidfp = fopen(pidpath, "w");
    if (pidfp) {
        fprintf(pidfp, "%d\n", getpid());
        fclose(pidfp);
    }

    return 0;
}

/* ========== 前台运行模式（用于调试） ========== */
int run_foreground(void) {
    g_logfp = stdout;
    printf("memfix 前台调试模式启动\n");
    printf("配置: 间隔=%ds 阈值=%d%%\n", g_config.check_interval, g_config.mem_threshold);

    while (g_running) {
        check_and_recover();
        /* 分段sleep，便于响应信号 */
        for (int i = 0; i < g_config.check_interval && g_running; i++) {
            sleep(1);
        }
    }
    return 0;
}

/* ========== 单次执行模式 ========== */
int run_once(void) {
    g_logfp = stdout;
    printf("memfix 单次检查模式\n");
    int result = check_and_recover();
    if (result == 0) {
        printf("内存正常，无需回收\n");
    } else if (result > 0) {
        printf("已执行 %d 项内存回收操作\n", result);
    }
    return result;
}

/* ========== 显示状态 ========== */
int show_status(void) {
    /* 检查PID文件（先默认路径，再回退路径） */
    const char *pidpath = PID_FILE_PATH;
    FILE *pidfp = fopen(pidpath, "r");
    if (!pidfp) {
        pidpath = "/tmp/memfix.pid";
        pidfp = fopen(pidpath, "r");
    }
    if (!pidfp) {
        printf("memfix 未运行（无PID文件）\n");
        return 1;
    }
    int pid = 0;
    if (fscanf(pidfp, "%d", &pid) != 1) {
        fclose(pidfp);
        printf("memfix PID文件格式错误\n");
        return 1;
    }
    fclose(pidfp);

    /* 检查进程是否存在 */
    if (pid <= 0 || (kill(pid, 0) < 0 && errno == ESRCH)) {
        printf("memfix 未运行（PID文件存在但进程不存在，PID=%d）\n", pid);
        return 1;
    }

    printf("memfix 正在运行，PID=%d\n", pid);

    /* 显示当前内存状态 */
    mem_info_t info;
    if (read_mem_info(&info) == 0) {
        int avail_percent = (int)((info.available_kb * 100) / info.total_kb);
        printf("当前内存: 总计=%ldMB 可用=%ldMB(%d%%) 空闲=%ldMB 缓存=%ldMB Slab=%ldMB\n",
               info.total_kb / 1024, info.available_kb / 1024, avail_percent,
               info.free_kb / 1024, info.cached_kb / 1024, info.slab_kb / 1024);
    }

    /* 显示最近日志（先默认路径，再回退/tmp） */
    printf("\n最近日志:\n");
    FILE *logfp = fopen(LOG_FILE, "r");
    if (!logfp) {
        logfp = fopen("/tmp/memfix.log", "r");
    }
    if (logfp) {
        char lines[20][MAX_LINE_LEN];
        int count = 0;
        while (fgets(lines[count % 20], MAX_LINE_LEN, logfp)) {
            count++;
        }
        fclose(logfp);
        int start = count > 20 ? count - 20 : 0;
        for (int i = start; i < count; i++) {
            printf("%s", lines[i % 20]);
        }
    } else {
        printf("(无法读取日志文件)\n");
    }

    return 0;
}

/* ========== 停止守护进程 ========== */
int stop_daemon(void) {
    const char *pidpath = PID_FILE_PATH;
    FILE *pidfp = fopen(pidpath, "r");
    if (!pidfp) {
        pidpath = "/tmp/memfix.pid";
        pidfp = fopen(pidpath, "r");
    }
    if (!pidfp) {
        printf("memfix 未运行\n");
        return 1;
    }
    int pid = 0;
    if (fscanf(pidfp, "%d", &pid) != 1) {
        fclose(pidfp);
        printf("memfix PID文件格式错误\n");
        return 1;
    }
    fclose(pidfp);

    if (pid <= 0) {
        printf("无效的PID\n");
        unlink(PID_FILE);
        return 1;
    }

    if (kill(pid, SIGTERM) < 0) {
        if (errno == ESRCH) {
            printf("进程不存在，清理PID文件\n");
            unlink(PID_FILE);
        } else {
            printf("停止失败: %s\n", strerror(errno));
            return 1;
        }
    } else {
        printf("已发送停止信号，等待进程退出...\n");
        for (int i = 0; i < 30; i++) {
            usleep(100000);
            if (kill(pid, 0) < 0 && errno == ESRCH) {
                printf("memfix 已停止\n");
                unlink(PID_FILE);
                return 0;
            }
        }
        printf("进程未响应，强制杀死\n");
        kill(pid, SIGKILL);
        unlink(PID_FILE);
    }
    return 0;
}

/* ========== 使用说明 ========== */
void usage(const char *prog) {
    printf("小米AX3000T(联发科版) 内存泄漏监控修复工具 v1.0\n\n");
    printf("用法: %s [选项]\n\n", prog);
    printf("选项:\n");
    printf("  start        以守护进程方式启动（默认）\n");
    printf("  stop         停止正在运行的守护进程\n");
    printf("  restart      重启守护进程\n");
    printf("  status       显示运行状态和当前内存信息\n");
    printf("  once         执行单次内存检查和回收（前台）\n");
    printf("  foreground   前台运行（调试用，输出到stdout）\n");
    printf("  -h, --help   显示此帮助信息\n\n");
    printf("配置文件: %s\n", CONFIG_FILE);
    printf("日志文件: %s\n", LOG_FILE);
    printf("PID 文件: %s\n", PID_FILE);
    printf("\n配置项说明:\n");
    printf("  check_interval      检查间隔（秒），默认30\n");
    printf("  mem_threshold       可用内存阈值（%%），默认15\n");
    printf("  restart_process     内存不足时重启的进程名（可选）\n");
    printf("  enable_drop_caches  是否释放页缓存（1/0），默认1\n");
    printf("  enable_slab_compact 是否压缩slab（1/0），默认1\n");
    printf("  verbose             详细日志（1/0），默认1\n");
}

/* ========== 主函数 ========== */
int main(int argc, char *argv[]) {
    const char *action = "start";

    /* 初始化PID文件路径（优先/var/run，回退/tmp） */
    get_pid_file_path();

    if (argc > 1) {
        if (strcmp(argv[1], "-h") == 0 || strcmp(argv[1], "--help") == 0) {
            usage(argv[0]);
            return 0;
        }
        action = argv[1];
    }

    /* 初始化默认配置 */
    init_default_config();

    /* 处理不需要配置的命令 */
    if (strcmp(action, "stop") == 0) {
        return stop_daemon();
    }
    if (strcmp(action, "status") == 0) {
        g_logfp = NULL; /* status模式不写日志 */
        return show_status();
    }

    /* 加载配置 */
    load_config(CONFIG_FILE);

    /* 注册信号 */
    signal(SIGTERM, signal_handler);
    signal(SIGINT, signal_handler);
    signal(SIGHUP, signal_handler);
    signal(SIGPIPE, SIG_IGN);

    if (strcmp(action, "start") == 0) {
        /* 检查是否已在运行 */
        FILE *pidfp = fopen(PID_FILE, "r");
        if (pidfp) {
            int pid = 0;
            if (fscanf(pidfp, "%d", &pid) == 1 && pid > 0) {
                fclose(pidfp);
                if (kill(pid, 0) == 0) {
                    printf("memfix 已在运行，PID=%d\n", pid);
                    return 0;
                }
            } else {
                fclose(pidfp);
            }
        }

        if (daemonize() < 0) {
            fprintf(stderr, "守护进程化失败\n");
            return 1;
        }

        log_msg("INFO", "===== memfix 守护进程启动 =====");
        log_msg("INFO", "版本: 1.0 | 目标: 小米AX3000T(MT7981B)");

        while (g_running) {
            check_and_recover();
            /* 分段sleep */
            for (int i = 0; i < g_config.check_interval && g_running; i++) {
                sleep(1);
            }
        }

        log_msg("INFO", "===== memfix 守护进程停止 =====");
        if (g_logfp) fclose(g_logfp);
        unlink(PID_FILE);
        return 0;
    }
    else if (strcmp(action, "foreground") == 0) {
        return run_foreground();
    }
    else if (strcmp(action, "once") == 0) {
        return run_once();
    }
    else if (strcmp(action, "restart") == 0) {
        stop_daemon();
        usleep(1000000);
        /* 重新执行start */
        execl(argv[0], argv[0], "start", NULL);
        return 0;
    }
    else {
        fprintf(stderr, "未知命令: %s\n", action);
        usage(argv[0]);
        return 1;
    }
}
