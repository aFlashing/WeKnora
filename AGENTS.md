# AGENTS.md

本文件记录**本地二次开发**的 Git 工作流与注意事项。上游自带的 `cli/AGENTS.md` 是 CLI 组件的技术契约文档，与本文件无关，改动 CLI 时仍需遵守。

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

## 二次开发约定

- 新增依赖或修改构建配置时，同步更新对应的锁文件（`go.mod`/`go.sum`、前端 `package.json` 锁文件），避免与上游合并时产生大规模冲突。
- 尽量让改动落在独立的新文件或独立函数中，减少对上游文件的侵入式修改，可显著降低 `git sync` 的冲突概率。
- 改动前先确认上游是否已在相关分支实现相同能力，避免重复造轮子。
