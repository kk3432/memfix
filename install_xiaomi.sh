#!/bin/sh
# ============================================
# memfix 小米官方固件专用安装脚本
# 适配小米AX3000T(联发科版)及其他小米路由器
#
# 小米固件特点：
#   - rootfs为squashfs只读，/etc为overlay可写
#   - /var为tmpfs内存文件系统，重启后丢失
#   - 开机自启支持 /etc/rc.local 和 /etc/init.d/
#   - 使用BusyBox，命令功能可能受限
# ============================================

set -e

PROG_NAME="memfix"
VERSION="1.0.0"

# 小米固件可写路径
INSTALL_BIN="/userdisk/.memfix"   # 用户数据分区，持久化
INSTALL_ETC="/etc"                 # overlay可写
INIT_SCRIPT="/etc/init.d/memfix"
RC_LOCAL="/etc/rc.local"

# 颜色输出（BusyBox sh可能不支持，做兼容）
info() {
    echo "[INFO] $1"
}

warn() {
    echo "[WARN] $1"
}

error() {
    echo "[ERROR] $1"
    exit 1
}

# 检查是否为root
check_root() {
    if [ "$(id -u)" != "0" ]; then
        error "此脚本需要root权限运行"
    fi
}

# 检测是否为小米固件
detect_xiaomi() {
    info "检测固件类型..."
    if [ -f "/etc/xiaoqiang_version" ] || [ -f "/etc/miwifi_version" ] || \
       [ -d "/etc/init.d/miwifi" ] || [ -f "/usr/sbin/mcpd" ]; then
        info "检测到小米官方固件"
        return 0
    else
        warn "未检测到小米固件特征，仍将继续安装"
        warn "如为标准OpenWrt，建议使用 install.sh"
        return 1
    fi
}

# 检查架构
check_arch() {
    ARCH=$(uname -m)
    info "系统架构: $ARCH"
    case "$ARCH" in
        aarch64|arm64)
            info "架构兼容 (ARM64/aarch64)"
            ;;
        mips|mipsel)
            warn "检测到MIPS架构，本程序为aarch64版本，可能无法运行"
            warn "请确认您的路由器型号是否为联发科版(MT7981B)"
            ;;
        *)
            warn "未知架构: $ARCH"
            ;;
    esac
}

# 检查文件
check_files() {
    if [ ! -f "./memfix" ]; then
        error "未找到 memfix 可执行文件"
    fi
    if [ ! -x "./memfix" ]; then
        chmod +x ./memfix
    fi
}

# 创建安装目录
create_dirs() {
    info "创建安装目录..."
    mkdir -p "$INSTALL_BIN"
    mkdir -p "/etc/init.d"
}

# 安装主程序
install_bin() {
    info "安装主程序到 $INSTALL_BIN ..."
    cp "./memfix" "$INSTALL_BIN/memfix"
    chmod 755 "$INSTALL_BIN/memfix"

    # 创建符号链接到PATH中（如果/usr/sbin可写）
    if [ -w "/usr/sbin" ]; then
        ln -sf "$INSTALL_BIN/memfix" "/usr/sbin/memfix"
        info "已创建符号链接: /usr/sbin/memfix -> $INSTALL_BIN/memfix"
    else
        # /usr/sbin只读，添加到profile的PATH
        if ! grep -q "$INSTALL_BIN" "/etc/profile" 2>/dev/null; then
            echo "export PATH=\$PATH:$INSTALL_BIN" >> "/etc/profile"
            info "已将 $INSTALL_BIN 添加到 /etc/profile 的PATH中"
            info "请重新登录SSH或执行 'source /etc/profile' 使PATH生效"
        fi
    fi
}

# 安装配置文件
install_config() {
    info "安装配置文件..."
    CONFIG_SRC="./memfix.conf"
    if [ -f "./memfix.xiaomi.conf" ]; then
        CONFIG_SRC="./memfix.xiaomi.conf"
        info "使用小米固件专用配置"
    fi

    if [ -f "$INSTALL_ETC/memfix.conf" ]; then
        warn "配置文件已存在，保留原配置"
        cp "$CONFIG_SRC" "$INSTALL_ETC/memfix.conf.new"
        info "新配置已保存为 $INSTALL_ETC/memfix.conf.new"
    else
        cp "$CONFIG_SRC" "$INSTALL_ETC/memfix.conf"
        chmod 644 "$INSTALL_ETC/memfix.conf"
        info "已安装配置文件: $INSTALL_ETC/memfix.conf"
    fi
}

