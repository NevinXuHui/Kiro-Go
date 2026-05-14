#!/bin/bash
# run.sh - 运行 Kiro-Go
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "=== 启动 Kiro-Go ==="

# 检查可执行文件
if [ ! -f "./kiro-go" ]; then
    echo -e "${RED}错误: 未找到 ./kiro-go${NC}"
    echo "请先运行: ./build.sh"
    exit 1
fi

# 读取端口配置
PORT=8991
if [ -f "data/config.json" ]; then
    PORT=$(grep -oP '"port"\s*:\s*\K[0-9]+' data/config.json | head -1)
    PORT=${PORT:-8991}
fi

# 检查端口占用
if command -v lsof &> /dev/null; then
    OCCUPIED=$(lsof -ti:$PORT 2>/dev/null || true)
    if [ -n "$OCCUPIED" ]; then
        echo -e "${YELLOW}⚠ 端口 $PORT 已被进程 $OCCUPIED 占用${NC}"
        read -p "是否结束该进程并继续？[y/N] " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            kill $OCCUPIED 2>/dev/null || true
            sleep 1
            echo -e "${GREEN}✓${NC} 已释放端口"
        else
            echo "取消启动"
            exit 1
        fi
    fi
fi

# 启动
echo -e "${GREEN}启动服务...${NC}"
echo "  管理面板: http://localhost:$PORT/admin"
echo "  Claude API: http://localhost:$PORT/v1/messages"
echo "  OpenAI API: http://localhost:$PORT/v1/chat/completions"
echo ""
echo "按 Ctrl+C 停止服务"
echo "---"

exec ./kiro-go
