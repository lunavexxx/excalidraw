# 品牌替换决策清单

日期:2026-10-12。目的:去品牌化收尾——把所有用户可见的 Excalidraw 品牌出口列全,先拍板再动手,避免漏改和误改。

## 〇、需要先拍板的事(阻塞项)

| # | 决策 | 说明 |
|---|------|------|
| 1 | **新品牌名** | 阻塞所有文案。需要定:中文名/英文名/同一拼写,以及 PWA 名、og:title 里的叫法 |
| 2 | **Logo** | 阻塞所有物料。需要:图形 mark + 字标(欢迎屏大 logo、favicon、PWA 图标、og 卡片) |
| 3 | **官网/分享 URL** | og:url、canonical、sitemap、robots 都要指向 mochixxx.cc(或最终域) |
| 4 | **是否保留开源署名** | 建议保留:页脚/关于页一行"基于 Excalidraw 开源项目"+链接(MIT 合规且诚实,提名式使用安全) |
| 5 | **社区链接去留** | 命令面板/帮助对话框里的 GitHub/X/Discord/YouTube——删,或换成自己的(现在都没有自己的账号) |
| 6 | **素材库去留** | 浏览=libraries.excalidraw.com(上游公共站);发布=上传到**上游 Firebase 后端**(VITE_APP_LIBRARY_BACKEND)。发布必须砍,浏览可留可砍 |
| 7 | **帮助文档指向** | docs.excalidraw.com 链接:暂删、或自建 docs、或先指到自己的 README |

## 一、品牌常量与配置(改一处生效多处)

| 位置 | 内容 |
|------|------|
| `packages/common/src/constants.ts:14` | `APP_NAME = "Excalidraw"`。消费点:App.tsx 里 7 处 `replaceState`(历史记录标题)、包内文案插值 |
| `apps/web/index.html` | `<title>`、meta title/description、og:site_name/url/title/image:alt/description、twitter:site/url/title/description/image、`canonical`、`<h1>` visually-hidden |
| `apps/web/vite.config.mts:242-246` | PWA manifest:name/short_name/description |
| `apps/web/vite.config.mts:147` | Sitemap `hostname: "https://excalidraw.com"` → 生成的 sitemap.xml 全是上游 URL |
| `apps/web/public/robots.txt` | `Sitemap: https://excalidraw.com/sitemap.xml` |

## 二、bug 级残留(与品牌无关也该修,建议先做)

1. **index.html:102-118** Excalidraw+ 自动跳转脚本:cookie `excplus-autoredirect` 存在时把用户重定向到 `app.excalidraw.com`。把自己的用户送给上游付费版。
2. **index.html:219-248** SimpleAnalytics 统计脚本:prod 构建无条件加载 `scripts.simpleanalyticscdn.com`,数据进上游的统计账户;国内访问还额外慢。建议直接删。
3. **index.html:57** canonical + og:url/twitter:url 指向 `excalidraw.com`:告诉搜索引擎正主在上游,SEO 自杀。
4. `src/sentry.ts:6-7` hostname 映射 excalidraw.com(DSN 未配置,Sentry 实际未启用,顺手清)。

## 三、应用内文案(locale)

`packages/excalidraw/locales/` 下 **58 个语言文件,57 个含品牌词,共 825 处**。en.json / zh-CN.json 各约 19 处,清单:

- `labels.addWatermark`:"添加'使用 Excalidraw 创建'水印"——导出水印功能,保留功能改文案
- `labels.madeWithExcalidraw`、`labels.excalidrawLib`("Excalidraw 素材库")、`labels.installPWA`
- `errors.uploadedSecurly`、`errors.invalidSceneUrl`(提到 Excalidraw JSON)
- `labels.mermaidToExcalidraw` / `helpDialog.mermaidToExcalidraw.*`(mermaid 功能在用)
- `labels.shareTitle`:"加入 Excalidraw 实时协作会话"(分享页标题)
- `overwriteConfirm.action.excalidrawPlus.*`(在用,见 §五-1)
- `welcomeScreen.app.center_heading_plus`(已随 2026-10-12 文案修复删除)
- `ai.messageLimit`(AI 功能未启用,休眠文案)

其余 56 个 locale 的品牌词处理:**A.** 脚本批量替换品牌词;**B.** 语言列表裁剪到 en+zh-CN(外语用户回退 en,维护面变小)。可同时做。

