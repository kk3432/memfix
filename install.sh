#!/bin/sh
# ============================================
# memfix 一键安装脚本
# 适用于小米AX3000T(联发科版)及其他OpenWrt设备
# ============================================

set -e

PROG_NAME="memfix"
VERSION="1.0.0"
INSTALL_DIR="/usr/sbin"
CONFIG_DIR="/etc"
INIT_DIR="/etc/init.d"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

# 检查是否为root
check_root() {
    if [ "$(id -u)" != "0" ]; then
        error "此脚本需要root权限运行"
    fi
}

# 检查架构
check_arch() {
    ARCH=$(uname -m)
    info "检测到系统架构: $ARCH"
    case "$ARCH" in
        aarch64|arm64)
            info "架构兼容 (ARM64/aarch64)"
            ;;
        *)
            warn "当前架构 $ARCH 可能不是目标架构 (aarch64)"
            warn "程序可能无法正常运行"
            ;;
    esac
}

# 检查文件是否存在
check_files() {
    if [ ! -f "./memfix" ]; then
        error "未找到 memfix 可执行文件，请确保在安装包目录下运行"
    fi
    if [ ! -f "./memfix.conf" ]; then
        warn "未找到 memfix.conf，将使用默认配置"
    fi
    if [ ! -f "./S99memfix" ]; then
        warn "未找到 S99memfix 启动脚本"
    fi
}

# 安装文件
do_install() {
    info "开始安装 memfix v${VERSION}..."

    # 创建目录
    mkdir -p "$INSTALL_DIR" "$CONFIG_DIR" "$INIT_DIR"

    # 安装主程序
    cp "./memfix" "$INSTALL_DIR/$PROG_NAME"
    chmod 755 "$INSTALL_DIR/$PROG_NAME"
    info "已安装主程序: $INSTALL_DIR/$PROG_NAME"

    # 安装配置文件（不覆盖已有配置）
    if [ -f "./memfix.conf" ]; then
        if [ -f "$CONFIG_DIR/memfix.conf" ]; then
            warn "配置文件已存在，保留原配置 ($CONFIG_DIR/memfix.conf)"
            cp "./memfix.conf" "$CONFIG_DIR/memfix.conf.new"
            info "新配置文件已保存为 $CONFIG_DIR/memfix.conf.new"
        else
            cp "./memfix.conf" "$CONFIG_DIR/memfix.conf"
            chmod 644 "$CONFIG_DIR/memfix.conf"
            info "已安装配置文件: $CONFIG_DIR/memfix.conf"
        fi
    fi

    # 安装启动脚本
    if [ -f "./S99memfix" ]; then
        cp "./S99memfix" "$INIT_DIR/$PROG_NAME"
        chmod 755 "$INIT_DIR/$PROG_NAME"
        info "已安装启动脚本: $INIT_DIR/$PROG_NAME"
    fi

    info "文件安装完成"
}

# 启用并启动服务
enable_service() {
    if [ -f "$INIT_DIR/$PROG_NAME" ]; then
        # 尝试启用开机自启（OpenWrt）
        if command -v "$INIT_DIR/$PROG_NAME" >/dev/null 2>&1; then
            "$INIT_DIR/$PROG_NAME" enable 2>/dev/null || warn "无法启用开机自启，请手动执行: $INIT_DIR/$PROG_NAME enable"
            info "已启用开机自启"
        fi

        # 启动服务
        info "启动 memfix 服务..."
        "$INSTALL_DIR/$PROG_NAME" start
        sleep 1

        # 验证运行状态
        if "$INSTALL_DIR/$PROG_NAME" status >/dev/null 2>&1; then
            info "memfix 服务已成功启动"
        else
            warn "memfix 服务启动可能失败，请检查: $INSTALL_DIR/$PROG_NAME status"
        fi
    else
        warn "未找到启动脚本，您可以手动运行: $INSTALL_DIR/$PROG_NAME start"
    fi
}

# 显示安装完成信息
show_summary() {
    echo ""
    echo "============================================"
    echo "  memfix v${VERSION} 安装完成！"
    echo "============================================"
    echo ""
    echo "安装路径:"
    echo "  可执行文件: $INSTALL_DIR/$PROG_NAME"
    echo "  配置文件:   $CONFIG_DIR/memfix.conf"
    echo "  启动脚本:   $INIT_DIR/$PROG_NAME"
    echo "  日志文件:   /var/log/memfix.log (或 /tmp/memfix.log)"
    echo "  PID文件:    /var/run/memfix.pid (或 /tmp/memfix.pid)"
    echo ""
    echo "常用命令:"
    echo "  memfix start      # 启动服务"
    echo "  memfix stop       # 停止服务"
    echo "  memfix restart    # 重启服务"
    echo "  memfix status     # 查看状态"
    echo "  memfix once       # 单次检查回收"
    echo "  memfix foreground # 前台运行(调试)"
    echo ""
    echo "配置修改:"
    echo "  vi $CONFIG_DIR/memfix.conf"
    echo "  修改后执行: memfix restart"
    echo ""
    echo "卸载方法:"
    echo "  memfix stop"
    echo "  rm $INSTALL_DIR/$PROG_NAME $CONFIG_DIR/memfix.conf $INIT_DIR/$PROG_NAME"
    echo "============================================"
}

# 主流程
main() {
    echo "memfix v${VERSION} 安装脚本"
    echo "================================"
    echo ""

    check_root
    check_arch
    check_files
    do_install
    enable_service
    show_summary
}

main "$@"
