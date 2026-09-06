# ============================================
# memfix Makefile
# 小米AX3000T(联发科版) 内存泄漏监控修复工具
# ============================================

# 项目信息
PROJECT    := memfix
VERSION    := 1.0.0
AUTHOR     := memfix contributors
LICENSE    := MIT

# 源文件
SRCDIR     := src
BUILDDIR   := build
TARGET     := $(BUILDDIR)/$(PROJECT)

# 源码
SOURCES    := $(SRCDIR)/memfix.c
HEADERS    := $(SRCDIR)/memfix.h

# 默认编译器（本地x86编译，用于测试）
CC         ?= gcc
CFLAGS     ?= -O2 -Wall -Wextra -Werror -std=c11
LDFLAGS    ?=

# 交叉编译配置
# 使用方法: make CROSS=aarch64-linux-musl-
# 或:     make cross
CROSS      ?=
CROSS_CC   := $(CROSS)gcc

# 安装路径（路由器上）
PREFIX     ?= /usr
SBINDIR    := $(PREFIX)/sbin
ETCDIR     := /etc
INITDIR    := /etc/init.d

# ============================================
# 默认目标
# ============================================
.PHONY: all
all: $(TARGET)

# 创建构建目录
$(BUILDDIR):
	mkdir -p $(BUILDDIR)

# 编译
$(TARGET): $(SOURCES) $(HEADERS) | $(BUILDDIR)
	$(CC) $(CFLAGS) -o $@ $(SOURCES) $(LDFLAGS)
	@echo "编译完成: $@"
	@ls -lh $@

# ============================================
# 交叉编译（aarch64 / ARM64）
# ============================================
.PHONY: cross
cross:
	$(MAKE) CC=$(CROSS_CC) CFLAGS="$(CFLAGS) -static" LDFLAGS="$(LDFLAGS) -static" TARGET=$(BUILDDIR)/$(PROJECT)-aarch64 all

# 快捷方式：使用musl交叉编译
.PHONY: cross-musl
cross-musl:
	$(MAKE) cross CROSS=aarch64-linux-musl-

# 快捷方式：使用gnu交叉编译
.PHONY: cross-gnu
cross-gnu:
	$(MAKE) cross CROSS=aarch64-linux-gnu-

# ============================================
# 本地编译（用于开发测试）
# ============================================
.PHONY: debug
debug: CFLAGS += -g -DDEBUG -O0
debug: $(TARGET)

.PHONY: test
test: debug
	@echo "运行测试..."
	./$(TARGET) once || true
	./$(TARGET) status || true

# ============================================
# 静态分析
# ============================================
.PHONY: analyze
analyze:
	@which cppcheck >/dev/null 2>&1 && cppcheck --enable=all --inconclusive $(SOURCES) || echo "cppcheck 未安装"
	@which clang-tidy >/dev/null 2>&1 && clang-tidy $(SOURCES) -- $(CFLAGS) || echo "clang-tidy 未安装"

# ============================================
# 代码格式化
# ============================================
.PHONY: format
format:
	@which clang-format >/dev/null 2>&1 && clang-format -i $(SOURCES) $(HEADERS) || echo "clang-format 未安装"

# ============================================
# 安装（在路由器上执行）
# ============================================
.PHONY: install
install: $(TARGET)
	install -d $(DESTDIR)$(SBINDIR)
	install -m 755 $(TARGET) $(DESTDIR)$(SBINDIR)/$(PROJECT)
	install -d $(DESTDIR)$(ETCDIR)
	install -m 644 config/memfix.conf $(DESTDIR)$(ETCDIR)/memfix.conf
	install -d $(DESTDIR)$(INITDIR)
	install -m 755 init/S99memfix $(DESTDIR)$(INITDIR)/memfix
	@echo "安装完成"
	@echo "  可执行文件: $(DESTDIR)$(SBINDIR)/$(PROJECT)"
	@echo "  配置文件:   $(DESTDIR)$(ETCDIR)/memfix.conf"
	@echo "  启动脚本:   $(DESTDIR)$(INITDIR)/memfix"

# ============================================
# 打包发布
# ============================================
.PHONY: release
release: cross-musl
	@mkdir -p release
	@cp $(BUILDDIR)/$(PROJECT)-aarch64 release/$(PROJECT)
	@cp config/memfix.conf release/
	@cp init/S99memfix release/
	@cp README.md release/
	@cp docs/USAGE.md release/ 2>/dev/null || true
	@cd release && tar czf ../$(PROJECT)-$(VERSION)-aarch64.tar.gz .
	@echo "发布包已生成: $(PROJECT)-$(VERSION)-aarch64.tar.gz"

# ============================================
# 清理
# ============================================
.PHONY: clean
clean:
	rm -rf $(BUILDDIR) release *.tar.gz
	@echo "已清理"

.PHONY: distclean
distclean: clean
	rm -rf toolchain
	@echo "已深度清理"

# ============================================
# 帮助
# ============================================
.PHONY: help
help:
	@echo "$(PROJECT) v$(VERSION) - 小米AX3000T(联发科版) 内存泄漏监控修复工具"
	@echo ""
	@echo "常用目标:"
	@echo "  make            本地编译（x86，用于测试）"
	@echo "  make cross      交叉编译（需设置CROSS，如 CROSS=aarch64-linux-musl-）"
	@echo "  make cross-musl 使用musl交叉编译（静态链接）"
	@echo "  make cross-gnu  使用gnu交叉编译"
	@echo "  make debug      调试版本编译"
	@echo "  make test       编译并运行测试"
	@echo "  make install    安装到系统（路由器上执行）"
	@echo "  make release    交叉编译并打包发布"
	@echo "  make clean      清理构建文件"
	@echo "  make format     格式化代码"
	@echo "  make analyze    静态代码分析"
	@echo ""
	@echo "交叉编译工具链获取:"
	@echo "  musl:  https://musl.cc/aarch64-linux-musl-cross.tgz"
	@echo "  或使用系统包管理器: apt install gcc-aarch64-linux-gnu"
