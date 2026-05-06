# oc-go-cc Windows 构建与使用指南

这份文档面向 Windows 用户，介绍如何在 PowerShell 中构建、打包、启动并实际使用 `oc-go-cc`。

适用场景：

- 你在 Windows 上开发或测试这个项目
- 你的 PowerShell 没有 `make`
- 你希望直接生成 `bin/oc-go-cc.exe` 或 `dist` 发布文件
- 你希望额外生成一个可双击打开的控制面板 `bin/oc-go-cc-ui.exe`
- 你希望在 Windows 上把 `oc-go-cc` 接到 Claude Code 使用

## 1. 前置要求

需要准备：

- Windows PowerShell 或 PowerShell 7
- Git
- Go 1.25+
- OpenCode Go API Key
- Claude Code

检查 Go 是否可用：

```powershell
go version
```

如果提示找不到 `go`，可以安装：

```powershell
winget install GoLang.Go
```

安装后请重新打开一个新的 PowerShell 窗口，再执行：

```powershell
go version
```

## 2. 进入项目目录

```powershell
cd C:\project\oc-go-cc
```

如果你是自己克隆仓库，先执行：

```powershell
git clone https://github.com/samueltuyizere/oc-go-cc.git
cd oc-go-cc
```

## 3. 解决 Go 模块下载问题

有些网络环境下，默认的 `proxy.golang.org` 可能无法访问。推荐在 Windows 上先设置：

```powershell
go env -w GOPROXY direct
```

查看当前设置：

```powershell
go env GOPROXY
```

如果你所在环境可以正常访问 Go 官方代理，也可以不改这个设置。

## 4. 构建当前 Windows 可执行文件

这相当于 `make build`，但适用于 PowerShell。

```powershell
$version = git describe --tags --always --dirty 2>$null
if (-not $version) { $version = "dev" }

New-Item -ItemType Directory -Force -Path bin | Out-Null
go build -ldflags "-X main.version=$version" -o bin/oc-go-cc.exe ./cmd/oc-go-cc
```

构建成功后，产物在：

```powershell
bin\oc-go-cc.exe
```

检查文件是否存在：

```powershell
Get-Item .\bin\oc-go-cc.exe
```

## 4.1 构建控制面板 UI

如果你不想每次都手敲：

- `serve`
- `status`
- `stop`

现在可以额外构建一个本地控制面板程序 `oc-go-cc-ui.exe`。

推荐直接使用仓库里新增的 PowerShell 脚本：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-ui.ps1
```

构建成功后，产物在：

```powershell
bin\oc-go-cc-ui.exe
```

这个脚本默认会生成适合双击启动的 Windows GUI 程序，不会额外弹出控制台窗口。

如果你想生成带控制台窗口的版本，便于观察标准输出，也可以这样执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-ui.ps1 -NoWindowGui
```

注意：

- 最简单的用法是把 `oc-go-cc.exe` 和 `oc-go-cc-ui.exe` 放在同一个 `bin\` 目录下
- 控制面板会优先寻找和自己同目录的 `oc-go-cc.exe`
- 如果你的服务程序不在默认位置，可以启动 UI 时手动传 `--service-binary`

## 5. 运行项目测试

推荐至少先跑一次这几个核心包：

```powershell
go test ./internal/config ./internal/router ./internal/handlers
```

如果你想跑整个仓库：

```powershell
go test ./...
```

如果你想跑带 race detector 的完整测试：

```powershell
go test ./... -v -race
```

## 6. 生成发布包 dist

仓库现在已经补了 Windows 原生脚本：

```powershell
.\scripts\dist.ps1
```

推荐在 PowerShell 中这样执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -GoProxy direct
```

这个脚本会做以下事情：

- 清理旧的 `dist` 目录
- 交叉编译 6 个目标平台
- 生成 `checksums.txt`
- 列出所有产物

生成的文件位于：

```powershell
dist\
```

默认会生成：

- `oc-go-cc_darwin-amd64`
- `oc-go-cc_darwin-arm64`
- `oc-go-cc_linux-amd64`
- `oc-go-cc_linux-arm64`
- `oc-go-cc_windows-amd64.exe`
- `oc-go-cc_windows-arm64.exe`
- `checksums.txt`

### 6.1 dist.ps1 常用参数

指定版本号：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -Version v0.0.21
```

指定输出目录：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -OutputDir release
```

跳过 checksum：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -SkipChecksums
```

不清理旧目录：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -SkipClean
```

## 7. 初始化配置文件

先生成默认配置：

