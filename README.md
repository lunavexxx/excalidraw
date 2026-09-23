# Excalidraw 协作平台

基于 [Excalidraw](https://github.com/excalidraw/excalidraw) fork 改造的**多用户协作白板平台**(独立产品):账号体系、工作区、多人实时协作、权限管理、在线保存、评论、导出增强、在线图形库、后台管理。

> 改造总计划见 [plans/collab-platform.md](./plans/collab-platform.md)。

## 仓库结构

本仓库是一个「多项目聚合」的 git 大目录,**根目录不是 yarn/Go 项目**,每个子目录自包含:

```
excalidraw/
├── apps/
│   ├── web/      # 前端:yarn workspace monorepo(原 excalidraw 根项目 + excalidraw-app 合并)
│   │             #   packages/ — @excalidraw/* 核心包
│   │             #   src/      — 应用代码
│   ├── api/      # 后端:Go(Gin + pgx + golang-migrate)
│   └── dev-docs/ # 独立的 Docusaurus 文档站(上游编辑器文档)
└── plans/        # 改造计划文档
```

## 快速开始

### web(前端)

```bash
cd apps/web
yarn install
yarn start          # http://localhost:3000
```

常用命令(`apps/web` 内执行):

```bash
yarn test:typecheck # TypeScript 类型检查
yarn test:app       # 运行测试
yarn test:update    # 运行测试(更新快照)
yarn fix            # 自动修复格式与 lint
yarn build          # 生产构建 → apps/web/build/
```

### api(后端)

```bash
cd apps/api
go run ./cmd/api    # http://localhost:8080/healthz
```

详见 [apps/api/README.md](./apps/api/README.md)。

## 上游同步策略

产品定位为独立产品,**基本不同步上游** [excalidraw/excalidraw](https://github.com/excalidraw/excalidraw);安全修复等按需 cherry-pick。重组(2026-09)前的最后一次上游合并对应上游 `#12143`。

## License

MIT(继承自上游 Excalidraw)。
