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
	Version     = "1.1.0"
	ProjectName = "memfix"
	Description = "小米AX3000T(联发科版) 内存泄漏监控修复工具"
)

// 默认配置常量
const (
	DefaultCheckInterval = 30   // 默认检查间隔(秒)
	DefaultMemThreshold  = 15   // 默认可用内存阈值(百分比)
	DefaultLogFile       = "/tmp/memfix.log"
	DefaultPidFile       = "/var/run/memfix.pid"
	DefaultConfigFile    = "/etc/memfix.conf"
	FallbackLogFile      = "/tmp/memfix.log"
	FallbackPidFile      = "/tmp/memfix.pid"

	// 场景调控: 触发回收后的密集监控参数
	BoostDuration = 300 // 密集监控持续时间(秒) = 5分钟
	BoostInterval = 2   // 密集监控间隔(秒)

	// 日志轮换
	LogRotateDays = 3 // 日志每N天轮换一次
)

// Config 配置结构体
type Config struct {
	CheckInterval     int    // 检查间隔(秒)
	MemThreshold      int    // 可用内存阈值(%)
	RestartProc       string // 需要重启的进程名
	EnableDropCaches  bool   // 是否启用drop_caches
	EnableSlabCompact bool   // 是否启用slab压缩
	Verbose           bool   // 详细日志
	LogFile           string // 日志文件路径（由配置文件指定）
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
	config       Config
	logFile      *os.File
	pidFile      string
	running      = true
	boostMode    bool      // 是否处于密集监控模式
	boostEndTime time.Time // 密集监控结束时间
	cstLocation  = time.FixedZone("CST", 8*3600) // 固定UTC+8时区，修复路由器时区错误导致的时间戳偏移
)

// logMsg 记录日志（使用固定CST时区，避免路由器系统时区为UTC导致时间戳偏移8小时）
func logMsg(level, format string, args ...interface{}) {
	if logFile == nil {
		return
	}
	now := time.Now().In(cstLocation).Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("[%s] [%s] %s\n", now, level, msg)
	logFile.WriteString(line)
	logFile.Sync()
}

// getPidFilePath 获取可用的PID文件路径（无副作用，不删除已存在的文件）
func getPidFilePath() string {
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

// getLogFilePath 获取可用的日志文件路径（优先使用配置文件指定的路径）
func getLogFilePath() string {
	// 优先使用配置文件指定的路径
	candidates := []string{config.LogFile, DefaultLogFile, FallbackLogFile}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			f.Close()
			return path
		}
	}
	return FallbackLogFile
}

// rotateLogIfNeeded 检查日志文件是否需要轮换（在打开文件之前调用，避免O_APPEND更新mtime）
func rotateLogIfNeeded(logPath string) {
	info, err := os.Stat(logPath)
	if err != nil {
		return
	}
	// 如果日志文件修改时间超过N天，执行轮换
	if time.Since(info.ModTime()) >= time.Duration(LogRotateDays)*24*time.Hour {
		// 移除旧的.1备份（如果存在）
		os.Remove(logPath + ".1")
		// 重命名当前日志为.1
		os.Rename(logPath, logPath+".1")
	}
}

// checkLogRotate 运行时检查并执行日志轮换（每LogRotateDays天轮换一次）
func checkLogRotate() {
	if logFile == nil {
		return
	}
	logPath := getLogFilePath()
	info, err := os.Stat(logPath)
	if err != nil {
		return
	}
	// 如果日志文件修改时间超过N天，执行轮换
	if time.Since(info.ModTime()) >= time.Duration(LogRotateDays)*24*time.Hour {
		logMsg("INFO", "日志文件超过%d天，执行轮换", LogRotateDays)
		logFile.Close()
		rotateLogIfNeeded(logPath)
		// 创建新日志文件
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			logFile = f
			logMsg("INFO", "日志轮换完成，旧日志保存为 %s.1", logPath)
		} else {
			// 尝试fallback路径
			f2, err2 := os.OpenFile(FallbackLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err2 == nil {
				logFile = f2
				logMsg("WARN", "原日志路径不可写，切换到 %s", FallbackLogFile)
			}
		}
	}
}

