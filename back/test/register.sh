#!/bin/bash

mkdir -p /tmp/peerdrive_test/folder
echo "file1 content 1" > /tmp/peerdrive_test/single.txt
echo "folder file1 content 1" > /tmp/peerdrive_test/folder/f1.txt
echo "folder file2 content 2" > /tmp/peerdrive_test/folder/f2.txt

SERVER_URL="http://localhost:3000"
TEST_DIR="/tmp/peerdrive_test"
SINGLE_FILE="$TEST_DIR/single.txt"
FOLDER_DIR="$TEST_DIR/folder"

sha256_file() {
    sha256sum "$1" | cut -d' ' -f1
}

file_size() {
    stat -c%s "$1" 2>/dev/null || stat -f%z "$1"
}

file_mime() {
    file --mime-type -b "$1" 2>/dev/null || echo "application/octet-stream"
}

echo "=== 测试单文件注册 ==="
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

EXPECTED_HASH=$(sha256_file "$SINGLE_FILE")
EXPECTED_SIZE=$(file_size "$SINGLE_FILE")

echo "期望 Hash: $EXPECTED_HASH"
echo "期望 Size: $EXPECTED_SIZE"

echo "=== 验证元数据 ==="
VERIFY_RESP=$(curl -s -x "" "$SERVER_URL/files/verify/$HASH")
echo "元数据响应: $VERIFY_RESP"

VERIFY_HASH=$(echo $VERIFY_RESP | grep -oP '(?<="hash":")[^"]*')
VERIFY_SIZE=$(echo $VERIFY_RESP | grep -oP '(?<="size":)[0-9]+')

if [ "$VERIFY_HASH" != "$EXPECTED_HASH" ]; then
    echo "错误: Hash 不匹配 (期望 $EXPECTED_HASH, 实际 $VERIFY_HASH)"
    exit 1
fi

if [ "$VERIFY_SIZE" != "$EXPECTED_SIZE" ]; then
    echo "错误: Size 不匹配 (期望 $EXPECTED_SIZE, 实际 $VERIFY_SIZE)"
    exit 1
fi

echo "元数据校验通过: Hash=$VERIFY_HASH, Size=$VERIFY_SIZE"

echo "=== 下载并对比内容 ==="
curl -x ""  -s -X GET "$SERVER_URL/sha256sum/$HASH" -o /tmp/downloaded_single.txt
DOWNLOAD_SIZE=$(stat -c%s /tmp/downloaded_single.txt 2>/dev/null || stat -f%z /tmp/downloaded_single.txt)
echo "下载文件大小: $DOWNLOAD_SIZE 字节"

if diff -q "$SINGLE_FILE" /tmp/downloaded_single.txt >/dev/null; then
    echo "成功: 下载的文件与原文件一致"
else
    echo "错误: 下载的文件与原文件不一致"
    echo "--- 原始文件内容 ---"
    cat "$SINGLE_FILE"
    echo "--- 下载文件内容 ---"
    cat /tmp/downloaded_single.txt
    exit 1
fi

echo -e "\n=== 测试文件夹注册 ==="
REG_FOLDER_RESP=$(curl -x ""  -s -X POST "$SERVER_URL/files/register_folder" \
    -H "Content-Type: application/json" \
    -d "{\"folder_path\": \"$FOLDER_DIR\"}")
echo "文件夹注册响应: $REG_FOLDER_RESP"

HASHES=$(echo "$REG_FOLDER_RESP" | grep -oP '"hash":"\K[^"]+')
if [ -z "$HASHES" ]; then
    echo "错误: 未从文件夹注册响应中获取到任何 hash"
    exit 1
fi

for H in $HASHES; do
    echo "测试下载 Hash: $H"
    curl -x "" -s -X GET "$SERVER_URL/sha256sum/$H" -o /tmp/downloaded_folder_file.txt
    MATCH=false
    for F in "$FOLDER_DIR"/*; do
        if diff -q "$F" /tmp/downloaded_folder_file.txt >/dev/null; then
            MATCH=true
            echo "文件 $F 与下载内容匹配"
            break
        fi
    done

    if [ "$MATCH" = true ]; then
        echo "成功: 下载的文件在原文件夹中找到了匹配项"
    else
        echo "错误: 下载的文件与文件夹中任何文件都不匹配"
        echo "文件夹内容:"
        ls -la "$FOLDER_DIR"
        echo "下载文件哈希: $(sha256sum /tmp/downloaded_folder_file.txt)"
        exit 1
    fi
done

echo -e "\n=== 测试重复注册（幂等性）==="
DUP_RESP=$(curl -x ""  -s -X POST "$SERVER_URL/files/register_local" \
    -H "Content-Type: application/json" \
    -d "{\"path\": \"$SINGLE_FILE\", \"filename\": \"single.txt\"}")
echo "重复注册响应: $DUP_RESP"

DUP_HASH=$(echo "$DUP_RESP" | grep -oP '"hash":"\K[^"]+')
if [ "$DUP_HASH" != "$HASH" ]; then
    echo "错误: 重复注册返回的 Hash 不同，期望 $HASH，实际 $DUP_HASH"
    echo "响应内容: $DUP_RESP"
    exit 1
fi

VERIFY_DUP=$(curl -x "" -s "$SERVER_URL/files/verify/$HASH")
echo "验证重复注册后的元数据: $VERIFY_DUP"
if ! echo "$VERIFY_DUP" | grep -q "\"size\":$EXPECTED_SIZE"; then
    echo "错误: 重复注册后元数据中 size 不正确"
    exit 1
fi

echo "成功: 重复注册幂等性验证通过"
echo -e "\n所有测试通过！"