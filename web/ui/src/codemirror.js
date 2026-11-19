// The editor is loaded on first use, so the explorer itself stays small.
import { EditorView, basicSetup } from 'codemirror'
import { EditorState, Compartment } from '@codemirror/state'
import { keymap } from '@codemirror/view'
import { indentWithTab } from '@codemirror/commands'
import { oneDark } from '@codemirror/theme-one-dark'
import { markdown } from '@codemirror/lang-markdown'
import { json } from '@codemirror/lang-json'
import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { html } from '@codemirror/lang-html'
import { css } from '@codemirror/lang-css'
import { xml } from '@codemirror/lang-xml'
import { yaml } from '@codemirror/lang-yaml'
import { sql } from '@codemirror/lang-sql'
import { go } from '@codemirror/lang-go'
import MarkdownIt from 'markdown-it'

function language(name) {
  const ext = name.toLowerCase().split('.').pop()
  switch (ext) {
    case 'md':
    case 'markdown':
      return markdown()
    case 'json':
      return json()
    case 'js':
    case 'mjs':
    case 'cjs':
      return javascript()
    case 'ts':
      return javascript({ typescript: true })
    case 'py':
      return python()
    case 'html':
    case 'htm':
      return html()
    case 'css':
    case 'scss':
      return css()
    case 'xml':
    case 'svg':
      return xml()
    case 'yml':
    case 'yaml':
      return yaml()
    case 'sql':
      return sql()
    case 'go':
      return go()
  }
  return []
}

const darkQuery = () => {
  const t = document.documentElement.dataset.theme
  return t ? t === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches
}

const baseTheme = EditorView.theme({
  '&': { height: '100%', fontSize: 'var(--editor-font, 14px)' },
  '.cm-scroller': { fontFamily: "Consolas, 'Cascadia Mono', 'Roboto Mono', monospace", lineHeight: '1.55' },
  '.cm-content': { paddingBottom: '40vh' },
})

/**
 * Creates an editor in parent. lineBreak keeps Windows line endings when the
 * file had them; onchange runs on every edit, onsave on Ctrl+S.
 */
export function createEditor(parent, { text, name, lineBreak, wrap, onchange, onsave }) {
  const theme = new Compartment()
  const wrapping = new Compartment()
  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: text,
      extensions: [
        basicSetup,
        keymap.of([indentWithTab, { key: 'Mod-s', preventDefault: true, run: () => (onsave(), true) }]),
        EditorState.lineSeparator.of(lineBreak),
        language(name),
        baseTheme,
        theme.of(darkQuery() ? oneDark : []),
        wrapping.of(wrap ? EditorView.lineWrapping : []),
        EditorView.updateListener.of((u) => u.docChanged && onchange()),
      ],
    }),
  })
  const media = matchMedia('(prefers-color-scheme: dark)')
  const follow = () => view.dispatch({ effects: theme.reconfigure(darkQuery() ? oneDark : []) })
  media.addEventListener('change', follow)
  const observer = new MutationObserver(follow)
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  return {
    // sliceDoc joins lines with the configured separator; toString always uses LF.
    text: () => view.state.sliceDoc(),
    setText(t) {
      view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: t } })
    },
    setWrap(on) {
      view.dispatch({ effects: wrapping.reconfigure(on ? EditorView.lineWrapping : []) })
    },
    focus: () => view.focus(),
    destroy() {
      media.removeEventListener('change', follow)
      observer.disconnect()
      view.destroy()
    },
  }
}

// Raw HTML stays text and javascript: links are refused, so a document
// cannot run script in the explorer.
const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
const defaultLink = md.renderer.rules.link_open ?? ((tokens, idx, options, env, self) => self.renderToken(tokens, idx, options))
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx].attrSet('target', '_blank')
  tokens[idx].attrSet('rel', 'noopener noreferrer')
  return defaultLink(tokens, idx, options, env, self)
}

// Checklists like "- [ ] Milch" show as boxes. This runs on the rendered,
// already escaped HTML, so it adds no markup from the file.
export const renderMarkdown = (text) =>
  md
    .render(text)
    .replace(/<li>\[ \] ?/g, '<li class="task">☐ ')
    .replace(/<li>\[[xX]\] ?/g, '<li class="task">☑ ')
