# Excalidraw 协作平台改造计划

> 日期:2026-09-23
> 目标:将当前单机版 Excalidraw 改造为多用户协作平台 —— 账号体系、工作区、多人实时协作、权限管理、在线保存、评论、导出增强、在线图形库、后台管理。

---

## 一、现状分析(基于当前仓库)

| 模块 | 现状 | 改造接入点 |
|---|---|---|
| 数据存储 | 浏览器 localStorage([LocalData.ts](../excalidraw-app/data/LocalData.ts))+ Firebase 云存储([firebase.ts](../excalidraw-app/data/firebase.ts)) | `excalidraw-app/data/` 已是适配器结构,新增 ServerData 适配器替换 Firebase 即可 |
| 实时协作 | 前端已完整实现([collab/](../excalidraw-app/collab/)),socket.io 连接 `VITE_APP_WS_SERVER_URL`;但 WebSocket 服务端是上游独立项目 excalidraw-room,**本仓库没有**,且无鉴权 | 自托管 room 服务并入 monorepo,加鉴权中间件 |
| 权限 | 无任何账号/权限概念,协作者仅靠房间号(随机字符串) | 全部新建 |
| 评论 | 无 | 新建(依赖后端 + 前端 UI) |
| 导出 | PNG/SVG 已有;无 PPT | 新增 PPT 导出(pptxgenjs) |
| 图形库 | localStorage 本地图形库,云端库指向 excalidraw.com 官方服务 | 新建自建 library API |
| 后台管理 | 无 | 新建 admin 应用 |

**关键结论:前端协作能力大部分已存在,工作量主要在后端(约 60-70%)+ 前端改造(30-40%)。**

---

## 二、目标架构

```
                    ┌─────────────────────────────────────┐
                    │              单台服务器               │
                    │                                     │
  浏览器 ─── HTTPS ─┤  Caddy (自动 HTTPS / 反向代理)        │
                    │   ├── apps/web      静态资源 + SSR?  │
                    │   ├── apps/api      REST API (Fastify)
                    │   ├── apps/room     WebSocket 协作服务
                    │   ├── apps/admin    管理后台静态资源    │
                    │   ├── PostgreSQL    元数据            │
                    │   └── MinIO         对象存储(图片/导出) │
                    └─────────────────────────────────────┘
```

- **所有流量经 Caddy 443 进来**,按路径/域名分发
- **实时协作走 `/ws`** WebSocket,room 服务启动时用内部接口校验 JWT + 文档权限
- **图片数据**(画板内嵌图片)存 MinIO,元数据存 PG

### 技术选型

| 项 | 选型 | 理由 |
|---|---|---|
| API 框架 | **Go**(Gin/Echo)+ sqlc 或 GORM | 用户熟悉 Go;单二进制部署、内存占用小(~50MB),与 PG/MinIO/Caddy 在单服务器共存最轻松 |
| 数据库访问 | sqlc(编译期生成类型安全代码)或 GORM | sqlc 更可控;GORM 上手快,二选一即可 |
| 认证 | 自实现 JWT(access 15min + refresh 30d)+ argon2/bcrypt 密码哈希;后续可加 OAuth(Google/GitHub/微信) | 可控、无外部依赖;OAuth 各家接入点留好 |
| 配置中心 | **Nacos**(standalone + 鉴权,nacos/nacos-server v2 镜像) | 配置值不进 git;api 以 `--nacos-addr` 定位,全部应用配置(端口、DATABASE_URL)运行时拉取;本地/服务器用 namespace(`local`/`server`)隔离;.env 只存基础设施自举密钥 |
| 实时协作 | 自托管 excalidraw-room(MIT,保留 Node)+ 鉴权中间件;API 调用 Go 服务校验 ACL | 前端协议已按 socket.io 实现,自研房间协议成本极高;room 仅做协议转发,业务鉴权全部走 Go API |
| 对象存储 | MinIO(S3 兼容 API) | 单机可跑,未来可平移到云 S3 |
| 邮件 | SMTP(初期用阿里云邮件推送/Resend) | 邀请、验证、找回密码必需 |
| 后台管理 | React + Vite 独立应用(可基于 react-admin) | 与主应用技术栈一致 |
| 部署 | Docker Compose + Caddy | 单服务器最优解,一条命令起全栈 |