```powershell
.\bin\oc-go-cc.exe init
```

默认配置文件位置：

```powershell
$env:APPDATA\oc-go-cc\config.json
```

兼容规则：如果你已经有旧目录：

```powershell
$HOME\.config\oc-go-cc\config.json
```

程序会优先继续使用这个旧目录，不会强制切到 `$env:APPDATA\oc-go-cc`。

如果你已经有配置文件，命令会提示现有路径。

如果你要调整上游超时，可以在 `config.json` 里设置：

```json
"opencode_go": {
  "timeout_ms": 300000
}
```

这个值现在同时作用于普通请求和流式请求；如果 `deepseek-v4-pro` 这类模型思考更久，可以适当调大。

## 8. 配置 OpenCode Go API Key

当前 PowerShell 会话中设置：

```powershell
$env:OC_GO_CC_API_KEY = "sk-opencode-your-key-here"
```

如果你希望每次打开终端都生效，可以把它写入用户环境变量：

```powershell
[System.Environment]::SetEnvironmentVariable("OC_GO_CC_API_KEY", "sk-opencode-your-key-here", "User")
```

设置完成后，重新打开一个 PowerShell 窗口。

## 9. 配置 Claude Code 模型映射

编辑：

```powershell
notepad $env:APPDATA\oc-go-cc\config.json
```

重点关注这段：

```json
"claude_code": {
  "models": {
    "haiku": {
      "provider": "opencode-go",
      "model_id": "qwen3.5-plus"
    },
    "sonnet": {
      "provider": "opencode-go",
      "model_id": "kimi-k2.6"
    },
    "opus": {
      "provider": "opencode-go",
      "model_id": "deepseek-v4-pro",
      "reasoning_effort": "max",
      "thinking": { "type": "enabled" }
    }
  }
}
```

含义是：

- Claude Code 请求 `haiku` 时，转到 `qwen3.5-plus`
- Claude Code 请求 `sonnet` 时，转到 `kimi-k2.6`
- Claude Code 请求 `opus` 时，转到 `deepseek-v4-pro`

另外，现在如果 Claude Code 或 CC Switch 直接把完整模型名传给 `oc-go-cc`，例如：

- `deepseek-v4-pro`
- `qwen3.5-plus`
- `minimax-m2.7`

程序会优先按这个完整模型名直接使用对应模型。

只有当请求模型名既没有命中完整模型名，也没有命中 `haiku`、`sonnet`、`opus` 时，程序才会回退到场景路由。

## 10. 验证配置

```powershell
.\bin\oc-go-cc.exe validate
```

如果你把 API Key 配对了，通常会看到配置有效的输出。

## 11. 启动代理

### 11.1 使用控制面板启动和管理

如果你更希望用图形界面，而不是命令行，直接运行：

```powershell
.\bin\oc-go-cc-ui.exe
```

启动后会自动打开一个本地浏览器页面。这个页面可以直接完成：

- 启动服务
- 停止服务
- 查看当前运行状态和 PID
- 查看日志文件的最新内容
- 退出控制面板

控制面板本身只是一个本地网页服务，不会把数据发到外部；它只调用本机上的 `oc-go-cc.exe`、PID 文件和日志文件。

默认行为：

- UI 会自动打开浏览器
- 如果浏览器页面关闭且一段时间没有访问，控制面板进程会自动退出
- 服务本身是否继续运行，取决于你有没有点击“停止服务”

如果你不想自动打开浏览器，可以这样：

```powershell
.\bin\oc-go-cc-ui.exe --no-open
```

如果 `oc-go-cc.exe` 不在同目录，可以这样显式指定：

```powershell
.\bin\oc-go-cc-ui.exe --service-binary C:\project\oc-go-cc\bin\oc-go-cc.exe
```

如果你想让控制面板更久不自动退出，例如 30 分钟：

```powershell
.\bin\oc-go-cc-ui.exe --idle-timeout 30m
```

### 11.2 使用命令行启动代理

前台启动：

```powershell
.\bin\oc-go-cc.exe serve
```

后台启动：

```powershell
.\bin\oc-go-cc.exe serve -b
```

查看状态：

```powershell
.\bin\oc-go-cc.exe status
```

停止：

```powershell
.\bin\oc-go-cc.exe stop
```

### 11.3 开启调试日志

如果你要排查 Claude Code 到 `oc-go-cc` 的原始请求、转换后的上游请求，以及非流式上游响应预览，可以在配置里这样设置：

```json
"logging": {
  "level": "debug",
  "requests": true
}
```

