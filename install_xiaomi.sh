#!/bin/sh
# ============================================
# memfix 一键安装脚本（小米AX3000T专用）
# 通过 gh-proxy.com 加速下载 GitHub 发布包
#
# 用法:
#   wget -qO- https://gh-proxy.com/https://raw.githubusercontent.com/kk3432/memfix/main/install_xiaomi.sh | sh
#
# 指定版本:
#   MEMFIX_VERSION=v1.1.0 wget -qO- https://gh-proxy.com/https://raw.githubusercontent.com/kk3432/memfix/main/install_xiaomi.sh | sh
# ============================================

set -e

PROG_NAME="memfix"
VERSION="${MEMFIX_VERSION:-v1.1.0}"
GITHUB_REPO="kk3432/memfix"
GH_PROXY="https://gh-proxy.com"
INSTALL_DIR="/userdisk/.memfix"
TMP_DIR="/tmp/memfix_install_$$"
CONF_DST="/etc/memfix.conf"
CRON_FILE="/etc/crontabs/root"

# ---------- 输出函数 ----------
info()  { echo "[INFO]  $1"; }
warn()  { echo "[WARN]  $1"; }
error() { echo "[ERROR] $1"; exit 1; }

# ---------- 前置检查 ----------
check_root() {
    if [ "$(id -u)" != "0" ]; then
        error "此脚本需要 root 权限运行"
    fi
}

check_arch() {
    ARCH=$(uname -m)
    info "系统架构: $ARCH"
    case "$ARCH" in
        aarch64|arm64)
            info "架构兼容 (ARM64/aarch64)"
            ;;
        *)
            error "不支持的架构: $ARCH（本程序仅支持 aarch64/arm64）"
            ;;
    esac
}

check_wget() {
    if ! command -v wget >/dev/null 2>&1; then
        error "未找到 wget 命令，请先安装 wget"
    fi
}

# ---------- 下载发布包 ----------
download_package() {
    info "下载 memfix ${VERSION} 发布包..."

    # 构建下载URL（通过gh-proxy加速）
    PKG_NAME="${PROG_NAME}-${VERSION}-aarch64.tar.gz"
    DOWNLOAD_URL="${GH_PROXY}/https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/${PKG_NAME}"

    info "下载地址: $DOWNLOAD_URL"

    mkdir -p "$TMP_DIR"
    cd "$TMP_DIR"

    # 下载（带重试）
    if ! wget --tries=3 --timeout=30 -qO "$PKG_NAME" "$DOWNLOAD_URL"; then
        # 尝试不带版本号的latest格式
        PKG_NAME_LATEST="${PROG_NAME}-aarch64.tar.gz"
        LATEST_URL="${GH_PROXY}/https://github.com/${GITHUB_REPO}/releases/latest/download/${PKG_NAME_LATEST}"
        info "首次下载失败，尝试 latest 地址: $LATEST_URL"
        if ! wget --tries=3 --timeout=30 -qO "$PKG_NAME" "$LATEST_URL"; then
            rm -rf "$TMP_DIR"
            error "下载失败！请检查网络或版本号是否正确。"
            error "可手动指定版本: MEMFIX_VERSION=v1.1.0 wget -qO- <脚本URL> | sh"
        fi
    fi

    # 验证文件
    if [ ! -s "$PKG_NAME" ]; then
        rm -rf "$TMP_DIR"
        error "下载的文件为空"
    fi

    PKG_SIZE=$(ls -l "$PKG_NAME" | awk '{print $5}')
    info "下载完成，大小: ${PKG_SIZE} 字节"
}

# ---------- 解压 ----------
extract_package() {
    info "解压发布包..."
    cd "$TMP_DIR"
    tar xzf "$PKG_NAME"

    # 检查关键文件
    if [ ! -f "./memfix" ]; then
        # 可能在子目录中
        if [ -d "./${PROG_NAME}-${VERSION}" ]; then
            cd "./${PROG_NAME}-${VERSION}"
        elif [ -d "./memfix" ]; then
            cd "./memfix"
        fi
    fi

    if [ ! -f "./memfix" ]; then
        error "解压后未找到 memfix 二进制文件"
    fi

    info "解压完成"
}

