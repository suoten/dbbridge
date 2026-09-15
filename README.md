<p align="center">
  <img src="https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Vue-3.5+-42b883?logo=vue.js&logoColor=white" alt="Vue">
  <img src="https://img.shields.io/badge/Wails-2.x-f6ad55?logo=go&logoColor=white" alt="Wails">
  <img src="https://img.shields.io/badge/License-AGPLv3-blue.svg" alt="License">
  <img src="https://img.shields.io/badge/平台-Windows%20%7C%20macOS%20%7C%20Linux-success" alt="Platform">
</p>

<p align="center">
  <a href="https://github.com/suoten/dbbridge/stargazers"><img src="https://img.shields.io/github/stars/suoten/dbbridge?style=social" alt="GitHub Stars"></a>
  <a href="https://gitee.com/suoten/dbbridge/stargazers"><img src="https://gitee.com/suoten/dbbridge/badge/star.svg?theme=dark" alt="Gitee Stars"></a>
  <a href="https://github.com/suoten/dbbridge/releases"><img src="https://img.shields.io/github/v/release/suoten/dbbridge" alt="GitHub Release"></a>
</p>

<p align="center">
  <img src="design/logo/wordmark.png" alt="DBBridge" width="520">
</p>

<p align="center"><strong>数据库迁移工具</strong></p>

> **填好连接信息，点击"开始迁移"，5 分钟搞定异构数据库迁移。**
>
> 无需安装 JDK，无需写配置文件，无需命令行操作——**双击运行，所见即所得**。

---

## 📖 这是什么？

DBBridge 是一款**开源、免费、零依赖**的数据库迁移与 SQL 转换工具。

你有没有遇到过这些场景？

| 痛点场景 | 传统方式 | 用 DBBridge |
|----------|----------|-------------|
| 客户给的 MySQL 数据库，你本地用 PostgreSQL | 手工改 SQL，半天搞定 | 填两个连接信息，一键迁移 |
| 拿到一个 `.sql` 备份文件，导入报语法错误 | 逐行排查改语法 | 解析 SQL 文件，自动方言转换 |
| 测试环境要搭和生产库一样的表结构 | 手工建表，容易漏 | 直连源库，一键迁移结构 |
| 信创项目：MySQL → 达梦/金仓/openGauss | 找厂商工具或手工改 | 原生支持国产库迁移 |

**一句话总结：DBBridge 让数据从一个数据库迁移到另一个数据库变得简单。**

---

## ✨ 核心特性

### 🎯 极致易用 — 5 分钟上手
- **图形化界面**：不是命令行工具，是漂亮桌面 App
- **三步迁移**：填连接 → 选表 → 点迁移
- **零配置**：不需要写 YAML/JSON 配置文件
- **实时进度**：迁移进度条 + 详细日志

### 🔌 零依赖 — 单文件即用
- **不依赖 JDK**（不像 DBeaver/Flyway/Liquibase）
- **不依赖 Python**（不像 pgloader）
- **不依赖运行时**（不像 DBSwitch 需要部署服务端 + JVM）
- **单文件 < 30MB**，下载双击即可运行

### 🗄️ 多数据库支持
| 数据库 | 作为源库 | 作为目标库 |
|--------|:--------:|:--------:|
| **MySQL** (5.7/8.0+) | ✅ | ✅ |
| **PostgreSQL** (14/16/17+) | ✅ | ✅ |
| **SQLite** (3.x) | ✅ | ✅ |
| **MariaDB** | ✅ | ✅ |
| **OceanBase** | ✅ | ✅ |
| **TiDB** | ✅ | ✅ |
| **openGauss** | ✅ | ✅ |
| **达梦 DM8** | ✅ | ✅ |
| **人大金仓 KingbaseES** | ✅ | ✅ |
| **CockroachDB** | ✅ | ✅ |
| **SQL Server (MSSQL)** | ✅ | ✅ |

> 以上 11 种数据库可以**任意两两互转**（N × N 组合）。

