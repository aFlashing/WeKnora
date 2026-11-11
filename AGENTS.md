# AGENTS.md

本文件记录**本地二次开发**的 Git 工作流、桌面端部署方式与运行约定。上游自带的 `cli/AGENTS.md` 是 CLI 组件的技术契约文档，与本文件无关，改动 CLI 时仍需遵守。

> 运行时形态约定：本项目**只使用桌面端（WeKnora Lite）**，不通过 Docker / 服务端二进制运行。详见「桌面端（唯一运行形态）」。`

## 仓库拓扑

| 远端 | 地址 | 用途 | 权限 |
| --- | --- | --- | --- |
| `origin` | `https://github.com/aFlashing/WeKnora.git` | 个人 fork，存放二次开发成果 | 可推送 |
| `upstream` | `https://github.com/Tencent/WeKnora.git` | 官方主仓库，只读拉取 | 无推送权限 |

- `main`：跟踪 `upstream/main`，推送指向 `origin`。**保持与官方一致，不在此分支写业务代码。**
- `dev`：跟踪 `origin/dev`。**所有二次开发在此分支进行。**
- 提交身份：`TY <aFlashing@users.noreply.github.com>`（仓库级配置）。
- 历史完整（非浅克隆），首次导入时已执行 `git fetch --unshallow`。

## 日常命令

```powershell
cd "d:\软件项目\Weknora"

git sync                  # fetch upstream + merge upstream/main 到当前分支
git add -A
git commit -m "feat: ..."
git push                  # 推送到 origin 的当前分支
```

`git sync` 是本仓库的本地别名，等价于 `git fetch upstream && git merge upstream/main`，仅在当前仓库生效。

## 融合上游更新

1. 确认当前在 `dev` 分支且工作区干净（`git status`）。
2. 执行 `git sync`。
3. 出现冲突时，在 IDE 中逐个解决后：
   ```powershell
   git add <冲突文件>
   git merge --continue
   ```
4. **在 `dev` 上执行 `git pull` 拉的是自己的 fork，不是官方更新。** 拉官方必须用 `git sync`。
5. 需要对照纯净上游时：先提交或 stash 当前改动，再 `git checkout main && git pull`，看完切回 `dev`。

## 禁止操作

- 禁止 `git reset --hard`、`git checkout -- .`、`git clean -fd`：会丢弃未提交的二次开发成果。
- 禁止对 `main` 分支做本地提交，否则后续 `git pull` 会产生分叉。
- 禁止 `git push upstream`：无权限，且无意义。
- 谨慎对已推送的 `dev` 做 `git rebase`：会重写历史，导致下次推送需强推。若确要 rebase，先确认远端无协作者。
- 禁止用「重新下载压缩包覆盖目录」的方式更新代码，会绕过 Git 并丢失全部本地提交。

## 环境注意事项

- `.gitignore` 第 2 行为 `.*`，**所有以点开头的文件/目录默认不被跟踪**（`.env` 因此不会入库，这是预期行为）。新增需要入库的点文件用 `git add -f <文件>`。
- 严禁提交密钥、Token、`.env`。参考 `.env.example` 生成本地 `.env`。
- Git 直连 GitHub 会 `Connection was reset`，已配置 GitHub 专用代理 `http.https://github.com/.proxy = http://127.0.0.1:7897`（写入 `~/.gitconfig`）。代理未启动时 `git sync` / `git push` 会失败，需先启动本地代理。
- Git 安装于 `D:\Program Files\Git`（2.56.1）。若终端报 `git 不是内部或外部命令`，说明该终端进程早于安装启动，重启终端或 IDE 即可，无需重启系统。
- 工程路径含中文与空格，所有 Shell 命令中的路径必须加引号。
- 首次 `push` 由 Git Credential Manager 完成浏览器授权，凭据随后缓存；若报认证失败，用 Personal Access Token 作为密码。

## 桌面端（唯一运行形态）

**本项目只以桌面端形态运行与验收。** 不要再启动 `docker-compose.yml`、`cmd/server`（服务端版）或 `frontend` 的 `vite` dev server；所有功能验证都通过桌面应用完成。

### 形态与代码位置

- 桌面端源码：`cmd/desktop/`，基于 **Wails v2**，产品名 `WeKnora Lite`。
- 架构：内嵌 Gin 后端（随机回环端口）+ Wails WebView（反向代理到内嵌后端）+ SQLite 本地存储，**不依赖 Docker、Redis、外部数据库**。
- 前端产物路径固定为仓库根的 `web/`（后端从 `./web` 提供 SPA），不是 Go embed。
- 官方说明：`website-docs/05-clients/05-desktop.md`；运行时配置模板：`.env.lite.example`。