# ---------- 安装 ----------
install_files() {
    info "安装到 $INSTALL_DIR ..."
    mkdir -p "$INSTALL_DIR"

    # 备份旧版本
    if [ -f "$INSTALL_DIR/memfix" ]; then
        cp "$INSTALL_DIR/memfix" "$INSTALL_DIR/memfix.old.bak" 2>/dev/null || true
        info "已备份旧版本为 memfix.old.bak"
    fi
    if [ -f "$INSTALL_DIR/memfix.conf" ]; then
        cp "$INSTALL_DIR/memfix.conf" "$INSTALL_DIR/memfix.conf.bak" 2>/dev/null || true
    fi

    # 安装二进制
    cp "./memfix" "$INSTALL_DIR/memfix"
    chmod 755 "$INSTALL_DIR/memfix"

    # 安装配置文件（保留用户已有配置）
    if [ -f "$INSTALL_DIR/memfix.conf" ]; then
        info "保留已有配置文件"
        # 检查是否有log_file配置项，没有则追加
        if ! grep -q "log_file" "$INSTALL_DIR/memfix.conf" 2>/dev/null; then
            echo "" >> "$INSTALL_DIR/memfix.conf"
            echo "# 日志文件路径（v1.1.0新增）" >> "$INSTALL_DIR/memfix.conf"
            echo "log_file = $INSTALL_DIR/memfix.log" >> "$INSTALL_DIR/memfix.conf"
            info "已追加 log_file 配置项"
        fi
    else
        if [ -f "./memfix.xiaomi.conf" ]; then
            cp "./memfix.xiaomi.conf" "$INSTALL_DIR/memfix.conf"
            info "已安装小米专用配置文件"
        elif [ -f "./memfix.conf" ]; then
            cp "./memfix.conf" "$INSTALL_DIR/memfix.conf"
            info "已安装配置文件"
        fi
        chmod 644 "$INSTALL_DIR/memfix.conf"
    fi

    # 安装memfixctl管理脚本
    if [ -f "./memfixctl" ]; then
        cp "./memfixctl" "$INSTALL_DIR/memfixctl"
        chmod 755 "$INSTALL_DIR/memfixctl"
        info "已安装 memfixctl 管理脚本"
    fi

    # 复制配置到/etc（程序默认读取/etc/memfix.conf）
    cp "$INSTALL_DIR/memfix.conf" "$CONF_DST" 2>/dev/null || true

    info "文件安装完成"
}

# ---------- 配置开机自启（crontab方式） ----------
install_autostart() {
    info "配置开机自启（crontab每分钟检查拉起）..."

    # 确保crontab目录存在
    mkdir -p /etc/crontabs

    # 检查是否已配置
    if grep -q "memfixctl" "$CRON_FILE" 2>/dev/null; then
        info "crontab已配置memfix自启，跳过"
    else
        echo "* * * * * $INSTALL_DIR/memfixctl start >/dev/null 2>&1" >> "$CRON_FILE"
        info "已添加到 crontab"
    fi

    # 重载crontab
    if command -v crontab >/dev/null 2>&1; then
        crontab "$CRON_FILE" 2>/dev/null || true
    fi

    # 确保cron服务运行
    if [ -x "/etc/init.d/cron" ]; then
        /etc/init.d/cron start 2>/dev/null || true
    fi

    info "自启配置完成"
}