---

## 三、仓库重组(第 1 周完成)

**定位:git 仓库只是"装代码的大目录",每个 `apps/*` 都是自包含的独立项目**,各自持有自己的 package.json / go.mod / lockfile,自安装、自构建、自运行。开发时 cd 进对应子项目操作;根目录不注册 workspace。

```
excalidraw/                ← git 大目录,非 yarn 项目
├── apps/
│   ├── web/               ← 自包含 yarn 项目(原根项目 + excalidraw-app 合并而成)
│   │   ├── package.json   ← 原根 package.json 迁移而来,workspaces: ["packages/*"]
│   │   ├── packages/      ← 原 packages/ 原封移入(excalidraw/element/math/utils/common)
│   │   ├── src/           ← 原 excalidraw-app/
│   │   └── vite.config.mts / vitest.config.mts / tsconfig.json / .env.*
│   ├── api/               ← 自包含 Go module(go.mod)
│   ├── room/              ← 自包含 npm 项目(自托管 excalidraw-room)
│   └── admin/             ← 自包含 yarn 项目(管理后台)
├── deploy/                ← Docker Compose、Caddyfile、备份脚本
└── plans/                 ← 计划文档
```

要点:
- `apps/web` 内部 import(`@excalidraw/excalidraw` 等)**完全不变**
- 原 excalidraw-app 的内容(src、vite 配置、.env)并入 `apps/web` 根,原根 package.json 的脚本(test:typecheck / fix / test:app)变成 web 自己的脚本
- `examples/` 移入 `apps/web/examples/`(它依赖 packages,生命周期跟随 web)

**决策:产品定位为独立产品,基本不同步上游。** 上游更新今后按需 cherry-pick(安全修复等),不定期 merge。

**重组前先做最后一次上游合并**(当前 HEAD 已含 2026-09 的最新提交,基本无欠账),之后再移动目录,这样 cherry-pick 未来补丁时仍能对应上旧路径前的提交历史。

重组需同步修改:`.vscode/`(launch/tasks 的路径)、CI workflow(工作目录改为 apps/web)、vitest/tsconfig 相对路径(原从根引用 packages,移入 web 后路径变浅)、eslint 根配置的归属。

---

## 四、数据库 Schema 设计(初稿)

```sql
-- 账号
users            (id, email, password_hash, name, avatar_url, email_verified, created_at)
refresh_tokens   (id, user_id, token_hash, expires_at, revoked)

-- 工作区
workspaces            (id, name, owner_id, plan, created_at)
workspace_members     (workspace_id, user_id, role)          -- role: owner/admin/editor/viewer
workspace_invitations (id, workspace_id, email, role, token, expires_at)

-- 文档
documents               (id, workspace_id, name, owner_id, latest_version, created_at, updated_at, deleted_at)
document_collaborators  (document_id, user_id, role)          -- role: editor/viewer
share_links             (id, document_id, token, role, expires_at, created_by)  -- 匿名分享链接
document_versions       (id, document_id, version, data jsonb, created_by, created_at)  -- 场景快照
document_files          (id, document_id, file_key, size)     -- MinIO 中的内嵌图片

-- 评论
comments         (id, document_id, thread_id, author_id, element_id, x, y, body, resolved, created_at)
comment_mentions (comment_id, user_id)

-- 图形库
libraries      (id, name, visibility, owner_workspace_id, owner_user_id)   -- visibility: private/workspace/public
library_items  (id, library_id, data jsonb, name, tags, downloads, created_at)

-- 后台
audit_logs       (id, actor_id, action, target_type, target_id, meta jsonb, created_at)
storage_quotas   (workspace_id, bytes_used, bytes_limit)
```

---

## 五、分阶段计划与 Timeline

