<p align="center">
  <img src="web/public/logo.svg" width="96" alt="infinite-canvas logo">
</p>

<h1 align="center">无限画布 (infinite-canvas)</h1>

无限画布是一款面向 AI 视觉创作的开源工具，集成画布编排、图片生成与编辑、视频与音频生成、对话助手、提示词库和素材管理。

本仓库基于原作者 [basketikun](https://github.com/basketikun) 的 [infinite-canvas](https://github.com/basketikun/infinite-canvas) 项目进行二次开发，保留原项目版权声明，并遵循 [MIT License](LICENSE)。

项目处于开发阶段，本地存储格式可能调整，不保证历史数据兼容。

## 核心功能

- **无限画布**：多画布项目、节点拖拽与缩放、连线、分组、小地图、撤销重做及导入导出。
- **AI 创作**：接入 OpenAI 兼容接口，支持文生图、图生图、参考图编辑、文本问答、视频与音频生成，具体能力取决于配置的服务和模型。
- **画布助手**：结合选中节点和上游内容进行对话，将生成结果插入画布。
- **本地 Agent**：通过 Canvas Agent 连接 Codex 和 Claude Code，使用 MCP 操作画布；提供 Codex app 插件入口。
- **插件与接口扩展**：通过 URL 安装和管理节点插件，使用 TypeScript SDK 开发插件，自定义图片与视频接口调用脚本。
- **提示词与素材管理**：使用开源提示词来源或自定义 JSON 来源，保存和复用素材及生成记录。

## 快速开始

### 本地开发

准备 Git 和 [Bun](https://bun.sh/)，执行：

```bash
git clone https://github.com/lengxiaoyu6/infinite-canvas.git
cd infinite-canvas/web
bun install
bun run dev
```

默认访问地址为 [http://localhost:3000](http://localhost:3000)。

### Docker 运行

安装 Docker 后，从当前仓库源码构建镜像并启动：

```bash
git clone https://github.com/lengxiaoyu6/infinite-canvas.git
cd infinite-canvas
docker build -t infinite-canvas .
docker run --rm -p 3000:3000 infinite-canvas
```

默认访问地址为 [http://localhost:3000](http://localhost:3000)。

### 首次配置

进入右上角配置，填写服务提供商的 `Base URL`、`API Key` 和模型信息。如果接口格式存在差异，可配置图片或视频调用脚本。

画布、素材、生成记录和 API 配置默认保存在浏览器本地。AI 请求默认由浏览器向配置的服务发送，API Key 也保存在浏览器本地。

## 文档入口

| 文档 | 内容 |
| --- | --- |
| [快速开始](docs/content/docs/overview/quick-start.zh-CN.mdx) | 启动与基础配置 |
| [功能介绍](docs/content/docs/overview/features.zh-CN.mdx) | 详细功能说明 |
| [画布节点操作手册](docs/content/docs/canvas/canvas-node-manual.zh-CN.mdx) | 节点使用与画布操作 |
| [画布快捷键](docs/content/docs/canvas/canvas-shortcuts.zh-CN.mdx) | 常用快捷键 |
| [本地 Canvas Agent](canvas-agent/README.md) | 本地 Agent 安装与连接 |
| [Codex app 插件](plugins/infinite-canvas/README.md) | 插件安装与使用 |
| [画布插件开发](plugins/canvas/README.md) | 节点插件与 SDK |
| [本地代理](canvas-proxy/README.md) | 浏览器跨域请求转发 |
| [待办事项](docs/content/docs/progress/todo.zh-CN.mdx) | 后续计划 |
| [待测试事项](docs/content/docs/progress/pending-test.zh-CN.mdx) | 当前版本的验收内容 |
| [更新记录](CHANGELOG.md) | 版本变更 |
| [漏洞提交](SECURITY.md) | 安全问题反馈方式 |