### 本机部署位置（全部在 D 盘）

| 用途 | 路径 |
| --- | --- |
| 应用目录（exe + config + migrations + web + licenses + .env） | `D:\DevEnv\WeKnora Lite\` |
| 数据目录（SQLite、上传文件、分词字典、日志） | `D:\DevEnv\WeKnoraLiteData\` |
| 启动入口 | 桌面与开始菜单的 `WeKnora Lite.lnk` |

- 数据库：`D:\DevEnv\WeKnoraLiteData\weknora.db`（`DB_PATH` 绝对路径注入）。
- 文件存储：`D:\DevEnv\WeKnoraLiteData\files`。
- 偏好与端口设置：`%AppData%\WeKnora Lite\desktop-prefs.json`（`http_port` 为 0 时每次启动随机端口）。
- 运行时按 exe 所在目录**向上查找**含 `config/config.yaml` 的目录作为配置根，并 chdir 到该目录；因此 exe 必须与 `config/`、`migrations/`、`web/` 放在同一层。

### 构建工具链（全部在 D 盘）

| 组件 | 路径 / 值 |
| --- | --- |
| Go | `D:\Program Files\Go`（1.27.3） |
| GOPATH / GOBIN / GOMODCACHE | `D:\DevEnv\GoPath` / `...\bin` / `...\pkg\mod` |
| Node / npm | `D:\DevEnv\nodejs`（26.13.0 / 11.20.0） |
| npm 全局目录 / 缓存 | `D:\DevEnv\npm-global` / `D:\DevEnv\npm-cache` |
| GCC (CGO) | `D:\DevEnv\winlibs\mingw64\bin`（16.2.0） |
| Wails CLI | `D:\DevEnv\GoPath\bin\wails.exe`（v2.12.0） |
| SQLite 头文件 | `D:\DevEnv\sqlite3\include\sqlite3.h` |
| 中文分词字典 | `D:\DevEnv\WeKnoraLiteData\jieba-dict` |
| Go 代理 | `GOPROXY=https://goproxy.cn,direct`（官方源在国内超时） |
| npm 镜像 | `registry=https://registry.npmmirror.com`，`replace-registry-host=npmjs`（保持 `cdn.sheetjs.com` 原样，否则 `xlsx` 报 422） |

### 构建流程

**不要用 `wails build`。** Go 1.27 与 Wails 2.12 内置的 `go/packages` 不兼容，会报
`internal error: package "bytes" without types was imported from ...sqlite-vec-go-bindings/cgo`，
且 `-skipbindings` 也无法绕过（前端绑定 `frontend/src/wailsjs/` 已存在，不需要重新生成）。改用 `go build` 直接编译。

```powershell
# 0) 环境
$env:Path = "D:\DevEnv\nodejs;D:\DevEnv\winlibs\mingw64\bin;D:\DevEnv\GoPath\bin;D:\Program Files\Go\bin;" + $env:Path
$env:GOPATH = "D:\DevEnv\GoPath"
$env:CGO_ENABLED = "1"
$env:CGO_CFLAGS = "-Wno-deprecated-declarations -ID:/DevEnv/sqlite3/include"

# 1) 前端 → web/
cd "d:\软件项目\Weknora\frontend"
npm ci --no-audit --no-fund          # 仅依赖变动时需要
npm run build
cd "d:\软件项目\Weknora"
Remove-Item web -Recurse -Force -ErrorAction SilentlyContinue
Copy-Item frontend\dist -Destination web -Recurse

# 2) 编译桌面程序
cd "d:\软件项目\Weknora\cmd\desktop"
$ld = "-s -w -H windowsgui " +
      "-X github.com/Tencent/WeKnora/internal/handler.Version=0.9.0 " +
      "-X github.com/Tencent/WeKnora/internal/handler.Edition=lite " +
      "-X github.com/Tencent/WeKnora/internal/handler.CommitID=$(git rev-parse --short HEAD) " +
      "-X github.com/Tencent/WeKnora/internal/handler.BuildTime=2026-11-11T10-00-00Z " +
      "-X github.com/Tencent/WeKnora/internal/handler.GoVersion=go1.27.3"
go build -tags "desktop sqlite_fts5" -trimpath -ldflags "$ld" -o "WeKnora Lite.exe" .

# 3) 部署（先停掉运行中的实例，否则文件被占用）
Stop-Process -Name "WeKnora Lite" -Force -ErrorAction SilentlyContinue
Copy-Item "WeKnora Lite.exe" "D:\DevEnv\WeKnora Lite\" -Force
Remove-Item "WeKnora Lite.exe"
Remove-Item "D:\DevEnv\WeKnora Lite\web" -Recurse -Force -ErrorAction SilentlyContinue
Copy-Item "d:\软件项目\Weknora\web" -Destination "D:\DevEnv\WeKnora Lite\web" -Recurse
```