> 假设:**1 名全栈开发,全职投入**。兼职投入请按 1.5-2 倍估算。
> 每阶段结束部署到服务器实测,主分支保持可运行。

### M0 — 基础设施与仓库重组(第 1 周)🔴 P0

**目标:monorepo 新结构就位,服务器可一键起全栈空壳。**

- [ ] `git mv excalidraw-app apps/web`、`git mv packages apps/web/packages`、根 package.json/vitest/tsconfig/eslint 并入 `apps/web`,修复相对路径与 CI 工作目录
- [ ] 服务器初始化:删除 conduit 容器(compose down + 清镜像),升级 Docker/Compose v2,加 2G swap(完整步骤见 deploy/README.md「服务器一次性初始化」)
- [ ] `apps/api` 脚手架:Go(Gin/Echo)+ PG 连接 + 迁移工具(golang-migrate)+ 健康检查接口
- [ ] `apps/room` 脚手架:fork excalidraw-room 源码并入 monorepo
- [x] `deploy/`:Docker Compose 最小栈已落地 —— `test.yml`(服务器=正式环境:pg + nacos + api + web + caddy)/ `local.yml`(本地=测试环境:api + web,连服务器 nacos/pg)+ Caddyfile + init-nacos.sh;MinIO/room 随 M1/M3 补,备份脚本 M1 有真实数据后加
- [ ] 服务器安装:确认配置(建议 ≥ 4C8G/100G 盘)、防火墙、域名 + HTTPS

### M1 — 账号体系 + 在线保存(第 2-4 周)🔴 P0

**目标:可注册登录,画板保存/打开走自己的服务器,Firebase 彻底移除。**

- [ ] Schema(PSQL 迁移脚本):users / refresh_tokens / documents / document_versions
- [ ] Auth API:注册、登录、邮箱验证、找回密码、refresh token 轮换、登出
- [ ] 文档 API:创建、保存(乐观锁版本号)、加载、列表、软删除、重命名
- [ ] 内嵌图片上传:POST 直传 MinIO(预签名 URL),替换现有 S3/firebase 图片逻辑
- [ ] 前端:`data/` 层新增 ServerData 适配器;登录/注册页;用户菜单
- [ ] 前端:文件列表页(最近打开/我的文档),替代"打开"对话框的数据源
- [ ] 本地自动保存策略:localStorage 打草稿 + 服务端持久化,离线可继续画
- [ ] e2e:注册→建文件→画→保存→另开浏览器打开

### M2 — 工作区 + 权限 + 分享(第 5-7 周)🔴 P0

**目标:多租户模型落地,权限贯穿所有 API。**

- [ ] Schema:workspaces / workspace_members / workspace_invitations / document_collaborators / share_links
- [ ] API:工作区 CRUD、成员邀请(邮件)、角色变更、移除成员
- [ ] API:文档 ACL 中间件(owner/editor/viewer 四级判定),**所有 M1 接口补上工作区归属**
- [ ] API:分享链接(只读/可编辑、可过期、可撤销),匿名访问文档 API
- [ ] 前端:工作区切换器、成员管理页、邀请弹窗
- [ ] 前端:文档内"分享"按钮(生成链接、设置权限、协作者列表)
- [ ] 前端:个人工作区 ↔ 团队工作区文件隔离视图
- [ ] e2e:A 创建文档 → 邀请 B 编辑 → C 用只读链接访问 → 权限外操作被拒

### M3 — 多人实时协作(第 8-10 周)🔴 P0

**目标:自托管 room 上线,多人可实时同编一份文档,且过权限校验。**

- [ ] room 服务(Node):连接时校验 JWT,进房时调用 Go API 内部接口校验文档 ACL
- [ ] room 服务:断线重连、房间回收(无人自动清内存)
- [ ] 前端 Portal.tsx:连接带 token,协作按钮对无权限者隐藏
- [ ] 协作 UI 验收:头像 presence、跟随模式、只读协作者不可编辑(服务端强制,不只是前端)
- [ ] 压测:10 人同房间、20 并发房间的内存与 CPU 基线
- [ ] e2e:两个浏览器同编一个文档,元素同步、撤销、光标 presence 正常

