import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { json } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { defineLanguageFacet, HighlightStyle, Language, syntaxHighlighting } from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { EditorState, type Extension, Prec } from "@codemirror/state";
import { drawSelection, EditorView, highlightActiveLine, keymap, lineNumbers } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import { GFM, parser as markdownParser } from "@lezer/markdown";
import { useEffect, useRef } from "react";
import { flash } from "./flash";
import { continueList } from "./list-continue";

// The theme reads the design tokens, so it follows the light and dark themes with no rebuild.
const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "var(--surface)", color: "var(--ink)", fontSize: "var(--text-sm)" },
  ".cm-scroller": { fontFamily: "var(--font-mono)", lineHeight: "1.65" },
  ".cm-content": { padding: "var(--space-4) 0", caretColor: "var(--accent)" },
  ".cm-line": { padding: "0 var(--space-4)" },
  ".cm-gutters": { backgroundColor: "var(--surface)", color: "var(--ink-3)", border: "none" },
  ".cm-lineNumbers .cm-gutterElement": { padding: "0 var(--space-2) 0 var(--space-3)", minWidth: "3ch" },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "var(--sunken)" },
  "&.cm-focused": { outline: "none" },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: "var(--accent-soft) !important",
  },
  ".cm-cursor": { borderLeftColor: "var(--accent)" },
  ".cm-searchMatch": { backgroundColor: "var(--warn-soft)" },
  ".cm-searchMatch.cm-searchMatch-selected": { backgroundColor: "var(--accent-soft)" },
  ".cm-panels": { backgroundColor: "var(--sunken)", color: "var(--ink)", borderColor: "var(--line)" },
  // The search panel's fields and buttons take CodeMirror's light gradients unless the theme
  // names them, and the dark theme's light text then sits on white.
  ".cm-panel.cm-search": { padding: "var(--space-2) var(--space-3)", fontFamily: "var(--font-sans)" },
  ".cm-panel.cm-search label": { color: "var(--ink-2)", fontSize: "var(--text-xs)" },
  ".cm-panel.cm-search input[type=checkbox]": { accentColor: "var(--accent)" },
  ".cm-textfield, .cm-button": {
    backgroundColor: "var(--surface)",
    backgroundImage: "none",
    color: "var(--ink)",
    border: "1px solid var(--line-strong)",
    borderRadius: "var(--radius-sm)",
    fontSize: "var(--text-xs)",
    padding: "2px 8px",
  },
  ".cm-textfield:focus": { outline: "2px solid var(--focus)", outlineOffset: "-1px" },
  ".cm-button:hover": { backgroundColor: "var(--sunken)" },
  ".cm-panel.cm-search [name=close]": { color: "var(--ink-3)" },
});

const highlight = HighlightStyle.define([
  { tag: t.heading, fontWeight: "650", color: "var(--ink)" },
  { tag: [t.strong], fontWeight: "650" },
  { tag: [t.emphasis], fontStyle: "italic" },
  { tag: [t.link, t.url], color: "var(--accent)" },
  { tag: [t.monospace], color: "var(--code-fn)" },
  { tag: [t.meta, t.processingInstruction, t.contentSeparator], color: "var(--ink-3)" },
  { tag: [t.keyword, t.bool], color: "var(--code-kw)" },
  { tag: [t.string], color: "var(--code-str)" },
  { tag: [t.number], color: "var(--code-num)" },
  { tag: [t.propertyName, t.definition(t.propertyName)], color: "var(--code-fn)" },
  { tag: [t.comment, t.quote], color: "var(--ink-3)" },
]);

// Markdown with GFM, straight from the Lezer parser. @codemirror/lang-markdown also bundles the
// HTML, CSS, and JavaScript languages, which the bundle route's JavaScript budget cannot carry.
const markdownLanguage = new Language(defineLanguageFacet(), markdownParser.configure([GFM]), [], "markdown");

export function languageFor(path: string): Extension[] {
  const ext = path.toLowerCase().split(".").pop();
  if (ext === "md" || ext === "markdown")
    return [markdownLanguage, EditorView.lineWrapping, Prec.high(keymap.of([{ key: "Enter", run: continueList }]))];
  if (ext === "yaml" || ext === "yml") return [yaml()];
  if (ext === "json") return [json()];
  return [];
}

export function CodeEditor({
  docKey,
  initial,
  path,
  readOnly = false,
  onChange,
  onSave,
  onView,
}: {
  // A new docKey replaces the document; the same key keeps the editor and its undo history.
  docKey: string;
  initial: string;
  path: string;
  readOnly?: boolean;
  onChange: (text: string) => void;
  onSave: () => void;
  onView?: (view: EditorView | null) => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const handlers = useRef({ onChange, onSave });
  handlers.current = { onChange, onSave };

  useEffect(() => {
    if (!host.current) return;
    const view = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: initial,
        extensions: [
          lineNumbers(),
          history(),
          drawSelection(),
          flash(),
          highlightActiveLine(),
          highlightSelectionMatches(),
          syntaxHighlighting(highlight),
          theme,
          EditorState.readOnly.of(readOnly),
          keymap.of([
            {
              key: "Mod-s",
              preventDefault: true,
              run: () => {
                handlers.current.onSave();
                return true;
              },
            },
            indentWithTab,
            ...defaultKeymap,
            ...historyKeymap,
            ...searchKeymap,
          ]),
          ...languageFor(path),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) handlers.current.onChange(u.state.doc.toString());
          }),
          EditorView.contentAttributes.of({ "aria-label": `Edit ${path}` }),
        ],
      }),
    });
    onView?.(view);
    return () => {
      onView?.(null);
      view.destroy();
    };
    // The editor is created once per document. Later changes to initial come from the user.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [docKey, readOnly]);

  return <div ref={host} className="h-full min-h-0" />;
}
