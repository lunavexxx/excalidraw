import { loginIcon, eyeIcon } from "@excalidraw/excalidraw/components/icons";
import { useI18n } from "@excalidraw/excalidraw/i18n";
import { MainMenu } from "@excalidraw/excalidraw/index";
import React from "react";

import { isDevEnv } from "@excalidraw/common";

import type { Theme } from "@excalidraw/element/types";

import { LanguageList } from "../app-language/LanguageList";
import { useAtomValue, useSetAtom } from "../app-jotai";
import { logout } from "../auth/api";
import { currentUserAtom } from "../auth/atoms";
import { parseCanvasIdFromPath } from "../canvas/load";

import { saveDebugState } from "./DebugCanvas";

// 由用户 ID 决定头像底色,同一用户恒定(与登录页/后续协作场景共用规则)
const avatarColor = (id: string) => {
  let hash = 5381;
  for (let i = 0; i < id.length; i++) {
    hash = (hash * 33) ^ id.charCodeAt(i);
  }
  return `hsl(${Math.abs(hash) % 360}, 55%, 45%)`;
};

export const AppMainMenu: React.FC<{
  onCollabDialogOpen: () => any;
  isCollaborating: boolean;
  isCollabEnabled: boolean;
  theme: Theme | "system";
  refresh: () => void;
}> = React.memo((props) => {
  const { t } = useI18n();
  const currentUser = useAtomValue(currentUserAtom);
  const setCurrentUser = useSetAtom(currentUserAtom);
  return (
    <MainMenu>
      <MainMenu.DefaultItems.LoadScene />
      <MainMenu.DefaultItems.SaveToActiveFile />
      <MainMenu.DefaultItems.Export />
      <MainMenu.DefaultItems.SaveAsImage />
      {props.isCollabEnabled && (
        <MainMenu.DefaultItems.LiveCollaborationTrigger
          isCollaborating={props.isCollaborating}
          onSelect={() => props.onCollabDialogOpen()}
        />
      )}
      <MainMenu.DefaultItems.CommandPalette className="highlighted" />
      <MainMenu.DefaultItems.SearchMenu />
      <MainMenu.DefaultItems.Help />
      <MainMenu.DefaultItems.ClearCanvas />
      <MainMenu.Separator />
      {currentUser ? (
        <>
          <MainMenu.ItemCustom>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: "0.85em",
                width: "100%",
                padding: "0.25rem 0rem",
              }}
            >
              <span
                style={{
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  flexShrink: 0,
                  width: "2rem",
                  height: "2rem",
                  borderRadius: "50%",
                  color: "#fff",
                  fontSize: "0.875rem",
                  fontWeight: 700,
                  background: avatarColor(currentUser.id),
                }}
              >
                {currentUser.nickname.slice(0, 1).toUpperCase()}
              </span>
              <div style={{ minWidth: 0 }}>
                <div
                  style={{
                    fontWeight: 700,
                    fontSize: "0.875rem",
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {currentUser.nickname}
                </div>
                <div
                  style={{
                    fontSize: "0.75rem",
                    opacity: 0.7,
                    whiteSpace: "nowrap",
                  }}
                >
                  {currentUser.phone_masked}
                </div>
              </div>
            </div>
          </MainMenu.ItemCustom>
          <MainMenu.Item
            icon={loginIcon}
            onSelect={async () => {
              await logout();
              setCurrentUser(null);
              // 登出时若停留在云端画布:回首页恢复本地草稿,
              // 避免停留在不可保存的编辑态
              if (parseCanvasIdFromPath()) {
                window.location.assign("/");
              }
            }}
          >
            {t("userArea.logout")}
          </MainMenu.Item>
        </>
      ) : (
        <MainMenu.Item
          icon={loginIcon}
          onSelect={() => {
            window.location.assign("/signup");
          }}
          className="highlighted"
        >
          {t("userArea.signInPrompt")}
        </MainMenu.Item>
      )}
      {isDevEnv() && (
        <MainMenu.Item
          icon={eyeIcon}
          onSelect={() => {
            if (window.visualDebug) {
              delete window.visualDebug;
              saveDebugState({ enabled: false });
            } else {
              window.visualDebug = { data: [] };
              saveDebugState({ enabled: true });
            }
            props?.refresh();
          }}
        >
          Visual Debug
        </MainMenu.Item>
      )}
      <MainMenu.Separator />
      <MainMenu.DefaultItems.Preferences />
      <MainMenu.DefaultItems.ToggleTheme allowSystemTheme theme={props.theme} />
      <MainMenu.ItemCustom>
        <LanguageList style={{ width: "100%" }} />
      </MainMenu.ItemCustom>
      <MainMenu.DefaultItems.ChangeCanvasBackground />
    </MainMenu>
  );
});