### 🛡️ 安全可靠
- **迁移前自动备份**：目标库的同名表自动重命名为 `_bak_` 前缀
- **一键回滚**：迁移不满意？一键从备份恢复
- **事务保障**：每批数据写入用事务包裹，失败可回滚
- **参数化查询**：防止 SQL 注入
- **日志脱敏**：密码不出现在任何日志中

### ⚡ 触发器与存储过程迁移
- **触发器自动转换**：MSSQL `inserted/deleted` → MySQL `NEW/OLD`，语法自动适配目标方言
- **存储过程自动转换**：T-SQL → MySQL/PLpgSQL 语法转换，`@变量` → 局部变量声明等
- **CHECK 约束迁移**：表结构中的 CHECK 约束一并迁移到目标库
- **多方言支持**：MSSQL、MySQL、PostgreSQL 之间的触发器和存储过程互转

> ⚠️ 触发器和存储过程的转换是语法层面的自动转换，复杂业务逻辑可能需要人工微调。
> 转换失败的项会在迁移报告中列出，方便定位和手动修复。

### 📊 迁移报告
- 成功/失败表数统计
- 每表行数对比
- 耗时统计
- 失败原因明细
- 迁移历史记录（本地 SQLite 持久化）

---

## 🚀 快速开始

### 方式一：Windows 用户（最简单）

