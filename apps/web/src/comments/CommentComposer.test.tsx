import {
  fireEvent,
  render,
  screen,
  waitFor,
  act,
} from "@testing-library/react";
import {
  EditorJotaiProvider,
  editorJotaiStore,
} from "@excalidraw/excalidraw/editor-jotai";

import { appJotaiStore, Provider } from "../app-jotai";

import { commentDraftsAtom } from "./atoms";
import { CommentComposer } from "./CommentComposer";
import { mentionCandidates } from "./api";

vi.mock("./api", () => ({
  mentionCandidates: vi.fn(),
  commentError: () => "Failed to send",
}));

beforeEach(() => {
  vi.clearAllMocks();
  appJotaiStore.set(commentDraftsAtom, {});
  vi.mocked(mentionCandidates).mockResolvedValue({
    items: [{ id: "ada", name: "Ada", avatar: "" }],
    next_cursor: "",
  });
});

const mount = (onSubmit: (draft: any) => Promise<void>, canvasId = "canvas") =>
  render(
    <Provider store={appJotaiStore}>
      <EditorJotaiProvider store={editorJotaiStore}>
        <CommentComposer
          canvasId={canvasId}
          draftKey="new"
          onSubmit={onSubmit}
        />
      </EditorJotaiProvider>
    </Provider>,
  );

it("does not send IME Enter, allows newlines and sends emoji-only content intact", async () => {
  const submit = vi.fn().mockResolvedValue(undefined);
  mount(submit);
  const input = screen.getByRole("textbox");
  fireEvent.change(input, { target: { value: "👨‍👩‍👧‍👦🇨🇳👍🏽", selectionStart: 19 } });
  fireEvent.compositionStart(input);
  fireEvent.keyDown(input, { key: "Enter", keyCode: 229 });
  fireEvent.compositionEnd(input);
  fireEvent.keyDown(input, { key: "Enter", shiftKey: true });
  expect(submit).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() => expect(submit).toHaveBeenCalledTimes(1));
  expect(submit.mock.calls[0][0].body).toBe("👨‍👩‍👧‍👦🇨🇳👍🏽");
  expect(submit.mock.calls[0][0].requestId).toMatch(/^[0-9a-f-]{36}$/);
  await waitFor(() => expect(input).toHaveValue(""));
});

it("inserts a picker emoji in the selection and retains text focus", async () => {
  mount(vi.fn().mockResolvedValue(undefined));
  const input = screen.getByRole("textbox") as HTMLTextAreaElement;
  fireEvent.change(input, {
    target: { value: "ABC", selectionStart: 1, selectionEnd: 2 },
  });
  input.setSelectionRange(1, 2);
  fireEvent.select(input);
  fireEvent.click(screen.getByRole("button", { name: "Choose emoji" }));
  fireEvent.click(await screen.findByRole("button", { name: "👍" }));
  await waitFor(() => expect(input).toHaveValue("A👍C"));
  expect(input).toHaveFocus();
  await waitFor(() => expect(input.selectionStart).toBe(3));
});

it("preserves failed drafts and idempotency across reopening, isolates other canvases", async () => {
  const submit = vi.fn().mockRejectedValue(new Error("offline"));
  const first = mount(submit);
  fireEvent.change(screen.getByRole("textbox"), {
    target: { value: "草稿 👍🏽" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByRole("alert");
  const requestId = submit.mock.calls[0][0].requestId;
  first.unmount();
  const second = mount(submit, "other");
  expect(screen.getByRole("textbox")).toHaveValue("");
  second.unmount();
  mount(submit);
  expect(screen.getByRole("textbox")).toHaveValue("草稿 👍🏽");
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(submit).toHaveBeenCalledTimes(2));
  expect(submit.mock.calls[1][0].requestId).toBe(requestId);
});

it("selects a real mention using its UTF-16 position after a family emoji", async () => {
  const submit = vi.fn().mockResolvedValue(undefined);
  mount(submit);
  const input = screen.getByRole("textbox");
  const body = "👨‍👩‍👧‍👦 @Ad";
  fireEvent.change(input, {
    target: { value: body, selectionStart: body.length },
  });
  fireEvent.click(await screen.findByRole("option", { name: "Ada" }));
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "Send" })),
  );
  expect(submit.mock.calls[0][0].mentions).toEqual([
    { user_id: "ada", start: 12, end: 16 },
  ]);
  expect(submit.mock.calls[0][0].body).toBe("👨‍👩‍👧‍👦 @Ada ");
});