### M4 — 评论功能(第 11-12 周)🟡 P1

**目标:图钉式评论,协作者可讨论、@提及、解决。**

- [ ] Schema:comments / comment_mentions
- [ ] API:创建线程、回复、解决/重开、@提及;通知(in-app 未读数,先不做邮件)
- [ ] 实时:评论事件走 room 的独立 channel(避免轮询)
- [ ] 前端:画布图钉 UI、侧栏线程面板、未读红点
- [ ] e2e:A 打图钉 → B 实时收到并回复 → A 解决 → 双方一致

### M5 — 导出增强(第 13 周)🟡 P1

**目标:补齐图片之外的导出能力。**

- [ ] PPT 导出:pptxgenjs,**按 frame 分页**(无 frame 则整页一张);服务端生成以省前端体积
- [ ] PDF 导出:多页打包(基于已有 SVG 导出链路)
- [ ] 导出菜单整合:PNG/SVG/PPT/PDF 四选项,高清/缩放设置复用现有 UI
- [ ] 服务端导出接口有配额限制(防止刷 CPU)

### M6 — 在线图形库(第 14-15 周)🟡 P1

**目标:图形库云同步 + 公共库市场。**

- [ ] Schema:libraries / library_items
- [ ] API:个人库/工作区库的 CRUD(兼容 .excalidrawlib 导入导出格式)、公共库发布、浏览/搜索/安装计数
- [ ] 前端:图形库面板接入自建 API(替换官方 library 后端地址)、"发布到公共库"入口
- [ ] 预置:内置 2-3 个官方开源图形库作为初始公共库内容

### M7 — 后台管理(第 16 周)🟢 P2

**目标:运营侧可见可控。**

- [ ] Schema:audit_logs / storage_quotas
- [ ] admin 应用:登录(仅 owner 角色可见入口)、用户管理(禁用/重置)、工作区管理、用量统计(存储/DAU/文档数)
- [ ] 审计日志:关键操作(删除文档、权限变更)落库可查
- [ ] API 全局限流(rate limit)与统计埋点

---

## 六、时间线总览

| 周次 | 里程碑 | 优先级 | 关键交付物 |
|---|---|---|---|
| W1 | M0 基础设施 | 🔴 P0 | monorepo 新结构,Docker Compose 全栈空壳 |
| W2-4 | M1 账号+保存 | 🔴 P0 | 可登录、在线保存/打开、Firebase 移除 |
| W5-7 | M2 工作区+权限 | 🔴 P0 | 多租户、分享链接、成员管理 |
| W8-10 | M3 实时协作 | 🔴 P0 | 自托管 room + 鉴权,多人同编 |
| W11-12 | M4 评论 | 🟡 P1 | 图钉评论、@提及、通知 |
| W13 | M5 导出 | 🟡 P1 | PPT/PDF 导出 |
| W14-15 | M6 图形库 | 🟡 P1 | 云同步图形库 + 公共市场 |
| W16 | M7 后台 | 🟢 P2 | 管理后台、审计、限流 |

**P0(账号/保存/工作区/权限/协作)= 10 周,是产品可用的最小闭环,建议严格按序执行。**
P1 三项(评论/导出/图形库)彼此独立,M4-M6 顺序可根据实际需要调整甚至并行。
M7 最后做,如果发布压力大可以推迟。

---

## 七、资源清单

**已有:**
- 1 台服务器(root;**IP、域名等部署敏感值统一存 deploy/.env,不进 git**):Debian 12,4C / 8G(无 swap)/ 40G 盘(**仅剩 20G,偏紧**);Docker 20.10 + docker-compose v1.29.2
  - 原有的 conduit 三件套已确认废弃,**M0 时删除**,释放 80/8080 端口给 Caddy
  - 机器上另有一套 K8s(apiserver 6443 / etcd)保留不动;我们的服务用独立 docker-compose 跑,与其隔离
  - 本机未发现 PostgreSQL(无容器/无 psql)——**M0 待确认**:其位置(K8s 内?另一台?),找不到就直接在 compose 里起专属 PG 容器
