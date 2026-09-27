<div align="center">

# LitePan Windows 版

**开源多网盘聚合挂载/管理工具 [LitePan](https://github.com/Ponphil/LitePan) 的 Windows 原生移植版**

无需 Docker、无需 WSL、无需虚拟机，单个 exe 即可运行。

</div>

---

## 项目来源与致谢

本项目由社区移植，**不是官方版本**，原作者保留全部权利。

- **原作者**：[@Ponphil](https://github.com/Ponphil) · [B 站主页](https://space.bilibili.com/1501989416)
- **原项目**：[Ponphil/LitePan](https://github.com/Ponphil/LitePan)（Go 语言编写，多网盘聚合挂载/管理工具，官方仅提供 Docker 部署）
- **移植内容**：将 LitePan 编译为 Windows 原生可执行程序，并修复 Windows 平台下的路径与存储访问问题（跨盘浏览等）
- **致谢**：感谢 [@Ponphil](https://github.com/Ponphil) 开发出如此优秀的多网盘聚合工具，本项目的一切功能与设计均源自上游

> [!IMPORTANT]
> 请前往上游仓库 [Ponphil/LitePan](https://github.com/Ponphil/LitePan) 为原作者点个 Star，支持原创。

---

## 特性

- **Windows 原生运行**：不依赖 Docker、WSL 或虚拟机，下载即用
- **单文件分发**：Go 后端 + Vue 3 前端构建产物通过 `go:embed` 内嵌为单一 exe
- **多网盘聚合**：多账号统一管理，一个界面看完（秒传、STRM 直连播放、刮削、整理、离线下载等）
- **跨盘浏览**：修复原版 Linux 路径语义限制，支持枚举并浏览本地盘 / 可移动盘 / 光驱 / 网络映射盘 / 虚拟盘
- **完整 Web / 存储 / API 功能**：在无 FUSE 内核的 Windows 上，除文件系统挂载外功能完整可用
- **默认账号**：`admin/admin`，首次登录后请立即修改密码

> Windows 没有 FUSE 内核支持，本版不含 FUSE 盘符挂载功能；其余功能与上游一致。

---

## 快速开始

### 方式一：直接运行 exe（推荐）

仓库 `output/` 目录已附带编译好的 `litepan.exe`，下载后直接运行：

```powershell
.\litepan.exe --listen=127.0.0.1:5211 --data-dir=.\data
```

打开 <http://127.0.0.1:5211>，默认账号 `admin/admin`。

### 方式二：从源码编译

详细编译步骤见 [LitePan-Windows-README.md](./LitePan-Windows-README.md)，要点：

1. 环境要求：Go 1.26.6+、Node 20+（构建前端）、Windows 10/11
2. 构建前端：`cd web && npm ci && npm run build`（前端产物写入 `internal/api/web`，由 `go:embed` 内嵌）
3. 编译：`GOOS=windows GOARCH=amd64 go build ./cmd/litepan`（走 `build-nofuse` 路径，不带 fuse tag）

---

## 与原版的差异

| 项目 | 上游 LitePan | 本 Windows 版 |
| --- | --- | --- |
| 运行环境 | Docker / Linux | Windows 原生 exe |
| 部署方式 | Docker Compose | 单文件运行 |
| 文件系统挂载 | FUSE（Linux） | 不支持（无 FUSE 内核） |
| 本地目录浏览 | 仅 Linux 路径语义 | 支持全盘符枚举与跨盘浏览 |
| 前端形态 | Docker 内嵌 Web | go:embed 单 exe |

---

## 反馈与交流

- 本版相关问题请在 Issues 提出
- 上游功能建议请前往 [Ponphil/LitePan](https://github.com/Ponphil/LitePan) 或 [B 站主页](https://space.bilibili.com/1501989416)

---

## 许可

[PolyForm Noncommercial 1.0.0](./LICENSE) — 个人学习与非商业使用，**禁止商用**。

本项目继承上游许可，任何商业使用（包括内部商业部署、商业 SaaS、销售、转授权等）均需另行获得原作者授权。请遵守各网盘服务条款与当地法规。第三方依赖见 [THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)。

[![license][license-shield]][license-url]

[license-shield]: https://img.shields.io/badge/License-PolyForm%20NC-red?style=flat-square
[license-url]: ./LICENSE