1. 从 [Releases](https://github.com/suoten/dbbridge/releases) 下载 `dbbridge-1.0.0-windows-amd64.zip`
2. 解压后双击 `dbbridge.exe`
3. 填写源库和目标库的连接信息
4. 点击"开始迁移"——完成！

> 🎉 不需要安装 JDK、Python 等运行时依赖。
>
> ⚠️ **Windows 用户注意**：DBBridge 是基于 WebView2 的桌面应用。Windows 10/11 通常已内置 WebView2 运行时，
> 如果双击后闪退或报错，请安装 [WebView2 运行时](https://developer.microsoft.com/microsoft-edge/webview2/) 后重试。

### 方式二：macOS 用户

1. 从 [Releases](https://github.com/suoten/dbbridge/releases) 下载 macOS 版本
2. 解压后拖入"应用程序"文件夹
3. 打开运行
4. 同上，填连接信息 → 迁移

### 方式三：Linux 服务器部署（下载发布包安装）

**最简单的方式**——下载发布包，解压，运行 install.sh：

```bash
# 1. 下载发布包（x86_64 服务器）
wget https://github.com/suoten/dbbridge/releases/latest/download/dbbridge-1.0.0-linux-amd64.zip

# ARM64 服务器（鲲鹏/飞腾/Kylin）:
# wget https://github.com/suoten/dbbridge/releases/latest/download/dbbridge-1.0.0-linux-arm64.zip

# 2. 解压
unzip dbbridge-1.0.0-linux-amd64.zip

# 3. 运行安装脚本
sudo bash install.sh
```

安装完成后：
- 🌐 Web 界面：`http://服务器IP:8989`
- 📂 安装目录：`/opt/dbbridge`
- 📋 日志文件：`/var/log/dbbridge/dbbridge.log`
- 🔄 服务管理：`systemctl start/stop/restart dbbridge`
- 🗑️ 卸载：`sudo bash /opt/dbbridge/uninstall.sh` 或 `sudo bash uninstall.sh`

<details>
<summary>📖 安装脚本支持的参数（点击展开）</summary>

```bash
# 指定端口
DBBRIDGE_PORT=9000 sudo bash install.sh

# 卸载
sudo bash uninstall.sh
```

</details>

<details>
<summary>📖 一行命令安装（curl 管道模式）</summary>

如果服务器已有二进制文件，可以一行命令安装：

```bash
curl -fsSL <your-download-url>/install.sh | sudo bash
```

或通过环境变量指定端口：

```bash
curl -fsSL <your-download-url>/install.sh | DBBRIDGE_PORT=9000 sudo bash
```

</details>

### 方式四：宝塔面板部署 + 反向代理

如果你使用**宝塔面板**管理服务器，DBBridge 提供了一键反向代理配置脚本：

```bash
# 1. 先安装 DBBridge（见方式三）

# 2. 运行宝塔反向代理配置脚本
sudo bash /opt/dbbridge/baota-proxy.sh
```

脚本会引导你完成：
1. 输入绑定域名（如 `db.yourdomain.com`）
2. 确认 DBBridge 端口（默认 8989）
3. 自动写入 Nginx 反向代理配置
4. 自动重载 Nginx

<details>
<summary>📖 手动配置宝塔反向代理（不想用脚本）</summary>

1. **宝塔面板 → 网站 → 添加站点**
   - 域名：`db.yourdomain.com`
   - 类型：纯静态

2. **站点设置 → 配置文件**，在 `server { }` 块中添加：

```nginx
location / {
    proxy_pass http://127.0.0.1:8989;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # WebSocket 支持（迁移进度实时推送）
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";

    # 超时设置（大表迁移耗时较长）
    proxy_connect_timeout 60s;
    proxy_send_timeout 300s;
    proxy_read_timeout 300s;

    proxy_buffering off;
    proxy_cache off;
}
```

3. **保存 → 重启 Nginx**

4. **（可选）申请 SSL 证书**：站点设置 → SSL → Let's Encrypt → 申请

</details>

> 完整的 Nginx 配置模板见 [`deploy/baota-nginx.conf`](deploy/baota-nginx.conf)。

---

## 🖥️ 打包发布（make-release.ps1）

如果你是开发者，需要自行打包发布多平台版本，使用 `make-release.ps1` 一键搞定：

```powershell
# 默认版本号 1.0.0
powershell -ExecutionPolicy Bypass -File make-release.ps1

# 指定版本号
powershell -ExecutionPolicy Bypass -File make-release.ps1 -Version 1.2.0
```

脚本会自动完成：
1. **构建前端**（Vue3 → `frontend/dist/`）
2. **编译多平台二进制**
   - Linux amd64/arm64：`CGO_ENABLED=0` 纯 Go 交叉编译（headless Web 模式，无需 GCC）
   - Windows amd64：`wails build`（完整桌面应用，含 WebView2 前端资源）
3. **打包发布 zip**
   - Linux 包：二进制 + install.sh + uninstall.sh + baota-proxy.sh + baota-nginx.conf + README.md
   - Windows 包：DBBridge.exe + README.md + appicon.png
4. **生成 SHA256 校验文件**

产物在 `release/` 目录下：
```
release/
├── dbbridge-1.0.0-linux-amd64.zip
├── dbbridge-1.0.0-linux-arm64.zip
├── dbbridge-1.0.0-windows-amd64.zip
└── checksums.txt
```

> **为什么 Linux 不需要 CGO？** DBBridge 使用 `modernc.org/sqlite`（纯 Go 实现的 SQLite），
> 不依赖任何 C 库，因此 Linux 版可以 `CGO_ENABLED=0` 交叉编译，无需安装 GCC。
>
> **Windows 版为什么用 wails build？** Wails 桌面应用依赖 WebView2 运行时，
> `wails build` 会生成包含前端资源的完整桌面应用。用户机器需要安装 WebView2 运行时
> （Windows 10/11 通常已内置，若未安装可从[微软官网](https://developer.microsoft.com/microsoft-edge/webview2/)下载）。

---

## 🔧 从源码构建（开发模式）

如果你想从源码编译（开发者或安全审查）：

### 前置要求

| 工具 | 版本 | 说明 |
|------|------|------|
| **Go** | 1.21+ | 后端语言 |
| **Node.js** | 18+ | 前端构建 |
| **Wails CLI** | v2.10+ | 桌面框架（开发模式用） |

> **无需 GCC/CMake** — DBBridge 使用纯 Go SQLite，不需要 CGO 编译。

### 开发模式

```bash
# 1. 克隆仓库
git clone https://github.com/suoten/dbbridge.git
cd dbbridge

# 2. 安装 Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# 3. 开发模式（热重载）
wails dev

# 4. 构建桌面 GUI
wails build
```

### 纯 Go 编译（Linux 服务器部署用）

```bash
# 前端
 cd frontend && npm install && npm run build && cd ..

# Linux amd64
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o dbbridge .

# Linux arm64 (信创/ARM 服务器)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o dbbridge .
```

> ⚠️ Windows 桌面版请使用 `wails build -platform windows`，不要直接 `go build`。
> `go build` 编译的 exe 缺少 Wails 桌面运行时初始化，可能导致无法启动。

<details>
<summary>📖 使用 Wails 构建各平台桌面版</summary>

```bash
# Windows
wails build -platform windows

# macOS (Universal Binary)
wails build -platform darwin/universal

# Linux (amd64)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 wails build -platform linux/amd64

# Linux (arm64 - 信创/ARM 服务器)
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 wails build -platform linux/arm64
```

</details>

---

## 📱 使用指南（图文版）

### 第一步：配置源数据库

打开 DBBridge，在左侧"配置连接"页面：

1. **选择数据库类型**：MySQL / PostgreSQL / SQLite / ...
2. **填写连接信息**：
   - 主机地址（如 `127.0.0.1`）
   - 端口（如 MySQL 默认 `3306`）
   - 用户名 / 密码
   - 数据库名
3. **点击"测试连接"**：确保能连上

### 第二步：选择要迁移的表

1. 连接成功后，自动加载表列表
2. **勾选**要迁移的表（默认全选）
3. 可用搜索框快速过滤

### 第三步：配置目标数据库

1. 同样选择数据库类型 + 填写连接信息
2. **测试连接**

### 第四步：配置迁移选项

| 选项 | 说明 | 建议 |
|------|------|------|
| 批量大小 | 每批写入的行数 | 5000（默认） |
| 并发表数 | 同时迁移几张表 | 4（默认） |
| 仅迁移结构 | 不迁移数据 | 适合搭测试环境 |
| 仅迁移数据 | 不迁移表结构 | 目标表已存在时 |
| 目标表已存在时删除 | 先 DROP 再建 | ✅ 推荐 |
| 迁移前备份 | 自动备份目标表 | ✅ 推荐 |
| 自动回滚 | 失败时自动恢复 | ✅ 推荐 |

### 第五步：点击"开始迁移"

- 实时进度条
- 详细日志
- 迁移完成后查看报告
- 如果不满意 → "备份管理"页面一键回滚

---

## 📂 项目结构

```
DBBridge/
├── app.go                      # Wails 绑定层（前端调用入口）
├── main.go                     # 程序入口
├── go.mod                      # Go 模块定义
├── wails.json                  # Wails 配置
├── make-release.ps1            # 一键打包发布脚本（多平台二进制 + zip）
├── install.sh                  # 源码编译安装脚本（自动安装 Go/Node/Wails 并编译）
├── deploy/                     # 部署相关配置（打包到发布 zip 中）
│   ├── install.sh              # 发布包安装脚本（从同目录复制二进制）
│   ├── uninstall.sh            # 卸载脚本
│   ├── baota-nginx.conf        # 宝塔 Nginx 反向代理模板
│   ├── baota-proxy.sh          # 宝塔反向代理自动配置脚本
│   └── dbbridge.service        # systemd 服务模板
├── internal/                   # 后端核心逻辑
│   ├── adapter/                # 数据库适配器（每种数据库一个目录）
│   │   ├── mysql/
│   │   ├── postgres/
│   │   ├── sqlite/
│   │   ├── mariadb/
│   │   ├── oceanbase/
│   │   ├── tidb/
│   │   ├── opengauss/
│   │   ├── dameng/
│   │   ├── kingbase/
│   │   ├── cockroachdb/
│   │   ├── mysqlcompat/        # MySQL 兼容协议库公共逻辑
│   │   └── pgcompat/           # PG 兼容协议库公共逻辑
│   ├── converter/              # DDL/数据转换器
│   ├── orchestrator/           # 迁移编排器（调度核心）
│   ├── migrator/               # 迁移执行器
│   ├── parser/                 # SQL 文件解析器
│   ├── history/                # 迁移历史记录
│   ├── service/                # 业务服务层
│   ├── typeconv/               # 类型映射
│   ├── logger/                 # 日志
│   ├── progress/               # 进度追踪
│   └── config/                 # 配置管理
├── pkg/                        # 公开包（适配器接口 + 类型定义）
│   ├── adapter.go
│   └── types.go
├── frontend/                   # Vue3 前端
│   ├── src/
│   │   ├── App.vue            # 主界面
│   │   └── components/         # 组件
│   │       ├── ConnectionForm.vue
│   │       ├── ProgressPanel.vue
│   │       ├── MigrationReport.vue
│   │       ├── BackupManager.vue
│   │       └── MigrationHistory.vue
│   ├── package.json
│   └── vite.config.ts
└── build/                      # 构建产物
    └── windows/
        ├── icon.ico
        └── installer/          # NSIS 安装包
```

---

## 🔧 服务管理命令

部署在 Linux 服务器后，使用以下命令管理 DBBridge：

```bash
# 启动
sudo systemctl start dbbridge

# 停止
sudo systemctl stop dbbridge

# 重启
sudo systemctl restart dbbridge

# 查看运行状态
sudo systemctl status dbbridge

# 查看实时日志
sudo journalctl -u dbbridge -f

# 或查看日志文件
tail -f /var/log/dbbridge/dbbridge.log

# 开机自启
sudo systemctl enable dbbridge

# 关闭开机自启
sudo systemctl disable dbbridge
```

---

## ❓ 常见问题

<details>
<summary><b>Q: Windows 上运行提示"缺少 DLL"、闪退或"不是有效的 Win32 程序"？</b></summary>

DBBridge 是基于 WebView2 的桌面应用，需要 **WebView2 运行时**：

1. **Windows 10/11 通常已内置** WebView2，大多数用户无需额外安装
2. 如果双击后闪退，请下载安装 [WebView2 运行时](https://developer.microsoft.com/microsoft-edge/webview2/)
3. 确保下载的是 Windows 版本的 `DBBridge.exe`（非 Linux 版）
4. 如果仍报错，可能系统缺少 Visual C++ 运行库，请安装 [VC++ 运行库](https://aka.ms/vs/17/release/vc_redist.x64.exe)
</details>

<details>
<summary><b>Q: macOS 上提示"无法验证开发者"或"已损坏"？</b></summary>

打开"系统设置 → 隐私与安全性"，在"安全性"部分点击"仍要打开"。
或在终端执行：
```bash
xattr -cr /Applications/DBBridge.app
```
</details>

<details>
<summary><b>Q: Linux 安装脚本报错"未找到二进制文件"？</b></summary>

请确保下载的是完整发布包（zip），解压后 `install.sh` 和 `dbbridge` 二进制在同一目录。

```bash
# 正确流程
wget <download-url>/dbbridge-1.0.0-linux-amd64.zip
unzip dbbridge-1.0.0-linux-amd64.zip
cd dbbridge-1.0.0-linux-amd64
sudo bash install.sh
```

如果你是从源码编译安装，请使用项目根目录的 `install.sh`（它会自动安装 Go/Node 并编译）。
</details>

<details>
<summary><b>Q: 迁移大表很慢怎么办？</b></summary>

调整迁移选项：
1. 增大批量大小（BatchSize）：如 10000-50000
2. 增加并发表数（Concurrency）：如 8
3. 关闭"迁移前备份"（如果确认不需要回滚）
4. 确保源库和目标库网络通畅
</details>

<details>
<summary><b>Q: 宝塔反代后访问显示 502 Bad Gateway？</b></summary>

1. 检查 DBBridge 服务是否运行：`systemctl status dbbridge`
2. 检查端口是否一致：Nginx 配置中的 `proxy_pass` 端口要和 DBBridge 运行端口一致
3. 检查防火墙：`ufw allow 8989/tcp` 或 `firewall-cmd --add-port=8989/tcp --permanent`
4. 查看日志：`tail -f /var/log/dbbridge/dbbridge.log`
</details>

<details>
<summary><b>Q: 连接国产数据库（达梦/金仓）失败？</b></summary>

国产数据库需要额外配置：
- **达梦 DM8**：确保 `dm.ini` 中 `PORT = 5236`，用户有查询权限
- **人大金仓**：默认端口 `54321`，确保监听非 `localhost`
- 如果持续失败，请在 Issue 中附带数据库版本和错误信息反馈
</details>

<details>
<summary><b>Q: 迁移中断了，数据会丢吗？</b></summary>

不会。如果开启了"迁移前备份"：
1. 目标库的同名表已自动备份为 `_bak_` 前缀
2. 已成功写入的表数据保留在目标库
3. 失败的表可通过"备份管理"页面恢复
4. 重新迁移时，已存在的表可勾选"删除已存在"选项重新写入
</details>

---

## 🆚 与其他工具对比

| 对比维度 | **DBBridge** | DBSwitch | Navicat | DBeaver | DataX |
|----------|:----------:|:--------:|:-------:|:-------:|:-----:|
| **单文件零依赖** | ✅ | ❌ 需 JVM | ❌ 安装包大 | ❌ 需 JDK | ❌ 需 JDK |
| **图形界面** | ✅ 桌面 | ✅ Web | ✅ 桌面 | ✅ 桌面 | ❌ |
| **跨数据库迁移** | ✅ 10 种 | ✅ 20+ | 有限支持 | ❌ 不支持 | 有限支持 |
| **SQL 文件输入** | ✅ 核心特性 | ❌ | ❌ | 仅执行 | ❌ |
| **国产数据库** | ✅ 重点 | ✅ | 有限 | 有限 | 有限 |
| **备份与回滚** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **学习成本** | ⭐ 极低 | 中 | 中 | 中 | 高 |
| **开源免费** | ✅ AGPLv3 | ✅ GPLv3 | ❌ | ✅ | ✅ |

---

## 🤝 参与贡献

欢迎提交 Issue 和 PR！

- **Bug 反馈**：[提交 Issue](https://github.com/suoten/dbbridge/issues/new?labels=bug)
- **功能建议**：[提交 Issue](https://github.com/suoten/dbbridge/issues/new?labels=enhancement)
- **代码贡献**：Fork → 开发分支 → 提交 PR
- **适配器开发**：参考 `internal/adapter/mysql/adapter.go` 实现新的数据库适配器

### 开发环境搭建

```bash
# 克隆代码
git clone https://github.com/suoten/dbbridge.git
cd dbbridge

# 安装 Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@latest

# 开发模式（热重载）
wails dev

# 构建桌面发布版
wails build

# 打包多平台发布包（Windows PowerShell）
powershell -ExecutionPolicy Bypass -File make-release.ps1 -Version 0.1.0
```

---

## 📄 开源协议

本项目采用 [AGPLv3](LICENSE) 协议开源。

- ✅ 个人使用、学习、研究：**免费**
- ✅ 企业内部使用：**免费**
- ✅ 社区贡献：**欢迎**
- ⚠️ 商业分发/SaaS 服务：需联系商业授权

---

## 📞 联系我们

- **GitHub**: [https://github.com/suoten/dbbridge](https://github.com/suoten/dbbridge)
- **Gitee**: [https://gitee.com/suoten/dbbridge](https://gitee.com/suoten/dbbridge)
- **Issue**: [https://github.com/suoten/dbbridge/issues](https://github.com/suoten/dbbridge/issues)
- **QQ 群**: [1098728913](https://qm.qq.com/q/1098728913)（加群交流、问题反馈、功能建议）

---

## 🙏 致谢

- [Wails](https://wails.io) — 使用 Go + Web 技术构建桌面应用
- [Vue.js](https://vuejs.org) — 渐进式 JavaScript 框架
- [Lucide Icons](https://lucide.dev) — 美观开源的图标库
- [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql) — MySQL 驱动
- [lib/pq](https://github.com/lib/pq) — PostgreSQL 驱动
- [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) — 纯 Go 实现的 SQLite

---

<div align="center">

### 数据迁移，一键搞定

**让数据库之间的数据流通变得简单**

⭐ 如果 DBBridge 帮到了你，给个 Star 支持一下！

</div>