- Git 仓库(单仓 monorepo)

**需要新增/确认:**
| 资源 | 用途 | 说明 |
|---|---|---|
| 磁盘扩容确认 | 服务器仅剩 20G | M0 前决定:扩容云盘,或靠镜像清理 + 日志轮转硬撑 |
| 域名 + DNS | HTTPS | ✅ 已就绪并备案(值存 deploy/.env,不入库);裸域/www 已指向服务器,只需补 `api` 一条 A 记录(子域名全表见 deploy/README.md) |
| SMTP 邮件账号 | 注册验证/邀请/找回密码 | 阿里云邮件推送或 Resend,免费额度够初期用 |
| 服务器磁盘余量 | MinIO 图片存储 | 按预估 DAU 估算,初期 50G 足够 |
| 备份方案 | 数据安全 | pg_dump 每日 + MinIO 快照,异地一份(对象存储或本地下载) |
| 监控 | 可用性 | Uptime Kuma(轻量自托管)+ docker 日志轮转 |
| pptxgenjs 等新依赖 | M5 | 注意 npm 包体积与 license(MIT) |

**人力:** 1 名熟悉 Go + TS 的全栈开发(后端 Go,前端 TS)。若前端改造(分享 UI、评论 UI)想提速,可加 1 名前端并行做 M4 的 UI 部分。

---

## 八、风险与对策

| 风险 | 影响 | 对策 |
|---|---|---|
| **上游同步漂移**:已决定基本不同步上游,但安全修复仍可能需要 cherry-pick | 长期维护成本 | ① 重组前先做最后一次上游合并;② 定制收敛在 `apps/`;③ `libs/` 尽量不改内部实现;④ 上游补丁按需 cherry-pick,关键改动记录在 `plans/` |
| room 自托管改造踩坑(上游 room 与前端协议版本耦合) | M3 延期 | M3 第一周先原样跑通无鉴权版,确认协议匹配后再加鉴权 |
| 单机故障 | 数据丢失/停服 | 每日备份 + 每周恢复演练;docker `restart: always`;监控告警 |
| 权限模型漏洞(尤其匿名分享链接、room 鉴权) | 数据泄露 | 服务端强制 ACL(不信任前端);M2/M3 各安排一次自审;匿名链接默认只读 |
| 协作大文档性能(几百上千元素) | 卡顿 | M3 压测摸底;document_versions 保留最近 N 版,老版本归档 MinIO |
| 实时协作与离线草稿的冲突语义 | 用户数据丢失 | M1 先只做"单人文档在线保存",M3 上线后明确:开协作即走实时通道 |

---

## 九、待决策项(需要你确认)

1. **PG 数据库在哪**:服务器本机未发现,是否在 K8s 集群里或另一台机器?若找不到,M0 直接在 compose 里起专属 PG 容器
2. **磁盘扩容**:仅剩 20G,是否扩容云盘?
3. **OAuth 提供方**:Google / GitHub / 微信 / 钉钉?影响 M1 范围(初期可只做邮箱+密码)
4. **邮件服务选择**:国内(阿里云邮件推送)还是海外(Resend)?取决于目标用户
5. ~~**域名**:是否已有?子域名方案?~~ **已定(2026-09-23)**:域名已就绪并备案(具体值存 deploy/.env 的 `WEB_DOMAIN`/`API_DOMAIN`,不入库)。环境只有两个 —— 本地=测试(local.yml)、服务器=正式(test.yml),服务器直接用正式域名:裸域(+www 跳转)→ web、api 子域 → api;预留 `files.`(M1)、`room.`(M3)、`admin.`(M7)。不走 test.* 子域名。
6. **PPT 导出规则**:一个 frame 一页?一个文档一页?多文档打包一份 PPT?
7. **匿名分享**是否允许匿名用户直接编辑(目前计划:默认只读,可手动开可编辑)
8. **存储配额**:个人/工作区初始配额给多少(影响 MinIO 容量规划)
