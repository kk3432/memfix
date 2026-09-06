// memfix - 小米AX3000T(联发科版) 内存泄漏监控修复工具
// Go版本 - 静态编译，无外部依赖
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 版本信息
const (
	Version     = "1.0.1"
	ProjectName = "memfix"
	Description = "小米AX3000T(联发科版) 内存泄漏监控修复工具"
)

// 默认配置常量
const (
	DefaultCheckInterval = 30   // 默认检查间隔(秒)
	DefaultMemThreshold  = 15   // 默认可用内存阈值(百分比)
	DefaultLogFile       = "/var/log/memfix.log"
	DefaultPidFile       = "/var/run/memfix.pid"
	DefaultConfigFile    = "/etc/memfix.conf"
	FallbackLogFile      = "/tmp/memfix.log"
	FallbackPidFile      = "/tmp/memfix.pid"
)

// Config 配置结构体
type Config struct {
	CheckInterval     int    // 检查间隔(秒)
	MemThreshold      int    // 可用内存阈值(%)
	RestartProc       string // 需要重启的进程名
	EnableDropCaches  bool   // 是否启用drop_caches
	EnableSlabCompact bool   // 是否启用slab压缩
	Verbose           bool   // 详细日志
}

// MemInfo 内存信息结构体
type MemInfo struct {
	TotalKB     int64 // 总内存(KB)
	AvailableKB int64 // 可用内存(KB)
	FreeKB      int64 // 空闲内存(KB)
	CachedKB    int64 // 缓存内存(KB)
	BuffersKB   int64 // 缓冲区内存(KB)
	SlabKB      int64 // Slab内存(KB)
}

var (
	config   Config
	logFile  *os.File
	pidFile  string
	running  = true
)

// logMsg 记录日志
func logMsg(level, format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%s] %s\n", now, level, msg)
	logFile.WriteString(line)
	logFile.Sync()
}

// getPidFilePath 获取可用的PID文件路径（无副作用，不删除已存在的文件）
func getPidFilePath() string {
	// 优先检查默认路径的父目录是否可写（用临时文件测试，不触碰真实PID文件）
	testFile := filepath.Join(filepath.Dir(DefaultPidFile), ".memfix_write_test")
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		f.Close()
		os.Remove(testFile)
		return DefaultPidFile
	}
	return FallbackPidFile
}

// findExistingPidFile 查找已存在的PID文件（按优先级顺序）
func findExistingPidFile() string {
	if _, err := os.Stat(DefaultPidFile); err == nil {
		return DefaultPidFile
	}
	if _, err := os.Stat(FallbackPidFile); err == nil {
		return FallbackPidFile
	}
	return ""
}

// getLogFilePath 获取可用的日志文件路径
func getLogFilePath() string {
	f, err := os.OpenFile(DefaultLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		f.Close()
		return DefaultLogFile
	}
	return FallbackLogFile
}

// initDefaultConfig 初始化默认配置
func initDefaultConfig() {
	config = Config{
		CheckInterval:     DefaultCheckInterval,
		MemThreshold:      DefaultMemThreshold,
		RestartProc:       "",
		EnableDropCaches:  true,
		EnableSlabCompact: true,
		Verbose:           true,
	}
}

// loadConfig 加载配置文件
func loadConfig(path string) {
	f, err := os.Open(path)
	if err != nil {
		logMsg("INFO", "未找到配置文件 %s，使用默认配置", path)
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过注释和空行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 解析 key=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "check_interval":
			if v, err := strconv.Atoi(value); err == nil {
				config.CheckInterval = v
			}
		case "mem_threshold":
			if v, err := strconv.Atoi(value); err == nil {
				config.MemThreshold = v
			}
		case "restart_process":
			config.RestartProc = value
		case "enable_drop_caches":
			config.EnableDropCaches = value == "1" || strings.ToLower(value) == "true"
		case "enable_slab_compact":
			config.EnableSlabCompact = value == "1" || strings.ToLower(value) == "true"
		case "verbose":
			config.Verbose = value == "1" || strings.ToLower(value) == "true"
		}
	}

	logMsg("INFO", "配置已加载: 间隔=%ds 阈值=%d%% 重启进程=%s drop_caches=%v slab_compact=%v verbose=%v",
		config.CheckInterval, config.MemThreshold,
		func() string {
			if config.RestartProc == "" {
				return "(无)"
			}
			return config.RestartProc
		}(),
		config.EnableDropCaches, config.EnableSlabCompact, config.Verbose)
}