// analyzeLogStats 分析近三天日志统计：回收次数、平均可用内存百分比
func analyzeLogStats() (recoverCount int, avgAvailPercent float64, sampleCount int) {
	logPath := getLogFilePath()
	threeDaysAgo := time.Now().In(cstLocation).Add(-time.Duration(LogRotateDays) * 24 * time.Hour)

	// 同时读取当前日志和轮换后的.1日志
	files := []string{logPath, logPath + ".1"}
	var totalAvail int

	for _, fpath := range files {
		data, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if len(line) < 21 || line[0] != '[' {
				continue
			}
			// 解析时间戳 [2006-01-02 15:04:05]
			ts, err := time.ParseInLocation("2006-01-02 15:04:05", line[1:20], cstLocation)
			if err != nil {
				continue
			}
			if ts.Before(threeDaysAgo) {
				continue
			}
			// 统计回收次数（匹配"内存回收完成"）
			if strings.Contains(line, "内存回收完成") {
				recoverCount++
			}
			// 统计平均可用内存百分比（从DEBUG行"内存状态"中提取）
			if strings.Contains(line, "内存状态:") {
				// 格式: 可用=XXMB(YY%%)
				idx := strings.Index(line, "(")
				if idx > 0 {
					rest := line[idx+1:]
					end := strings.Index(rest, "%%)")
					if end > 0 {
						if pct, err := strconv.Atoi(rest[:end]); err == nil {
							totalAvail += pct
							sampleCount++
						}
					}
				}
			}
		}
	}

	if sampleCount > 0 {
		avgAvailPercent = float64(totalAvail) / float64(sampleCount)
	}
	return
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
		LogFile:           DefaultLogFile,
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
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
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
		case "log_file":
			if value != "" {
				config.LogFile = value
			}
		}
	}

	logMsg("INFO", "配置已加载: 间隔=%ds 阈值=%d%% 重启进程=%s drop_caches=%v slab_compact=%v verbose=%v 日志=%s",
		config.CheckInterval, config.MemThreshold,
		func() string {
			if config.RestartProc == "" {
				return "(无)"
			}
			return config.RestartProc
		}(),
		config.EnableDropCaches, config.EnableSlabCompact, config.Verbose, config.LogFile)
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
	syscall.Sync()
	time.Sleep(100 * time.Millisecond)
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

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := process.Signal(syscall.SIGTERM); err != nil {
		logMsg("ERROR", "发送SIGTERM失败: %v", err)
		return err
	}

	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if process.Signal(syscall.Signal(0)) != nil {
			logMsg("INFO", "进程 %s 已终止", procName)
			break
		}
	}

	if process.Signal(syscall.Signal(0)) == nil {
		logMsg("WARN", "进程 %s 未响应SIGTERM，发送SIGKILL", procName)
		process.Kill()
	}

	time.Sleep(2 * time.Second)

	newPid := findProcessPid(procName)
	if newPid > 0 {
		logMsg("INFO", "进程 %s 已重启，新PID=%d", procName, newPid)
		return nil
	}
	logMsg("WARN", "进程 %s 未自动重启，请检查init配置", procName)
	return fmt.Errorf("进程未自动重启")
}

