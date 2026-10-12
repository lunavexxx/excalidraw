import { useEffect, useRef, useState } from "react";
import { useI18n } from "@excalidraw/excalidraw/i18n";

type EmojiEntry = [string, string, string];
export const QUICK_EMOJI = ["👍", "❤️", "😂", "🎉", "👀", "🙏"];
const aliases: Record<string, string> = {
  "👍": "赞 好 棒 同意",
  "❤️": "爱 心 喜欢",
  "😂": "笑 开心",
  "🎉": "庆祝 礼花",
  "👀": "看 眼睛 关注",
  "🙏": "感谢 谢谢 拜托",
  "😀": "笑 开心",
  "😊": "笑 开心",
  "😍": "喜欢 爱",
  "😢": "哭 难过",
  "😭": "哭 难过",
  "🤔": "思考 疑问",
  "✅": "完成 正确 对",
  "❌": "错误 错",
  "🔥": "火 热",
  "🇨🇳": "中国 国旗",
  "👨‍👩‍👧‍👦": "家庭 家人",
  "🚀": "火箭 发布",
};
const groups = {
  "Smileys & Emotion": "faces",
  "People & Body": "people",
  "Animals & Nature": "nature",
  "Food & Drink": "food",
  "Travel & Places": "travel",
  Activities: "activities",
  Objects: "objects",
  Symbols: "symbols",
  Flags: "flags",
} as const;

export const EmojiPicker = ({
  onSelect,
  onClose,
}: {
  onSelect: (emoji: string) => void;
  onClose: () => void;
}) => {
  const { t } = useI18n();
  const rootRef = useRef<HTMLDivElement>(null);
  const [entries, setEntries] = useState<EmojiEntry[]>([]);
  const [recent, setRecent] = useState<string[]>([]);
  const [query, setQuery] = useState("");
  const [group, setGroup] = useState("");
  const [limit, setLimit] = useState(120);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let disposed = false;
    void import("./emoji-data.json")
      .then((data) => {
        if (!disposed) {
          setEntries(data.default.items as EmojiEntry[]);
        }
      })
      .catch(() => {
        if (!disposed) {
          setFailed(true);
        }
      });
    try {
      const saved: unknown = JSON.parse(
        rootRef.current?.ownerDocument.defaultView?.localStorage.getItem(
          "excalidraw-comment-emoji-recent",
        ) || "[]",
      );
      if (Array.isArray(saved)) {
        setRecent(
          saved
            .filter((item): item is string => typeof item === "string")
            .slice(0, 24),
        );
      }
    } catch {
      /* Storage is optional. */
    }
    rootRef.current?.querySelector<HTMLInputElement>("input")?.focus();
    return () => {
      disposed = true;
    };
  }, []);
  const needle = query.trim().toLowerCase();
  const matches = entries.filter(
    ([emoji, category, name]) =>
      (!group || category === group) &&
      (!needle ||
        `${emoji} ${name} ${aliases[emoji] || ""}`
          .toLowerCase()
          .includes(needle)),
  );
  const choose = (emoji: string) => {
    const next = [emoji, ...recent.filter((item) => item !== emoji)].slice(
      0,
      24,
    );
    try {
      rootRef.current?.ownerDocument.defaultView?.localStorage.setItem(
        "excalidraw-comment-emoji-recent",
        JSON.stringify(next),
      );
    } catch {
      /* Storage is optional. */
    }
    onSelect(emoji);
    onClose();
  };
  return (
    <div
      className="CommentEmojiPicker"
      ref={rootRef}
      role="dialog"
      aria-label={t("comments.emoji")}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <div className="CommentEmojiPicker__header">
        <input
          aria-label={t("comments.emojiSearch")}
          placeholder={t("comments.emojiSearch")}
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setLimit(120);
          }}
        />
        <button
          type="button"
          aria-label={t("comments.close")}
          onClick={onClose}
        >
          ×
        </button>
      </div>
      <select
        aria-label={t("comments.emojiCategory")}
        value={group}
        onChange={(event) => {
          setGroup(event.target.value);
          setLimit(120);
        }}
      >
        <option value="">{t("comments.emojiAll")}</option>
        {Object.entries(groups).map(([name, key]) => (
          <option key={name} value={name}>
            {t(`comments.emojiGroups.${key}`)}
          </option>
        ))}
      </select>
      {!needle && !group && (
        <div
          className="CommentEmojiPicker__grid"
          aria-label={t("comments.emojiRecent")}
        >
          {[
            ...new Set([
              ...recent.filter((emoji) =>
                entries.some((entry) => entry[0] === emoji),
              ),
              ...QUICK_EMOJI,
            ]),
          ].map((emoji) => (
            <button
              type="button"
              key={emoji}
              aria-label={emoji}
              onClick={() => choose(emoji)}
            >
              {emoji}
            </button>
          ))}
        </div>
      )}
      <div className="CommentEmojiPicker__grid">
        {matches.slice(0, limit).map(([emoji, , name]) => (
          <button
            type="button"
            key={emoji}
            title={`${name} ${aliases[emoji] || ""}`}
            aria-label={`${emoji} ${name}`}
            onClick={() => choose(emoji)}
          >
            {emoji}
          </button>
        ))}
      </div>
      {!entries.length && (
        <p role={failed ? "alert" : "status"}>
          {t(failed ? "comments.failed" : "comments.loading")}
        </p>
      )}
      {!!entries.length && !matches.length && <p>{t("comments.noEmoji")}</p>}
      {matches.length > limit && (
        <button
          type="button"
          className="CommentTextButton"
          onClick={() => setLimit((value) => value + 120)}
        >
          {t("comments.loadMore")}
        </button>
      )}
    </div>
  );
};