// readMemInfo 读取内存信息
func readMemInfo() (*MemInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		logMsg("ERROR", "无法打开 /proc/meminfo: %v", err)
		return nil, err
	}
	defer f.Close()

	info := &MemInfo{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		// 提取数字（去掉kB）
		valueStr := strings.TrimSpace(parts[1])
		valueStr = strings.Fields(valueStr)[0]
		value, err := strconv.ParseInt(valueStr, 10, 64)
		if err != nil {
			continue
		}

		switch key {
		case "MemTotal":
			info.TotalKB = value
		case "MemAvailable":
			info.AvailableKB = value
		case "MemFree":
			info.FreeKB = value
		case "Cached":
			info.CachedKB = value
		case "Buffers":
			info.BuffersKB = value
		case "Slab":
			info.SlabKB = value
		}
	}

	if info.TotalKB == 0 {
		return nil, fmt.Errorf("读取内存信息失败: MemTotal为0")
	}
	return info, nil
}

// writeSysctl 写入sysctl
func writeSysctl(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		logMsg("ERROR", "无法打开 %s: %v", path, err)
		return err
	}
	defer f.Close()
	_, err = f.WriteString(value)
	if err != nil {
		logMsg("ERROR", "写入 %s 失败: %v", path, err)
	}
	return err
}

// doDropCaches 释放页缓存
func doDropCaches() error {
	logMsg("INFO", "执行 drop_caches (释放页缓存+目录项+inode)")
	// 先同步
	syscall.Sync()
	time.Sleep(100 * time.Millisecond)
	// 3 = 释放页缓存 + 目录项 + inode
	return writeSysctl("/proc/sys/vm/drop_caches", "3")
}

// doSlabCompact 压缩slab
func doSlabCompact() error {
	logMsg("INFO", "执行 slab 压缩 (compact_memory)")
	return writeSysctl("/proc/sys/vm/compact_memory", "1")
}

// findProcessPid 查找指定名称进程的PID
func findProcessPid(procName string) int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return -1
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		// 读取进程名
		commPath := filepath.Join("/proc", entry.Name(), "comm")
		comm, err := os.ReadFile(commPath)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(string(comm))
		if name == procName {
			return pid
		}
	}
	return -1
}

// restartProcess 重启指定进程
func restartProcess(procName string) error {
	if procName == "" {
		return nil
	}

	pid := findProcessPid(procName)
	if pid < 0 {
		logMsg("WARN", "未找到进程: %s", procName)
		return fmt.Errorf("进程未找到")
	}

	logMsg("INFO", "重启进程 %s (PID=%d)", procName, pid)

	// 发送SIGTERM
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		logMsg("ERROR", "发送SIGTERM失败: %v", err)
		return err
	}

	// 等待进程退出，最多5秒
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if process.Signal(syscall.Signal(0)) != nil {
			logMsg("INFO", "进程 %s 已终止", procName)
			break
		}
	}

	// 如果还在，强制杀死
	if process.Signal(syscall.Signal(0)) == nil {
		logMsg("WARN", "进程 %s 未响应SIGTERM，发送SIGKILL", procName)
		process.Kill()
	}

	// 等待init/procd重启进程
	time.Sleep(2 * time.Second)

	newPid := findProcessPid(procName)
	if newPid > 0 {
		logMsg("INFO", "进程 %s 已重启，新PID=%d", procName, newPid)
		return nil
	}
	logMsg("WARN", "进程 %s 未自动重启，请检查init配置", procName)
	return fmt.Errorf("进程未自动重启")
}

// checkAndRecover 内存检查与修复主逻辑
func checkAndRecover() int {
	info, err := readMemInfo()
	if err != nil {
		return -1
	}

	availPercent := int(info.AvailableKB * 100 / info.TotalKB)

	if config.Verbose {
		logMsg("DEBUG",
			"内存状态: 总计=%dMB 可用=%dMB(%d%%) 空闲=%dMB 缓存=%dMB Slab=%dMB",
			info.TotalKB/1024, info.AvailableKB/1024, availPercent,
			info.FreeKB/1024, info.CachedKB/1024, info.SlabKB/1024)
	}

	if availPercent <= config.MemThreshold {
		logMsg("WARN",
			"可用内存过低: %d%% (阈值=%d%%)，开始内存回收",
			availPercent, config.MemThreshold)

		recovered := 0

		// 步骤1: 释放页缓存
		if config.EnableDropCaches {
			if doDropCaches() == nil {
				recovered++
			}
			time.Sleep(500 * time.Millisecond)
		}

		// 步骤2: 压缩slab
		if config.EnableSlabCompact {
			if doSlabCompact() == nil {
				recovered++
			}
			time.Sleep(500 * time.Millisecond)
		}

		// 步骤3: 重启泄漏进程（如果配置了）
		if config.RestartProc != "" {
			// 先检查回收后内存是否仍然不足
			after, err := readMemInfo()
			if err == nil {
				afterPercent := int(after.AvailableKB * 100 / after.TotalKB)
				if afterPercent <= config.MemThreshold {
					logMsg("INFO", "缓存回收后可用内存仍为%d%%，尝试重启进程 %s",
						afterPercent, config.RestartProc)
					if restartProcess(config.RestartProc) == nil {
						recovered++
					}
				} else {
					logMsg("INFO", "缓存回收后可用内存恢复到%d%%，无需重启进程", afterPercent)
				}
			}
		}

		// 记录回收后状态
		final, err := readMemInfo()
		if err == nil {
			finalPercent := int(final.AvailableKB * 100 / final.TotalKB)
			freedKB := final.AvailableKB - info.AvailableKB
			logMsg("INFO", "内存回收完成: 可用内存 %d%% -> %d%%，释放约 %dMB",
				availPercent, finalPercent, freedKB/1024)
		}

		return recovered
	}

	return 0
}