然后重启：

```powershell
.\bin\oc-go-cc.exe serve
```

开启后可以看到：

- Claude Code 发来的原始请求体预览
- 解析后的 `temperature`、`top_p`、`max_tokens`、`stream` 等参数
- 发往 OpenCode Go 的请求体预览
- 非流式上游响应体预览
- 流式请求的超时设置和首包时间

说明：当前日志默认只打印截断预览，不会无限制输出整个 body。

## 12. 让 Claude Code 走本地代理

在你启动 `claude` 的那个 PowerShell 窗口里设置：

```powershell
$env:ANTHROPIC_BASE_URL = "http://127.0.0.1:3456"
$env:ANTHROPIC_AUTH_TOKEN = "unused"
```

然后启动 Claude Code：

```powershell
claude
```

这样 Claude Code 发到 Anthropic 的请求，就会先到 `oc-go-cc`，再转发到 OpenCode Go。

## 13. 常用命令速查

构建 Windows 可执行文件：

```powershell
$version = git describe --tags --always --dirty 2>$null
if (-not $version) { $version = "dev" }
New-Item -ItemType Directory -Force -Path bin | Out-Null
go build -ldflags "-X main.version=$version" -o bin/oc-go-cc.exe ./cmd/oc-go-cc
```

构建控制面板：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-ui.ps1
```

运行核心测试：

```powershell
go test ./internal/config ./internal/router ./internal/handlers
```

生成跨平台发布包：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -GoProxy direct
```

启动服务：

```powershell
.\bin\oc-go-cc.exe serve
```

启动控制面板：

```powershell
.\bin\oc-go-cc-ui.exe
```

## 14. 常见问题

### 14.1 `go` 不是内部或外部命令

说明 Go 没装好，或者 PATH 还没刷新。

先检查：

```powershell
go version
```

如果失败：

```powershell
winget install GoLang.Go
```

安装后关闭当前 PowerShell，重新打开再试。

### 14.2 `proxy.golang.org` 连接失败

先设置：

```powershell
go env -w GOPROXY direct
```

然后重试构建或测试。

### 14.3 `make` 命令不存在

Windows PowerShell 默认没有 `make`，这是正常现象。

直接使用：

- `go build ...` 构建本机二进制
- `scripts/dist.ps1` 生成 `dist` 发布包

### 14.4 Claude Code 没走代理

检查当前终端是否设置了：

```powershell
echo $env:ANTHROPIC_BASE_URL
echo $env:ANTHROPIC_AUTH_TOKEN
```

应当分别是：

- `http://127.0.0.1:3456`
- `unused`

### 14.5 `all models failed`

按顺序检查：

1. `OC_GO_CC_API_KEY` 是否正确
2. `oc-go-cc validate` 是否通过
3. OpenCode Go 服务是否可达
4. 你配置的 `claude_code.models` 或 `fallbacks` 是否填了不存在的 `model_id`

### 14.6 控制面板打开了，但启动服务失败

优先检查这几件事：

1. `bin\oc-go-cc.exe` 是否真的存在
2. `oc-go-cc-ui.exe` 是否和 `oc-go-cc.exe` 在同一个目录
3. 如果不在同目录，是否传了 `--service-binary`
4. 配置文件是否已经初始化并且 `OC_GO_CC_API_KEY` 可用
5. 日志面板里有没有更具体的错误信息

## 15. 推荐的 Windows 使用流程

如果你只是想尽快用起来，按下面顺序做：

1. 安装 Go：`winget install GoLang.Go`
2. 设置代理源：`go env -w GOPROXY direct`
3. 构建：生成 `bin\oc-go-cc.exe`
4. 可选：执行 `powershell -ExecutionPolicy Bypass -File .\scripts\build-ui.ps1`
5. 执行 `oc-go-cc.exe init`
6. 设置 `OC_GO_CC_API_KEY`
7. 修改 `config.json` 中的 `claude_code.models`
8. 执行 `oc-go-cc.exe validate`
9. 二选一：执行 `oc-go-cc.exe serve`，或者直接运行 `oc-go-cc-ui.exe` 后点击“启动服务”
10. 设置 `ANTHROPIC_BASE_URL` 和 `ANTHROPIC_AUTH_TOKEN`
11. 启动 `claude`

如果你要发版，再额外执行：

12. `powershell -ExecutionPolicy Bypass -File .\scripts\dist.ps1 -GoProxy direct`

这套流程已经覆盖了 Windows 下最常见的构建、配置、启动和排障路径。