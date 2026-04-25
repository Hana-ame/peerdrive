#!/bin/bash

SERVER_URL="http://localhost:3000"
TEST_DIR="/tmp/peerdrive_test"
SINGLE_FILE="$TEST_DIR/single.txt"
FOLDER_DIR="$TEST_DIR/folder"

echo "--- 测试单文件注册 ---"
# 1. 注册单文件
# 注意：我们使用绝对路径 /tmp/peerdrive_test/single.txt
REG_RESP=$(curl -s -x "" -X POST "$SERVER_URL/files/register_local" \
    -H "Content-Type: application/json" \
    -d "{\"path\": \"$SINGLE_FILE\", \"filename\": \"single.txt\"}")

echo "注册响应: $REG_RESP"
HASH=$(echo $REG_RESP | grep -oP '(?<="hash":")[^"]*')

if [ -z "$HASH" ]; then
    echo "错误: 无法从注册响应中获取 hash"
    exit 1
fi

echo "注册成功，Hash: $HASH"

# 2. 下载注册的文件
curl -s -x "" -X GET "$SERVER_URL/sha256sum/$HASH" -o /tmp/downloaded_single.txt
DIFF=$(diff $SINGLE_FILE /tmp/downloaded_single.txt)

if [ -z "$DIFF" ]; then
    echo "成功: 下载的文件与原文件一致"
else
    echo "错误: 下载的文件与原文件不一致"
    echo "$DIFF"
    exit 1
fi

echo -e "\n--- 测试文件夹注册 ---"
# 3. 注册文件夹
REG_FOLDER_RESP=$(curl -s -x "" -X POST "$SERVER_URL/files/register_folder" \
    -H "Content-Type: application/json" \
    -d "{\"folder_path\": \"$FOLDER_DIR\"}")

echo "文件夹注册响应: $REG_FOLDER_RESP"

# 提取注册文件的所有 hash
HASHES=$(echo $REG_FOLDER_RESP | grep -oP '(?<="hash":")[^"]*')

for H in $HASHES; do
    echo "测试下载 Hash: $H"
    curl -s -x "" -X GET "$SERVER_URL/sha256sum/$H" -o /tmp/downloaded_folder_file.txt
    # 检查下载的文件是否与文件夹中的任何一个文件匹配
    MATCH=false
    for F in $FOLDER_DIR/*; do
        if diff -q $F /tmp/downloaded_folder_file.txt > /dev/null; then
            MATCH=true
            break
        fi
    done
    
    if [ "$MATCH" = true ]; then
        echo "成功: 下载的文件在原文件夹中找到了匹配项"
    else
        echo "错误: 下载的文件与文件夹中任何文件都不匹配"
        exit 1
    fi
done

echo -e "\n所有测试通过！"
