import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { registerSW } from "virtual:pwa-register";

import "./sentry";

import ExcalidrawApp from "./App";

window.__EXCALIDRAW_SHA__ = import.meta.env.VITE_APP_GIT_SHA;
const rootElement = document.getElementById("root")!;
const root = createRoot(rootElement);
registerSW();

// 路由分流:主站(/)为白板编辑器;/login /signup 为独立登录页(不含编辑器)。
// 登录页(含 ~400KB 装饰 SVG)动态加载,避免编辑器主包超出 PWA 预缓存上限。
const isLoginRoute = /^\/(login|signup)\/?$/.test(window.location.pathname);

const ownerWindow = rootElement.ownerDocument.defaultView!;
const accessKind =
  ownerWindow.location.pathname === "/invite"
    ? "invite"
    : ownerWindow.location.pathname === "/request-access"
    ? "request"
    : null;
if (accessKind) {
  const query = new URLSearchParams(ownerWindow.location.search);
  void import("./share/AccessLandingPage").then(({ AccessLandingApp }) => {
    root.render(
      <StrictMode>
        <AccessLandingApp
          kind={accessKind}
          initialToken={query.get("token") || ""}
          invitationId={query.get("id") || ""}
        />
      </StrictMode>,
    );
  });
} else if (isLoginRoute) {
  void import("./components/LoginPage").then(({ LoginApp }) => {
    root.render(
      <StrictMode>
        <LoginApp />
      </StrictMode>,
    );
  });
} else {
  root.render(
    <StrictMode>
      <ExcalidrawApp />
    </StrictMode>,
  );
}
