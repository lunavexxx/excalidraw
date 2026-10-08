import { fireEvent, render, screen, waitFor } from "@testing-library/react";

import {
  EditorJotaiProvider,
  editorJotaiStore,
} from "@excalidraw/excalidraw/editor-jotai";

import { appJotaiStore, Provider } from "../app-jotai";
import {
  canvasCapabilitiesAtom,
  canvasRoleAtom,
  onlineUsersAtom,
  presenceConnectedAtom,
} from "../canvas/atoms";
import { createShareLink, listShareLinks } from "../canvas/api";

import { AccessManager } from "./AccessManager";
import { accessEntries, invitations, members, requests } from "./accessApi";

vi.mock("./accessApi", () => ({
  members: vi.fn(),
  requests: vi.fn(),
  invitations: vi.fn(),
  accessEntries: vi.fn(),
  accessApi: vi.fn(),
  inviteByPhone: vi.fn(),
  createAccessEntry: vi.fn(),
  requestAccess: vi.fn(),
}));
vi.mock("../canvas/api", () => ({
  createShareLink: vi.fn(),
  listShareLinks: vi.fn(),
  putCollaborator: vi.fn(),
  removeCollaborator: vi.fn(),
  revokeShareLink: vi.fn(),
}));
vi.mock("@excalidraw/excalidraw/clipboard", () => ({
  copyTextToSystemClipboard: vi.fn().mockRejectedValue(new Error("denied")),
}));

const caps = {
  can_manage_collaborators: true,
  can_manage_share_links: true,
  can_review_requests: true,
};
beforeEach(() => {
  vi.clearAllMocks();
  appJotaiStore.set(canvasCapabilitiesAtom, caps);
  appJotaiStore.set(canvasRoleAtom, "owner");
  appJotaiStore.set(presenceConnectedAtom, true);
  appJotaiStore.set(
    onlineUsersAtom,
    new Map([
      [
        "socket",
        { user_id: "ada", nickname: "Ada", avatar_url: "", role: "editor" },
      ],
    ]),
  );
  vi.mocked(members).mockResolvedValue({
    items: [
      {
        user_id: "ada",
        nickname: "Ada",
        avatar_url: "",
        effective_role: "editor",
        direct_role: "viewer",
        workspace_role: "editor",
        can_manage: false,
      },
    ],
    next_cursor: "",
    capabilities: caps,
  });
  vi.mocked(requests).mockResolvedValue({ items: [] });
  vi.mocked(invitations).mockResolvedValue({ items: [] });
  vi.mocked(accessEntries).mockResolvedValue({ items: [] });
  vi.mocked(listShareLinks).mockResolvedValue([]);
});
it("shows effective inherited access and account presence", async () => {
  render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <AccessManager canvasId="canvas" />
      </EditorJotaiProvider>
    </Provider>,
  );
  expect(await screen.findByText("Ada")).toBeVisible();
  expect(screen.getByText("From workspace")).toBeVisible();
  expect(screen.getByText("Online")).toBeVisible();
  expect(
    screen.getByRole("button", { name: "Remove direct access" }),
  ).toBeVisible();
});
it("lets viewers see members without management actions", async () => {
  appJotaiStore.set(canvasCapabilitiesAtom, {
    can_manage_collaborators: false,
    can_manage_share_links: false,
    can_review_requests: false,
  });
  appJotaiStore.set(canvasRoleAtom, "viewer");
  render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <AccessManager canvasId="canvas" />
      </EditorJotaiProvider>
    </Provider>,
  );
  expect(await screen.findByText("Ada")).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Remove direct access" }),
  ).toBeNull();
  expect(screen.queryByRole("button", { name: "Add directly" })).toBeNull();
  expect(
    screen.getByRole("button", { name: "Request editing access" }),
  ).toBeVisible();
  expect(invitations).not.toHaveBeenCalled();
});
it("retains a newly created token when clipboard access fails", async () => {
  vi.mocked(createShareLink).mockResolvedValue({
    id: "link",
    role: "viewer",
    token: "save-this-token",
    expires_at: null,
    revoked_at: null,
    created_at: "2026-10-08",
  });
  render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <AccessManager canvasId="canvas" />
      </EditorJotaiProvider>
    </Provider>,
  );
  await screen.findByText("Ada");
  // The label comes from the existing sharing translations.
  const { t } = await import("@excalidraw/excalidraw/i18n");
  fireEvent.click(
    screen.getByRole("button", { name: t("sharePanel.createViewerLink") }),
  );
  await waitFor(() =>
    expect(screen.getByDisplayValue(/share=save-this-token/)).toBeVisible(),
  );
  expect(screen.getByRole("alert")).toHaveTextContent("Copy failed");
});