// doRecoveryActions 执行实际的内存回收操作（drop_caches + slab_compact + 可选重启进程）
func doRecoveryActions() int {
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

	// 步骤3: 重启泄漏进程（如果配置了，且回收后内存仍不足）
	if config.RestartProc != "" {
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

	return recovered
}

// checkAndRecover 内存检查与修复主逻辑
// force=true 时不论内存是否充足都强制回收（用于once命令）
// force=false 时仅在可用内存低于阈值时回收（用于守护进程）
func checkAndRecover(force bool) int {
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

	// 非强制模式下，内存充足则不回收
	if !force && availPercent > config.MemThreshold {
		return 0
	}

	if force {
		logMsg("INFO", "强制内存回收: 当前可用=%d%% (阈值=%d%%)", availPercent, config.MemThreshold)
	} else {
		logMsg("WARN",
			"可用内存过低: %d%% (阈值=%d%%)，开始内存回收",
			availPercent, config.MemThreshold)
	}

	recovered := doRecoveryActions()

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

// daemonize 守护进程化
func daemonize() error {
	if os.Getenv("MEMFIX_DAEMON_CHILD") == "1" {
		syscall.Setsid()

		if os.Getenv("MEMFIX_DAEMON_GRANDCHILD") != "1" {
			cmd := exec.Command(os.Args[0], os.Args[1:]...)
			cmd.Env = append(os.Environ(), "MEMFIX_DAEMON_GRANDCHILD=1")
			cmd.Stdin = nil
			cmd.Stdout = nil
			cmd.Stderr = nil
			if err := cmd.Start(); err != nil {
				return err
			}
			os.Exit(0)
		}

		os.Chdir("/")
		syscall.Umask(0)

		os.Stdin.Close()
		os.Stdout.Close()
		os.Stderr.Close()

		logPath := getLogFilePath()
		rotateLogIfNeeded(logPath) // 打开前先检查轮换，避免O_APPEND更新mtime
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		logFile = f

		pidFile = getPidFilePath()
		pidContent := fmt.Sprintf("%d\n", os.Getpid())
		os.WriteFile(pidFile, []byte(pidContent), 0644)

		return nil
	}

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

// sleepWithInterrupt 可被信号中断的分段sleep
func sleepWithInterrupt(seconds int) {
	for i := 0; i < seconds && running; i++ {
		time.Sleep(1 * time.Second)
	}
}

// runMainLoop 主循环（支持密集监控模式和日志轮换）
func runMainLoop() {
	for running {
		// 检查日志轮换
		checkLogRotate()

		recovered := checkAndRecover(false)

		// 场景调控: 触发回收后进入密集监控模式
		if recovered > 0 {
			boostMode = true
			boostEndTime = time.Now().Add(time.Duration(BoostDuration) * time.Second)
			logMsg("INFO", "进入密集监控模式: 未来%d分钟内每%d秒检测一次",
				BoostDuration/60, BoostInterval)
		}

		// 决定本次sleep间隔
		interval := config.CheckInterval
		if boostMode {
			if time.Now().After(boostEndTime) {
				boostMode = false
				logMsg("INFO", "密集监控模式结束，恢复正常间隔%d秒", config.CheckInterval)
			} else {
				interval = BoostInterval
			}
		}

		sleepWithInterrupt(interval)
	}
}

// runForeground 前台运行模式
func runForeground() {
	logFile = os.Stdout
	fmt.Println("memfix 前台调试模式启动")
	fmt.Printf("配置: 间隔=%ds 阈值=%d%%\n", config.CheckInterval, config.MemThreshold)
	runMainLoop()
}

// runOnce 单次执行模式（强制回收，不论内存是否充足）
func runOnce() int {
	logFile = os.Stdout
	fmt.Println("memfix 单次强制回收模式")
	result := checkAndRecover(true)
	if result == 0 {
		fmt.Println("未执行回收操作（请检查配置）")
	} else if result > 0 {
		fmt.Printf("已执行 %d 项内存回收操作\n", result)
	}
	return result
}

// showStatus 显示状态
func showStatus() int {
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

	// 显示近三天统计
	recoverCount, avgAvail, sampleCount := analyzeLogStats()
	fmt.Println("\n近三天统计:")
	fmt.Printf("  回收次数: %d 次\n", recoverCount)
	if sampleCount > 0 {
		fmt.Printf("  平均可用内存: %.1f%% (基于 %d 次采样)\n", avgAvail, sampleCount)
	} else {
		fmt.Println("  平均可用内存: 无数据")
	}

	// 显示密集监控状态
	if boostMode {
		remaining := int(time.Until(boostEndTime).Seconds())
		if remaining > 0 {
			fmt.Printf("  密集监控模式: 开启 (剩余 %d 秒，每 %d 秒检测一次)\n",
				remaining, BoostInterval)
		}
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
	fmt.Println("  status       显示运行状态、近三天统计和当前内存信息")
	fmt.Println("  once         执行单次强制内存回收（不论内存是否充足）")
	fmt.Println("  foreground   前台运行（调试用，输出到stdout）")
	fmt.Println("  -h, --help   显示此帮助信息")
	fmt.Println()
	fmt.Printf("配置文件: %s (可指定 log_file 自定义日志路径)\n", DefaultConfigFile)
	fmt.Printf("默认日志: %s，每%d天自动轮换\n", DefaultLogFile, LogRotateDays)
	fmt.Printf("PID 文件: %s (或 %s)\n", DefaultPidFile, FallbackPidFile)
	fmt.Printf("场景调控: 触发回收后%d分钟内每%d秒密集检测\n", BoostDuration/60, BoostInterval)
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

	initDefaultConfig()

	// 所有命令都先加载配置（status需要读取配置中指定的日志路径）
	loadConfig(DefaultConfigFile)

	if action == "stop" {
		os.Exit(stopDaemon())
	}
	if action == "status" {
		os.Exit(showStatus())
	}

	go signalHandler()

	switch action {
	case "start":
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
			os.Remove(pidPath)
		}

		if err := daemonize(); err != nil {
			fmt.Fprintf(os.Stderr, "守护进程化失败: %v\n", err)
			os.Exit(1)
		}

		logMsg("INFO", "===== memfix 守护进程启动 =====")
		logMsg("INFO", "版本: %s | 目标: 小米AX3000T(MT7981B) | Go版本: %s", Version, runtime.Version())
		logMsg("INFO", "场景调控: 触发回收后%d分钟内每%d秒密集检测 | 日志每%d天轮换",
			BoostDuration/60, BoostInterval, LogRotateDays)

		runMainLoop()

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
