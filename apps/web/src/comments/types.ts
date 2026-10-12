export type CommentAuthor = { id: string; name: string; avatar: string };
export type CommentAnchor = {
  x: number;
  y: number;
  element_id?: string;
  u?: number;
  v?: number;
};
export type CommentMention = { user_id: string; start: number; end: number };
export type CommentReaction = { emoji: string; users: CommentAuthor[] };
export type CommentMessage = {
  id: string;
  seq: number;
  author: CommentAuthor;
  body: string;
  is_root: boolean;
  mentions: CommentMention[];
  reactions: CommentReaction[];
  created_at: string;
  edited_at: string | null;
  deleted_at: string | null;
};
export type CommentThread = {
  id: string;
  canvas_id: string;
  author: CommentAuthor;
  anchor: CommentAnchor;
  resolved_at: string | null;
  version: number;
  unread_version: number;
  is_unread: boolean;
  created_at: string;
  activity_at: string;
  preview: string;
  root_deleted: boolean;
  reply_count: number;
  can_resolve: boolean;
  can_delete: boolean;
};
export type CommentPage = {
  items: CommentThread[];
  next_cursor: string;
  total: number;
  unread: number;
};
export type CommentDetail = {
  thread: CommentThread;
  comments: CommentMessage[];
  next_before: number;
};
export type CommentDraft = {
  body: string;
  mentions: CommentMention[];
  requestId: string | null;
};
