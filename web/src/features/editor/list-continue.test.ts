import { EditorState } from "@codemirror/state";
import { describe, expect, it } from "vitest";
import { continueList } from "./list-continue";

function enter(doc: string): string | null {
  let state = EditorState.create({ doc, selection: { anchor: doc.length } });
  const ok = continueList({
    state,
    dispatch: (tr) => {
      state = tr.state;
    },
  });
  return ok ? state.doc.toString() : null;
}

describe("continueList", () => {
  it("continues bullets, numbers, and task items", () => {
    expect(enter("- one")).toBe("- one\n- ");
    expect(enter("  * one")).toBe("  * one\n  * ");
    expect(enter("9. nine")).toBe("9. nine\n10. ");
    expect(enter("- [x] done")).toBe("- [x] done\n- [ ] ");
  });
  it("ends the list on an empty item", () => {
    expect(enter("- one\n- ")).toBe("- one\n");
  });
  it("leaves other lines to the default Enter", () => {
    expect(enter("plain text")).toBeNull();
    expect(enter("-not a list")).toBeNull();
  });
});
