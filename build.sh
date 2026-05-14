#!/bin/bash
# build.sh - 编译 Kiro-Go
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "=== 编译 Kiro-Go ==="

# 检查 Go
if ! command -v go &> /dev/null; then
    echo -e "${RED}错误: 未检测到 Go${NC}"
    exit 1
fi

# 检测并修复 GOROOT 版本不匹配
GO_VERSION=$(go version | awk '{print $3}')
CURRENT_GOROOT=$(go env GOROOT)

if [ -f "$CURRENT_GOROOT/VERSION" ]; then
    GOROOT_VERSION=$(cat "$CURRENT_GOROOT/VERSION" | head -1)
    if [[ "$GO_VERSION" != *"$GOROOT_VERSION"* ]]; then
        echo -e "${YELLOW}⚠ Go 版本不匹配，自动修复...${NC}"
        GO_BINARY=$(which go)
        if [ -L "$GO_BINARY" ]; then
            REAL_GOROOT=$(dirname $(dirname $(readlink -f "$GO_BINARY")))
            export GOROOT="$REAL_GOROOT"
            echo "  GOROOT=$GOROOT"
        fi
    fi
fi

# 编译
echo "编译中..."
go build -o kiro-go .

if [ $? -eq 0 ]; then
    FILE_SIZE=$(du -h kiro-go | cut -f1)
    echo -e "${GREEN}✓${NC} 编译成功: ./kiro-go ($FILE_SIZE)"
else
    echo -e "${RED}✗${NC} 编译失败"
    exit 1
fi
