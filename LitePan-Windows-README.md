---
AIGC:
    Label: "1"
    ContentProducer: 001191440300708461136T1XGW3
    ProduceID: d6785ed2d245d7cd2ecd4bb869105e21_436faafcba6e11f1a1bf52540064ee0f
    ReservedCode1: 7EcG6uJpx6lC3X2dJD1YO9+4E0lfZicvOBHzDW008aCT/fvIu+v9mhJc9jQX1Kx08qz+IIaAhKuWclrpIw9/QGtiEh773cMwWHV5pObLadJbJtTom8arju7j93G3jMZKbQfSyJQmD+2LQXfdRcMjCs2kZL0jVSXhrfgEvsmFZ+qbuSONLs6En+sQdCE=
    ContentPropagator: 001191440300708461136T1XGW3
    PropagateID: d6785ed2d245d7cd2ecd4bb869105e21_436faafcba6e11f1a1bf52540064ee0f
    ReservedCode2: 7EcG6uJpx6lC3X2dJD1YO9+4E0lfZicvOBHzDW008aCT/fvIu+v9mhJc9jQX1Kx08qz+IIaAhKuWclrpIw9/QGtiEh773cMwWHV5pObLadJbJtTom8arju7j93G3jMZKbQfSyJQmD+2LQXfdRcMjCs2kZL0jVSXhrfgEvsmFZ+qbuSONLs6En+sQdCE=
---

# LitePan Windows 版