# ---------- 启动服务 ----------
start_service() {
    info "启动 memfix 服务..."

    # 先停止旧进程
    if [ -x "$INSTALL_DIR/memfixctl" ]; then
        "$INSTALL_DIR/memfixctl" stop 2>/dev/null || true
    else
        kill "$(ps w | grep "$INSTALL_DIR/memfix " | grep -v grep | awk '{print $1}')" 2>/dev/null || true
    fi
    sleep 1

    # 清理旧PID
    rm -f /var/run/memfix.pid /tmp/memfix.pid 2>/dev/null || true

    # 启动
    if [ -x "$INSTALL_DIR/memfixctl" ]; then
        "$INSTALL_DIR/memfixctl" start
    else
        "$INSTALL_DIR/memfix" start
    fi
    sleep 2

    # 验证进程
    if ps w | grep -q "$INSTALL_DIR/memfix " | grep -v grep >/dev/null 2>&1; then
        PID=$(ps w | grep "$INSTALL_DIR/memfix " | grep -v grep | head -1 | awk '{print $1}')
        info "memfix 启动成功，PID=$PID"
    else
        warn "进程检查异常，请手动执行: $INSTALL_DIR/memfixctl status"
    fi
}

# ---------- 验证安装 ----------
verify_install() {
    echo ""
    info "========== 安装验证 =========="

    # 检查文件
    ls -la "$INSTALL_DIR/memfix" "$INSTALL_DIR/memfix.conf" 2>/dev/null

    # 检查版本
    echo ""
    info "程序版本:"
    "$INSTALL_DIR/memfix" --version 2>/dev/null || "$INSTALL_DIR/memfix" -v 2>/dev/null || echo "(版本信息见日志)"

    # 检查状态
    echo ""
    info "运行状态:"
    if [ -x "$INSTALL_DIR/memfixctl" ]; then
        "$INSTALL_DIR/memfixctl" status 2>&1 | head -20
    else
        "$INSTALL_DIR/memfix" status 2>&1 | head -20
    fi

    # 检查日志
    LOG_FILE=$(grep "log_file" "$INSTALL_DIR/memfix.conf" 2>/dev/null | head -1 | cut -d= -f2 | tr -d ' ')
    if [ -z "$LOG_FILE" ]; then
        LOG_FILE="/tmp/memfix.log"
    fi
    echo ""
    info "日志文件: $LOG_FILE"
    if [ -f "$LOG_FILE" ]; then
        tail -5 "$LOG_FILE"
    else
        warn "日志文件尚未生成"
    fi
}

# ---------- 清理 ----------
cleanup() {
    rm -rf "$TMP_DIR"
}

# ---------- 显示安装摘要 ----------
show_summary() {
    echo ""
    echo "============================================"
    echo "  memfix ${VERSION} 安装完成！"
    echo "============================================"
    echo ""
    echo "安装路径:   $INSTALL_DIR"
    echo "主程序:     $INSTALL_DIR/memfix"
    echo "配置文件:   $INSTALL_DIR/memfix.conf"
    echo "管理脚本:   $INSTALL_DIR/memfixctl"
    echo "日志文件:   $LOG_FILE"
    echo "开机自启:   crontab (每分钟检查拉起)"
    echo ""
    echo "常用命令:"
    echo "  $INSTALL_DIR/memfixctl start    # 启动"
    echo "  $INSTALL_DIR/memfixctl stop     # 停止"
    echo "  $INSTALL_DIR/memfixctl restart  # 重启"
    echo "  $INSTALL_DIR/memfixctl status   # 查看状态"
    echo "  $INSTALL_DIR/memfixctl once     # 单次强制回收"
    echo ""
    echo "配置修改后执行: $INSTALL_DIR/memfixctl restart"
    echo "============================================"
}

# ---------- 主流程 ----------
main() {
    echo "============================================"
    echo "  memfix ${VERSION} 一键安装脚本"
    echo "  小米AX3000T (联发科MT7981B) 专用"
    echo "  下载加速: gh-proxy.com"
    echo "============================================"
    echo ""

    check_root
    check_arch
    check_wget
    download_package
    extract_package
    install_files
    install_autostart
    start_service
    verify_install
    cleanup
    show_summary

    info "安装全部完成！"
}

main "$@"