## 四、物料(需要新 logo 派生)

`apps/web/public/`:

| 文件 | 用途 | 动作 |
|------|------|------|
| favicon.ico / favicon.svg / favicon-16x16 / 32x32 | 标签页图标 | 换 |
| android-chrome-192/512、maskable_icon_x192/x512、apple-touch-icon | PWA/主屏图标 | 换 |
| og-image-3.png | 社交分享卡(上游品牌图) | 换 |
| screenshots/ ×6 | PWA 安装界面截图(上游的) | 换或删 |
| sidebar-presentation-promo-{light,dark}.jpg | PresentationPanel 空态插画(在用) | 换 |
| sidebar-comments-promo-{light,dark}.jpg | M4 后已无引用 | **直接删** |
| `components/ExcalidrawLogo.tsx` | 手绘 icon path + "Excalidraw" 字标 path;WelcomeScreen.Logo 与 ExportToExcalidrawPlus 在用 | 换成新 logo(欢迎屏需要新字标) |

login-assets/blob-*.svg 为纯装饰,无品牌词,保留。字体(Virgil 等)无品牌问题,保留。

## 五、上游功能耦合(不只是文案,是去留/重构决策)

1. **导出对话框 = Excalidraw+ 出口**(最重要):App.tsx `renderCustomUI` 渲染 `ExportToExcalidrawPlus`,经**上游 Firebase** 加密上传;`.env.production` 里 `VITE_APP_PLUS_APP=https://app.excalidraw.com`。**生产环境导出对话框的保存按钮是把用户数据送上上游后端**。方案:删 renderCustomUI 恢复原生导出 UI(注意会改变导出流程走向,改完要手动走查导出/覆盖确认链路)。
2. OverwriteConfirmDialog 的 excalidrawPlus action(App.tsx:1165-1180)。
3. `ExcalidrawPlusIframeExport`(App.tsx:1345,上游 plus iframe 嵌入流程)+ `isExcalidrawPlusSignedUser`(app_constants,cookie `excplus-auth`;**已随 2026-10-12 文案修复删除**)+ COOKIES 常量。
4. 素材库发布:PublishLibrary.tsx → `libraries.excalidraw.com` / 上游 GitHub / 上游 Firebase(见 §〇-6)。
5. HelpDialog 四个外链:docs.excalidraw.com、plus.excalidraw.com/blog、github issues、youtube.com/@excalidraw(见 §〇-5/7)。
6. 命令面板四条社区命令(App.tsx:1245-1310):GitHub / X / Discord / YouTube。
7. TopErrorBoundary:崩溃报告链接指向上游 GitHub issue 新建页。
8. BraveMeasureTextError(docs.excalidraw.com FAQ)、main-menu `Socials` 组件(未挂载,休眠)。
9. `.env.*`:`VITE_APP_PLUS_APP`、`VITE_APP_LIBRARY_URL`、`VITE_APP_LIBRARY_BACKEND` 按上面决策清理。

## 六、不能动的(误改=事故)

- **LICENSE**(MIT 版权义务,已确认在仓库根部)
- **localStorage/IndexedDB 键**(app_constants `STORAGE_KEYS`):`excalidraw`、`excalidraw-state`、`excalidraw-collab`、`excalidraw-theme`、`excalidraw-debug`、`excalidraw-draft-updated-at`、`excalidraw-library`、`excalidraw-ttd-chats`——改名=清空所有用户的本地草稿/主题/素材库。将来要换名必须走"读旧写新"迁移
- **CSS 类**:`.excalidraw`、`.excalidraw-link`(含外部自定义样式依赖)
- **文件格式互操作**:MIME `application/vnd.excalidraw+json` / `.excalidraw` / `.excalidrawlib` 扩展名(与上游文件兼容性挂钩)
- **npm scope** `@excalidraw/*`:内部 workspace 别名;仅在开源仓库或发包时才需要换(上游拥有该 npm org)
- 代码注释、issue 链接注释、测试夹具等技术标识

## 七、建议执行顺序

1. 拍板 §〇 七项
2. §二 bug 级残留(半天,不依赖品牌名)
3. §一 常量+index.html+manifest+robots(一次提交,半天)
4. §三 locale:en/zh-CN 手改(结合 §五 文案删除),其余脚本过
5. §四 物料(logo 定稿后)
6. §五 功能解耦(导出对话框优先,改完手动走查)
