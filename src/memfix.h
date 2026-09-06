/*
 * memfix.h - 小米AX3000T(联发科版) 内存泄漏监控修复工具
 *
 * 头文件：类型定义、常量、函数声明
 *
 * 版本: 1.0.0
 * 许可证: MIT
 */

#ifndef MEMFIX_H
#define MEMFIX_H

#include <stddef.h>
#include <signal.h>

/* ========== 版本信息 ========== */
#define MEMFIX_VERSION      "1.0.0"
#define MEMFIX_PROJECT      "memfix"
#define MEMFIX_DESCRIPTION  "小米AX3000T(联发科版) 内存泄漏监控修复工具"

/* ========== 默认配置常量 ========== */
#define DEFAULT_CHECK_INTERVAL      30      /* 默认检查间隔(秒) */
#define DEFAULT_MEM_THRESHOLD       15      /* 默认可用内存阈值(百分比) */
#define DEFAULT_RESTART_PROC        ""      /* 默认不重启进程 */

/* ========== 文件路径 ========== */
#define LOG_FILE_PATH               "/var/log/memfix.log"
#define LOG_FILE_FALLBACK           "/tmp/memfix.log"
#define PID_FILE_PATH               "/var/run/memfix.pid"
#define CONFIG_FILE_PATH            "/etc/memfix.conf"

/* ========== 缓冲区大小 ========== */
#define MAX_LINE_LEN                512
#define MAX_PROC_NAME               64
#define MAX_CONFIG_KEY              64
#define MAX_CONFIG_VALUE            256

/* ========== 配置结构体 ========== */
typedef struct {
    int  check_interval;          /* 检查间隔(秒) */
    int  mem_threshold;           /* 可用内存阈值(%) */
    char restart_proc[MAX_PROC_NAME];  /* 需要重启的进程名 */
    int  enable_drop_caches;      /* 是否启用drop_caches */
    int  enable_slab_compact;     /* 是否启用slab压缩 */
    int  verbose;                 /* 详细日志 */
} memfix_config_t;

/* ========== 内存信息结构体 ========== */
typedef struct {
    long total_kb;                /* 总内存(KB) */
    long available_kb;            /* 可用内存(KB) */
    long free_kb;                 /* 空闲内存(KB) */
    long cached_kb;               /* 缓存内存(KB) */
    long buffers_kb;              /* 缓冲区内存(KB) */
    long slab_kb;                 /* Slab内存(KB) */
} mem_info_t;

/* ========== 全局变量（在memfix.c中定义） ========== */
extern memfix_config_t g_config;
extern volatile sig_atomic_t g_running;

/* ========== 函数声明 ========== */

/* 日志 */
void log_msg(const char *level, const char *fmt, ...);

/* 信号处理 */
void signal_handler(int sig);

/* 内存信息 */
int read_mem_info(mem_info_t *info);

/* 内存回收操作 */
int write_sysctl(const char *path, const char *value);
int do_drop_caches(void);
int do_slab_compact(void);
int restart_process(const char *proc_name);
int find_process_pid(const char *proc_name);
int get_process_name_by_pid(int pid, char *name, size_t len);

/* 核心逻辑 */
int check_and_recover(void);

/* 配置 */
void load_config(const char *path);
void init_default_config(void);

/* 守护进程 */
int daemonize(void);

/* 运行模式 */
int run_foreground(void);
int run_once(void);
int show_status(void);
int stop_daemon(void);

/* 帮助 */
void usage(const char *prog);

#endif /* MEMFIX_H */