# 安装init.d启动脚本
install_init() {
    info "安装启动脚本..."
    if [ -f "./S99memfix" ]; then
        # 修改脚本中的程序路径为实际安装路径
        sed "s|/usr/sbin/memfix|$INSTALL_BIN/memfix|g" "./S99memfix" > "$INIT_SCRIPT"
        chmod 755 "$INIT_SCRIPT"
        info "已安装启动脚本: $INIT_SCRIPT"

        # 尝试启用（OpenWrt方式）
        if [ -x "/etc/rc.common" ]; then
            "$INIT_SCRIPT" enable 2>/dev/null || warn "无法通过init.d启用，将使用rc.local方式"
        fi
    else
        warn "未找到S99memfix启动脚本，将使用rc.local方式"
    fi
}

# 配置rc.local开机自启（兼容小米固件）
install_rclocal() {
    info "配置开机自启..."

    # 确保rc.local存在且可执行
    if [ ! -f "$RC_LOCAL" ]; then
        echo "#!/bin/sh" > "$RC_LOCAL"
        echo "" >> "$RC_LOCAL"
        echo "exit 0" >> "$RC_LOCAL"
    fi
    chmod +x "$RC_LOCAL"

    # 检查是否已添加
    if grep -q "memfix" "$RC_LOCAL" 2>/dev/null; then
        info "rc.local已配置memfix开机自启"
    else
        # 在exit 0之前插入启动命令
        if grep -q "exit 0" "$RC_LOCAL"; then
            sed -i '/exit 0/i\# memfix 内存泄漏监控修复\n'"$INSTALL_BIN"'/memfix start\n' "$RC_LOCAL"
        else
            echo "" >> "$RC_LOCAL"
            echo "# memfix 内存泄漏监控修复" >> "$RC_LOCAL"
            echo "$INSTALL_BIN/memfix start" >> "$RC_LOCAL"
            echo "" >> "$RC_LOCAL"
            echo "exit 0" >> "$RC_LOCAL"
        fi
        info "已添加到 $RC_LOCAL 开机自启"
    fi
}

# 启动服务
start_service() {
    info "启动 memfix 服务..."
    "$INSTALL_BIN/memfix" start
    sleep 2

    # 验证
    if "$INSTALL_BIN/memfix" status >/dev/null 2>&1; then
        info "memfix 服务启动成功"
    else
        warn "服务状态检查失败，请手动执行: $INSTALL_BIN/memfix status"
    fi
}

# 显示安装信息
show_summary() {
    echo ""
    echo "============================================"
    echo "  memfix v${VERSION} 小米固件安装完成！"
    echo "============================================"
    echo ""
    echo "安装路径:"
    echo "  主程序:   $INSTALL_BIN/memfix"
    echo "  配置文件: $INSTALL_ETC/memfix.conf"
    echo "  启动脚本: $INIT_SCRIPT"
    echo "  开机自启: $RC_LOCAL"
    echo ""
    echo "注意事项:"
    echo "  1. 小米固件/var为tmpfs，重启后PID文件和日志会丢失"
    echo "     但程序仍在后台运行，不影响功能"
    echo "  2. 重启后程序通过rc.local自动启动"
    echo "  3. 如需持久化日志，可修改配置或源码"
    echo ""
    echo "常用命令:"
    echo "  memfix start      # 启动"
    echo "  memfix stop       # 停止"
    echo "  memfix restart    # 重启"
    echo "  memfix status     # 状态"
    echo "  memfix once       # 单次回收"
    echo ""
    echo "如果提示'memfix: not found'，请执行:"
    echo "  export PATH=\$PATH:$INSTALL_BIN"
    echo "  或重新登录SSH"
    echo ""
    echo "配置修改:"
    echo "  vi $INSTALL_ETC/memfix.conf"
    echo "  memfix restart"
    echo "============================================"
}

# 主流程
main() {
    echo "memfix v${VERSION} 小米固件安装脚本"
    echo "===================================="
    echo ""

    check_root
    detect_xiaomi || true
    check_arch
    check_files
    create_dirs
    install_bin
    install_config
    install_init
    install_rclocal
    start_service
    show_summary
}

main "$@"