LitePan Windows 版是开源多网盘聚合挂载/管理工具 [LitePan](https://github.com/Ponphil/LitePan) 的 Windows 原生移植，无 Docker、无 WSL，单个 exe 即可运行。

## 特性

- **Windows 原生运行**：不依赖 Docker、WSL 或虚拟机，下载即用
- **单文件分发**：Go 后端 + Vue 3 前端构建产物通过 `go:embed` 内嵌为单一 exe，无需单独部署前端
- **多网盘聚合**：聚合管理多个网盘存储，提供统一 Web 界面
- **跨盘浏览**：修复原版 Linux 路径语义限制，支持枚举并浏览本地盘 / 可移动盘 / 光驱 / 网络映射盘 / 虚拟盘
- **文件系统挂载（WinFsp）**：基于 WinFsp + cgofuse 实现盘符挂载，挂载盘可在资源管理器「此电脑」正常显示
- **完整 Web / 存储 / API 功能**：与上游功能对齐，单 exe 内嵌前端与后端
- **默认账号开箱即用**：默认管理员账号 `admin/admin`，首次登录后请立即修改密码

## 项目来源与许可

- **上游项目**：[LitePan](https://github.com/Ponphil/LitePan)（Go 语言编写，多网盘聚合挂载/管理工具），作者 Ponphil
- **官方分发形态**：官方仅提供 Docker 部署（镜像 `ponphil/litepan:beta`），**无 Windows 原生版**
- **本项目定位**：将 LitePan 移植为 Windows 原生可执行程序（无 Docker、无 WSL），并修复 Windows 平台下的路径与存储访问问题
- **上游许可证**：本项目继承上游许可证 **PolyForm Noncommercial 1.0.0**
  - 该许可**仅允许非商业用途**，任何商业使用（包括内部商业部署、商业 SaaS、销售、转授权等）均需另行获得授权
  - 再分发时请完整保留许可证文本与版权声明，并确认你的分发与使用场景符合该许可条款
  - **合规提示**：若需商用，请联系原作者获得商业授权，或改用兼容许可；开源发布前请自行完成合规审查

## 实现原理

### 架构：Go 后端 + Vue3/Vite 前端，go:embed 单 exe

- 后端为 Go 编写的 Web/存储/API 服务；前端使用 Vue 3 + Vite 构建
- 前端构建产物输出到 `internal/api/web` 目录，通过 `go:embed` 内嵌进后端二进制
- 最终产物为**单一 exe**，运行时无需额外部署前端静态文件，也无需外部 Web 服务器

### FUSE 与 WinFsp 挂载处理

Windows 没有 FUSE 内核支持，无法直接使用 `go-fuse` 做文件系统挂载，本项目采用两条路径并存：

1. **build-nofuse 构建路径**：项目自带 `build-nofuse` 目标（见 Makefile）。编译时不带 `fuse` tag，完全跳过 go-fuse 依赖，Web / 存储 / API 功能完整可用。这是 Windows 下的默认推荐构建方式
2. **WinFsp + cgofuse 挂载路径**（已实现）：引入 [cgofuse](https://github.com/winfsp/cgofuse) 的 WinFsp 后端，实现真实盘符挂载：
   - 挂载通过 `internal/share/fuse/manager_winfsp.go` 的 `host.Mount` 异步执行，以 `Init()` 回调 + 超时兜底判定挂载成功
   - 盘符根挂载点（如 `L:\`）经 `toWinfspMountPoint` 归一化为 `\\?\L:`，走内核 Mount Manager 全局注册，与进程是否管理员无关，挂载盘在资源管理器「此电脑」中正常可见
   - 前端「挂载管理」提供挂载点选择弹窗（`MountPointPickerModal.vue`，列出 A-Z 全部盘符，已占用盘符禁用）
3. **fuse tag 兼容路径**：为让带 fuse tag 的构建也能在 Windows 下编译通过：
   - 将 go-fuse 依赖复制到本地 `third_party/go-fuse` 目录
   - 通过 `go.mod` 的 `replace` 指令指向本地副本
   - 补充 `fallocate_windows.go` 等 Windows 占位实现，补齐平台缺失的系统调用

### 跨盘浏览修复

原 `internal/api/local_fs.go` 按 Linux 语义实现：只接受 `/` 开头的路径、无盘符枚举、盘根父级会死循环，导致 Windows 下只能浏览 C 盘。修复内容：

1. **路径校验**：改用 `filepath.IsAbs` 判断绝对路径，兼容 Windows 盘符路径语义
2. **盘符枚举**：新增 `local_fs_windows.go`，枚举 `A:\` 至 `Z:\` 盘符，用 `os.Stat` 探测可用性，覆盖本地盘 / 可移动盘 / 光驱 / 网络映射盘 / 虚拟盘
3. **盘根防死循环**：当路径位于盘符根目录时，`parentPath` 返回 `nil`，避免无限向上递归
4. **前端适配**：`LocalDirBrowserModal.vue` 面包屑适配反斜杠路径，并新增**盘符快捷区**，方便在各盘之间快速切换

## 环境要求

| 组件 | 版本要求 | 说明 |
| --- | --- | --- |
| Go | 1.26.6+ | `go.mod` 声明 `go 1.26.6`，低于此版本无法编译 |
| Node.js + npm | 建议 Node 20+ | 用于构建前端（Vue 3 + Vite） |
| 操作系统 | Windows 10 / 11 | 原生运行，无需 Docker / WSL |

> 若本机 Go 版本不足，可从 <https://golang.google.cn/dl/go1.26.6.windows-amd64.zip> 下载 zip 包，解压到本地目录后使用 `GOTOOLCHAIN=local` 并让 `PATH` 指向其 `go/bin` 进行编译，无需安装器。

## 编译方法

### 1. 构建前端

```powershell
cd web
npm ci
npm run build
```

构建产物输出到 `internal/api/web`，供 `go:embed` 内嵌使用。

### 2. 编译后端（Windows 原生 exe）

务必**显式指定平台**，避免全局 GOENV 残留的 `GOOS=linux` 产出 Linux ELF：

```powershell
$env:GOOS = "windows"
$env:GOARCH = "amd64"
go build -o output/litepan.exe ./cmd/litepan
```

> 也可以直接使用项目自带的目标：`make build-nofuse`（不启用 fuse tag，跳过 go-fuse 依赖）。

### 3. 校验产物

检查输出文件头部两个字节是否为 PE 魔数 `4D 5A`（即 ASCII 字符 `MZ`），确认是 Windows PE 可执行文件：

```powershell
Format-Hex output\litepan.exe | Select-Object -First 1
# 期望前两字节: 4D 5A
```

### Go 工具链不足时的下载替代方案

若本机 Go 版本低于 1.26.6，无需安装器，可直接下载官方 zip 包并本地解压使用：

```powershell
# 下载 Go 1.26.6 Windows amd64 工具链
Invoke-WebRequest -Uri "https://golang.google.cn/dl/go1.26.6.windows-amd64.zip" -OutFile "$env:TEMP\go1.26.6.windows-amd64.zip"
Expand-Archive "$env:TEMP\go1.26.6.windows-amd64.zip" -DestinationPath "D:\tools\go1.26.6"

# 编译时使用本地工具链（PATH 指向其 go/bin）
$env:GOTOOLCHAIN = "local"
$env:Path = "D:\tools\go1.26.6\go\bin;" + $env:Path
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -o output/litepan.exe ./cmd/litepan
```

## 运行方式

```powershell
output\litepan.exe --listen=127.0.0.1:5211 --data-dir=<数据目录>
```

| 参数 | 说明 |
| --- | --- |
| `--listen` | 监听地址与端口，默认示例 `127.0.0.1:5211` |
| `--data-dir` | 数据目录，用于存放应用数据 |

启动后浏览器访问 `http://127.0.0.1:5211`。

- **默认账号**：`admin / admin`
- **安全提示**：首次登录后请立即修改默认密码，避免未授权访问

## 已知限制

- **挂载需安装 WinFsp**：文件系统挂载依赖 [WinFsp](https://winfsp.dev/) 运行时，使用挂载功能前需先安装
- **开机自启需自行注册**：Windows 版不提供自启安装，可用 NSSM / WinSW 注册为 Windows 服务
- **rtf.js 临时补丁**：Vite 8（rolldown）对 rtf.js 源码 `export { X, IType }` 类型混导报 `MISSING_EXPORT`，需在 `node_modules` 中修改 `wmfjs/index.ts`、`emfjs/index.ts`、`rtfjs/index.ts` 三处为 `export type { IType }`。该补丁是 `node_modules` 内的**临时修改**，重新 `npm ci` 后需重新应用（建议贡献给上游或改用 patch-package 管理）
- **UNC 路径限制**：数据目录选择器仅支持有盘符的存储；未映射盘符的 UNC 网络路径（`\\server\share`）需先在系统中映射为盘符

## 项目结构

```
LitePan-Windows/
├── cmd/
│   └── litepan/            # 程序入口
├── internal/
│   └── api/
│       ├── local_fs.go             # 本地文件服务（跨平台路径语义）
│       ├── local_fs_windows.go     # Windows 盘符枚举实现
│       └── web/                    # go:embed 内嵌的前端构建产物
├── third_party/
│   └── go-fuse/                    # go-fuse 本地副本（go.mod replace 指向）
├── web/                            # 前端源码（Vue 3 + Vite）
│   └── src/components/LocalDirBrowserModal.vue   # 目录选择器（盘符快捷区）
├── go.mod / go.sum
├── Makefile                        # 含 build-nofuse 目标
└── output/
    └── litepan.exe                 # 编译产物
```

## FAQ

**Q1：支持哪些盘符 / 存储？**

A：支持所有带盘符的本地与映射存储，包括本地硬盘、可移动盘（U 盘）、光驱、网络映射盘（已映射的 SMB/NFS 盘符）以及虚拟盘（如 VHD 挂载、subst 虚拟盘）。启动时通过枚举 `A:\` ~ `Z:\` 并 `os.Stat` 探测可用性。

**Q2：支持 UNC 路径（`\\server\share`）吗？**

A：数据目录选择器**仅支持有盘符的存储**。未映射盘符的 UNC 网络路径需先在系统中通过 `net use` 或资源管理器映射成盘符后再使用。

**Q3：支持文件系统挂载吗？**

A：支持。本项目已基于 WinFsp + cgofuse 实现盘符挂载（如将存储挂载为 `Z:`），挂载盘在资源管理器「此电脑」中可见。使用前需先安装 [WinFsp](https://winfsp.dev/)，并在「系统设置 → 挂载管理」中选择挂载点；盘符根挂载点经 `\\?\X:` 走 Mount Manager 全局注册，不依赖管理员权限。默认 `build-nofuse` 构建仍跳过挂载能力，需使用附带 cgofuse 的完整构建（如 `output/litepan.exe`）。

**Q4：重新 `npm ci` 后前端构建报 `MISSING_EXPORT` 怎么办？**

A：这是 rtf.js 类型混导导致的问题，需重新应用临时补丁：将 `node_modules` 中 `wmfjs/index.ts`、`emfjs/index.ts`、`rtfjs/index.ts` 的 `export { X, IType }` 改为 `export type { IType }`。

**Q5：如何开机自启？**

A：使用 NSSM 或 WinSW 将 `litepan.exe` 注册为 Windows 服务即可。

**Q6：可以商用吗？**

A：不可以直接商用。上游许可证为 PolyForm Noncommercial 1.0.0，仅限非商业用途。如有商用需求，请先联系上游作者获得商业授权或改用兼容许可。

**Q7：为什么编译产物打不开 / 报不是有效的 Win32 应用？**

A：很可能是全局 GOENV 残留了 `GOOS=linux`，编译出了 Linux ELF。请显式设置 `GOOS=windows GOARCH=amd64` 后重新编译，并用 PE 魔数 `4D 5A` 校验产物。

## 致谢与合规

- 感谢 LitePan 原作者 Ponphil 的开源贡献
- 本项目为移植与修复版本，完整保留上游版权声明与 PolyForm Noncommercial 1.0.0 许可证
- 发布与再分发前，请仔细阅读并遵守 PolyForm Noncommercial 1.0.0 的条款