关键点：

- `Edition=lite` **必须**通过 ldflags 注入，否则内嵌 SPA 与 auto-setup 的行为会退化；`cmd/desktop/main.go` 里也有 `ensureDesktopLiteEdition()` 兜底。
- `-H windowsgui` 决定是否带黑色控制台窗口；不加则会出现控制台。
- `-s -w` 把产物从约 453 MB 压到约 262 MB。
- 构建标签必须是 `desktop sqlite_fts5`：`desktop` 由 `wails.json` 提供（启用本机沙箱），`sqlite_fts5` 启用 SQLite 全文检索。
- 产物不要提交进仓库（构建后删除），部署副本放在 `D:\DevEnv\WeKnora Lite\`。
- `frontend/package.json` 中的 `allowScripts` 段是本机 npm 11 的脚本白名单（`esbuild`、`@vue-office/pptx`、`vue-demi`），属于构建必需配置，不要删除。

### 运行时配置与坑

- **`JIEBA_DICT_DIR` 必须写成用户环境变量**（已设为 `D:\DevEnv\WeKnoraLiteData\jieba-dict`）。`types.Jieba` 是包级变量，在 `main()` 之前初始化，此时 `.env` 尚未加载；只写进 `.env` 会启动即 panic：`Dictionary file does not exist: .../jieba.dict.utf8`。
- **`DUCKDB_SKIP_EXTENSION_LOAD=1` 已写入 `.env`**。首次启动会从 `extensions.duckdb.org` 下载 `spatial`/`excel` 扩展，网络受限时会长时间卡住且无窗口。代价是 Excel 数据分析不可用；需要时注释掉该行并在可联网环境下等待首次下载完成（扩展会被缓存）。
- `DB_PATH`、`LOCAL_STORAGE_BASE_DIR` 必须用绝对路径，否则按 exe 所在目录解析，可能写进 C 盘用户目录。
- 桌面版免登录：启动时生成一次性 token，首次自动创建 `admin@weknora.local`。
- 端口默认随机（`desktop-prefs.json` 中 `http_port=0`）。需要固定端口就修改该文件的 `http_port` 后重启。

### 验收与排障

- 正常标志：进程存活、`Get-NetTCPConnection -OwningProcess <PID> -State Listen` 有回环端口、`http://127.0.0.1:<port>/` 返回 200、`/api/v1/*` 返回 401（未带桌面 token，属预期）、`MainWindowHandle` 非 0。
- GUI 程序没有控制台，闪退时用重定向抓错误：
  ```powershell
  Start-Process "D:\DevEnv\WeKnora Lite\WeKnora Lite.exe" `
    -WorkingDirectory "D:\DevEnv\WeKnora Lite" `
    -RedirectStandardOutput "D:\DevEnv\WeKnoraLiteData\out.log" `
    -RedirectStandardError  "D:\DevEnv\WeKnoraLiteData\err.log"
  ```
- 修改前端或 Go 代码后**必须重新构建并同步 `web/`**，否则界面仍是旧产物；只改前端也应重跑第 1、3 步（`web/` 是外部目录，非 embed）。
- `git sync` 拉到上游对 `cmd/desktop` 或 `frontend` 的改动后，需要完整重跑构建流程；若上游修复了 Wails/Go 版本兼容问题，可再尝试 `wails build`。

## 二次开发约定

- 新增依赖或修改构建配置时，同步更新对应的锁文件（`go.mod`/`go.sum`、前端 `package.json` 锁文件），避免与上游合并时产生大规模冲突。
- 尽量让改动落在独立的新文件或独立函数中，减少对上游文件的侵入式修改，可显著降低 `git sync` 的冲突概率。
- 改动前先确认上游是否已在相关分支实现相同能力，避免重复造轮子。