// daemonize 守护进程化（使用环境变量标记避免循环）
func daemonize() error {
	// 检查是否是子进程（由父进程重新启动）
	if os.Getenv("MEMFIX_DAEMON_CHILD") == "1" {
		// 子进程：创建新会话，然后第二次fork
		syscall.Setsid()

		// 第二次fork（孙进程）
		if os.Getenv("MEMFIX_DAEMON_GRANDCHILD") != "1" {
			cmd := exec.Command(os.Args[0], os.Args[1:]...)
			cmd.Env = append(os.Environ(), "MEMFIX_DAEMON_GRANDCHILD=1")
			cmd.Stdin = nil
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Start(); err != nil {
				return err
			}
			// 子进程退出，让孙进程继续
			os.Exit(0)
		}

		// 孙进程：继续执行
		os.Chdir("/")
		syscall.Umask(0)

		// 关闭标准文件描述符
		os.Stdin.Close()
		os.Stdout.Close()
		os.Stderr.Close()

		// 打开日志
		logPath := getLogFilePath()
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		logFile = f

		// 写入PID文件
		pidFile = getPidFilePath()
		pidContent := fmt.Sprintf("%d\n", os.Getpid())
		os.WriteFile(pidFile, []byte(pidContent), 0644)

		return nil
	}

	// 父进程：启动子进程并退出
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), "MEMFIX_DAEMON_CHILD=1")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Printf("memfix 已启动，PID=%d\n", cmd.Process.Pid)
	os.Exit(0)
	return nil
}

// runForeground 前台运行模式
func runForeground() {
	logFile = os.Stdout
	fmt.Println("memfix 前台调试模式启动")
	fmt.Printf("配置: 间隔=%ds 阈值=%d%%\n", config.CheckInterval, config.MemThreshold)

	for running {
		checkAndRecover()
		// 分段sleep
		for i := 0; i < config.CheckInterval && running; i++ {
			time.Sleep(1 * time.Second)
		}
	}
}

// runOnce 单次执行模式
func runOnce() int {
	logFile = os.Stdout
	fmt.Println("memfix 单次检查模式")
	result := checkAndRecover()
	if result == 0 {
		fmt.Println("内存正常，无需回收")
	} else if result > 0 {
		fmt.Printf("已执行 %d 项内存回收操作\n", result)
	}
	return result
}

// showStatus 显示状态
func showStatus() int {
	// 查找已存在的PID文件（无副作用）
	pidPath := findExistingPidFile()
	if pidPath == "" {
		fmt.Println("memfix 未运行（无PID文件）")
		return 1
	}

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("memfix 未运行（无法读取PID文件）")
		return 1
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil || pid <= 0 {
		fmt.Println("memfix PID文件格式错误")
		return 1
	}

	// 检查进程是否存在
	process, err := os.FindProcess(pid)
	if err != nil || process.Signal(syscall.Signal(0)) != nil {
		fmt.Printf("memfix 未运行（PID文件存在但进程不存在，PID=%d）\n", pid)
		return 1
	}

	fmt.Printf("memfix 正在运行，PID=%d\n", pid)

	// 显示当前内存状态
	info, err := readMemInfo()
	if err == nil {
		availPercent := int(info.AvailableKB * 100 / info.TotalKB)
		fmt.Printf("当前内存: 总计=%dMB 可用=%dMB(%d%%) 空闲=%dMB 缓存=%dMB Slab=%dMB\n",
			info.TotalKB/1024, info.AvailableKB/1024, availPercent,
			info.FreeKB/1024, info.CachedKB/1024, info.SlabKB/1024)
	}

	// 显示最近日志
	fmt.Println("\n最近日志:")
	logPath := getLogFilePath()
	logData, err := os.ReadFile(logPath)
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
		start := 0
		if len(lines) > 20 {
			start = len(lines) - 20
		}
		for _, line := range lines[start:] {
			fmt.Println(line)
		}
	} else {
		fmt.Println("(无法读取日志文件)")
	}

	return 0
}

