---
title: 快速开始
description: 用最少步骤把无限画布跑起来
---

# 快速开始

如果你只是想先把项目跑起来，优先使用 Docker。

## Docker 启动

```bash
git clone git@github.com:tigerowo/infinite-canvas.git
cd infinite-canvas
cp .env.example .env
docker compose up -d
```

启动后访问：

```text
http://localhost:3000
```

默认管理员账号：

```text
用户名：admin
密码：.env 中的 ADMIN_PASSWORD
```

## 本地构建镜像启动

如果你需要基于当前源码本地构建镜像：

```bash
cp .env.example .env
docker compose -f docker-compose.local.yml up -d --build
```

## 首次使用建议

- 管理员先在管理后台配置渠道协议、接口地址和可用模型。
- 个人 API Key 模式下，在右上角配置弹窗选择模型并填写对应渠道的 API Key。
- 如果需要提示词仓库内容，可进入 `/admin/prompts` 拉取或同步。

## 说明

- 当前画布项目和“我的素材”主要保存在浏览器本地，不支持云同步。
- 未登录时，个人 API Key 保存在当前浏览器并由前端请求管理员配置的渠道地址。
- 登录后，模型选择和个人 API Key 保存在账号配置中，由后端代理请求，并可在其他浏览器登录后恢复。
