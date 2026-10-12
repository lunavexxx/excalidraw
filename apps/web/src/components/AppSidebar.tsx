import { DefaultSidebar, Sidebar } from "@excalidraw/excalidraw";
import {
  messageCircleIcon,
  presentationIcon,
} from "@excalidraw/excalidraw/components/icons";

import { CommentsPanel } from "../comments/CommentsPanel";

import { PresentationPanel } from "./PresentationPanel";
import { CanvasPanel } from "./CanvasPanel";

import "./AppSidebar.scss";

const canvasStackIcon = (
  <svg
    viewBox="0 0 24 24"
    width="18"
    height="18"
    fill="none"
    stroke="currentColor"
    strokeWidth="1.6"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <rect x="4" y="4" width="14" height="11" rx="2" />
    <path d="M7 19h13a1 1 0 0 0 1-1V9" />
  </svg>
);

export const AppSidebar = () => {
  return (
    <DefaultSidebar>
      <DefaultSidebar.TabTriggers>
        <Sidebar.TabTrigger tab="canvases">
          {canvasStackIcon}
        </Sidebar.TabTrigger>
        <Sidebar.TabTrigger tab="comments">
          {messageCircleIcon}
        </Sidebar.TabTrigger>
        <Sidebar.TabTrigger tab="presentation">
          {presentationIcon}
        </Sidebar.TabTrigger>
      </DefaultSidebar.TabTriggers>
      <Sidebar.Tab tab="comments">
        <CommentsPanel />
      </Sidebar.Tab>
      <Sidebar.Tab tab="presentation">
        <PresentationPanel />
      </Sidebar.Tab>
      <Sidebar.Tab tab="canvases">
        <CanvasPanel />
      </Sidebar.Tab>
    </DefaultSidebar>
  );
};