// stopDaemon 停止守护进程
func stopDaemon() int {
	// 查找已存在的PID文件（无副作用）
	pidPath := findExistingPidFile()
	if pidPath == "" {
		fmt.Println("memfix 未运行")
		return 1
	}

	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("memfix 未运行（无法读取PID文件）")
		return 1
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil || pid <= 0 {
		fmt.Println("无效的PID")
		os.Remove(pidPath)
		return 1
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		fmt.Println("进程不存在，清理PID文件")
		os.Remove(pidPath)
		return 0
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		fmt.Printf("停止失败: %v\n", err)
		return 1
	}

	fmt.Println("已发送停止信号，等待进程退出...")
	// 等待最多5秒（给信号处理足够时间）
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if process.Signal(syscall.Signal(0)) != nil {
			fmt.Println("memfix 已停止")
			os.Remove(pidPath)
			return 0
		}
	}

	fmt.Println("进程未响应，强制杀死")
	process.Kill()
	os.Remove(pidPath)
	return 0
}

// usage 显示帮助
func usage() {
	fmt.Printf("%s v%s - %s\n\n", ProjectName, Version, Description)
	fmt.Printf("用法: %s [选项]\n\n", ProjectName)
	fmt.Println("选项:")
	fmt.Println("  start        以守护进程方式启动（默认）")
	fmt.Println("  stop         停止正在运行的守护进程")
	fmt.Println("  restart      重启守护进程")
	fmt.Println("  status       显示运行状态和当前内存信息")
	fmt.Println("  once         执行单次内存检查和回收（前台）")
	fmt.Println("  foreground   前台运行（调试用，输出到stdout）")
	fmt.Println("  -h, --help   显示此帮助信息")
	fmt.Println()
	fmt.Printf("配置文件: %s\n", DefaultConfigFile)
	fmt.Printf("日志文件: %s (或 %s)\n", DefaultLogFile, FallbackLogFile)
	fmt.Printf("PID 文件: %s (或 %s)\n", DefaultPidFile, FallbackPidFile)
}

// signalHandler 信号处理
func signalHandler() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	for sig := range sigChan {
		switch sig {
		case syscall.SIGTERM, syscall.SIGINT:
			logMsg("INFO", "收到退出信号(%v)，正在停止...", sig)
			running = false
		case syscall.SIGHUP:
			logMsg("INFO", "收到SIGHUP，重新加载配置")
			loadConfig(DefaultConfigFile)
		}
	}
}

func main() {
	action := "start"

	if len(os.Args) > 1 {
		if os.Args[1] == "-h" || os.Args[1] == "--help" {
			usage()
			return
		}
		action = os.Args[1]
	}

	// 初始化默认配置
	initDefaultConfig()

	// 处理不需要配置的命令
	if action == "stop" {
		os.Exit(stopDaemon())
	}
	if action == "status" {
		os.Exit(showStatus())
	}

	// 加载配置
	loadConfig(DefaultConfigFile)

	// 注册信号处理
	go signalHandler()

	switch action {
	case "start":
		// 检查是否已在运行（使用无副作用的查找）
		pidPath := findExistingPidFile()
		if pidPath != "" {
			if pidData, err := os.ReadFile(pidPath); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(pidData))); err == nil && pid > 0 {
					if process, err := os.FindProcess(pid); err == nil && process.Signal(syscall.Signal(0)) == nil {
						fmt.Printf("memfix 已在运行，PID=%d\n", pid)
						return
					}
				}
			}
			// PID文件存在但进程不在，清理旧PID文件
			os.Remove(pidPath)
		}

		if err := daemonize(); err != nil {
			fmt.Fprintf(os.Stderr, "守护进程化失败: %v\n", err)
			os.Exit(1)
		}

		logMsg("INFO", "===== memfix 守护进程启动 =====")
		logMsg("INFO", "版本: %s | 目标: 小米AX3000T(MT7981B) | Go版本: %s", Version, runtime.Version())

		for running {
			checkAndRecover()
			for i := 0; i < config.CheckInterval && running; i++ {
				time.Sleep(1 * time.Second)
			}
		}

		logMsg("INFO", "===== memfix 守护进程停止 =====")
		if logFile != nil {
			logFile.Close()
		}
		os.Remove(pidFile)

	case "foreground":
		runForeground()

	case "once":
		os.Exit(runOnce())

	case "restart":
		fmt.Println("正在重启 memfix...")
		stopDaemon()
		time.Sleep(2 * time.Second)
		// 重新执行start，等待daemonize父进程退出
		cmd := exec.Command(os.Args[0], "start")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n", action)
		usage()
		os.Exit(1)
	}
}
