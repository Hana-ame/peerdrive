@echo off
rem ===========================================================================
rem start-embeddinggemma.bat — Windows 一键启动独立 EmbeddingGemma embedding 服务
rem
rem 配合 peerdrive 的「外挂 EmbeddingGemma 加载器」(issue #371 / PEERDRIVE_EMBEDDING_*)
rem 使用。peerdrive 不做任何本地推理：它只是往 llama-server 的 /v1/embeddings
rem 发请求。本脚本负责把那个服务起起来。
rem
rem 使用方式：
rem   scripts\start-embeddinggemma.bat                     双击即可
rem   HOST=0.0.0.0 scripts\start-embeddinggemma.bat        局域网内可访问
rem   EMBED_PORT=8080 scripts\start-embeddinggemma.bat     改用 llama.cpp 常用端口
rem
rem 前置条件（都放在本仓库根目录，即本脚本的上一个目录）：
rem   llama-server.exe                                     与 llama.cpp 同版本
rem   embeddinggemma-2-UD-Q4_K_XL.gguf                     约 168 MB
rem      下载：https://huggingface.co/ggml-org/embeddinggemma-2-GGUF
rem
rem 起好服务后，让 peerdrive 用上它（见文件末尾的「peerdrive 侧配置」）。
rem ===========================================================================
setlocal EnableExtensions

set "ROOT=%~dp0.."
cd /d "%ROOT%"

rem ── 可调参数：在脚本前面 set 一次即可覆盖，例：set PORT=8080 ─────────────
if not defined HOST set "HOST=127.0.0.1"
if not defined PORT set "PORT=8000"
if not defined CTX set "CTX=8192"
if not defined MODEL set "MODEL=embeddinggemma-2-UD-Q4_K_XL.gguf"
if not defined BIN set "BIN=llama-server.exe"
set "CORES="

rem ── [1/4] 取物理核数 ──────────────────────────────────────────────────────
rem 用 NumberOfCores（物理核）而不是 NumberOfLogicalProcessors（含 SMT 的逻辑核）：
rem 线程数取逻辑核会把半数线程放在超线程兄弟上争抢同一物理执行单元，
rem 对这种小模型的 CPU 推理几乎只有开销没有收益，实测慢约一倍。
rem llama.cpp 另可加 --numa distribute 处理多路 CPU，按需打开。
for /f "usebackq delims=" %%n in (`powershell -NoProfile -Command "(Get-CimInstance Win32_Processor | Measure-Object NumberOfCores -Sum).Sum"`) do set "CORES=%%n"
if not defined CORES set "CORES=8"
if errorlevel 1 echo [warn] 取不到核数（powershell 不可用），退回 -t %CORES%

echo == [1/4] llama-server: %BIN%
echo == [2/4] 模型文件: %MODEL%
echo == [3/4] 线程数（物理核）: -t %CORES%
echo == [4/4] 监听: %HOST%:%PORT%

rem ── 可选加固项：需要时去掉 REM ────────────────────────────────────────────
rem 1) 只在回环地址监听（默认就是 127.0.0.1，只有你显式改成 0.0.0.0 才需要关）
rem    llama-server.exe -m %MODEL% --embedding -c %CTX% -t %CORES% --host 127.0.0.1 --port %PORT%
rem
rem 2) API key：加了之后 peerdrive 侧也要配 PEERDRIVE_EMBEDDING_API_KEY，
rem    请求会变成 Authorization: Bearer ***。
rem    支持逗号分隔多个 key。
rem    set API_KEY=change-me        ← 在 set PORT 那一段里赋值
rem    llama-server.exe -m %MODEL% --embedding -c %CTX% -t %CORES% --host %HOST% --port %PORT% --api-key %API_KEY%
rem
rem 3) 收紧 CORS：--cors-origins 默认 *（允许任意来源）。只从本机浏览器
rem    访问时用 localhost；从局域网前端访问就填那个前端的 origin。
rem    llama-server.exe ... --cors-origins localhost
rem
rem 4) 把 key 放到文件里而不是命令行（避免 ps 进程列表里能看到）：
rem    llama-server.exe ... --api-key-file keys.txt
rem
rem 5) 显式绑定 IPv4（某些系统上默认会往 IPv6 绑）：
rem    llama-server.exe ... --host 127.0.0.1

echo.
echo 启动命令：
echo   %BIN% -m %MODEL% --embedding -c %CTX% -t %CORES% --host %HOST% --port %PORT%
echo.
echo 起好后 peerdrive 侧对应设置：
echo   PEERDRIVE_EMBEDDING_ENABLE=1
echo   PEERDRIVE_EMBEDDING_URL=http://%HOST%:%PORT%/v1/embeddings
if defined API_KEY echo   PEERDRIVE_EMBEDDING_API_KEY=%API_KEY%
echo.
echo 自检（另一窗口）：
echo   curl http://%HOST%:%PORT%/v1/embeddings -X POST -H "Content-Type: application/json" ^
        -d "{\"input\":\"hello\",\"model\":\"embeddinggemma-2\"}"
echo   peerdrive embed "hello"          ← 用 peerdrive 自己的 CLI，-url 要一致
echo.
echo ===========================================================
echo  peerdrive 侧配置（写到 .env 或 start-local.sh 的 export 段）：
echo
echo  PEERDRIVE_EMBEDDING_ENABLE=1
echo  PEERDRIVE_EMBEDDING_URL=http://%HOST%:%PORT%/v1/embeddings
echo  PEERDRIVE_EMBEDDING_MODEL=embeddinggemma-2
echo  PEERDRIVE_EMBEDDING_TIMEOUT_SECS=30
echo  PEERDRIVE_EMBEDDING_API_KEY=                  ← 没设 API key 就留空
echo  PEERDRIVE_EMBEDDING_DIM=768                   ← 0 = 不做维度校验
echo
echo  HTTP 端点（都在 authRequired 后面）：
echo    POST /embedding/v1/embed        {"text":"..."}
echo    POST /embedding/v1/embed/batch  {"texts":["a","b"]}
echo    POST /embedding/v1/ping         探测可达性
echo    GET  /embedding/v1/status       只看配置，不推理
echo ===========================================================
echo.

if not exist "%BIN%" (
    echo [错误] 未找到 %BIN%，放到仓库根目录：%ROOT%
    echo        llama.cpp release → https://github.com/ggml-org/llama.cpp/releases
    exit /b 1
)
if not exist "%MODEL%" (
    echo [错误] 未找到 %MODEL%，放到仓库根目录：%ROOT%
    echo        下载：https://huggingface.co/ggml-org/embeddinggemma-2-GGUF
    exit /b 1
)

"%BIN%" -m "%MODEL%" --embedding -c %CTX% -t %CORES% --host %HOST% --port %PORT%
set "RC=%ERRORLEVEL%"
if not "%RC%"=="0" (
    echo.
    echo [错误] llama-server 退出码 %RC%
    echo        常见原因：端口被占用（换个 PORT）、模型文件损坏、GGUF 与 llama-server 版本不匹配。
)
exit /b %RC%