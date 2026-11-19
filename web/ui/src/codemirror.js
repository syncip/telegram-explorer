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
import { yaml, yamlFrontmatter } from '@codemirror/lang-yaml'
import { sql } from '@codemirror/lang-sql'
import { go } from '@codemirror/lang-go'
import MarkdownIt from 'markdown-it'
import { obsidianExtensions, obsidianMarkdown, renderProps, resolveNote, splitFrontmatter, toggleTask } from './obsidian.js'

function language(name) {
  const ext = name.toLowerCase().split('.').pop()
  switch (ext) {
    case 'md':
    case 'markdown':
      return yamlFrontmatter({ content: markdown() })
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
  // Room at the top for the name above another device's cursor in line 1.
  '.cm-content': { paddingTop: '1.3em', paddingBottom: '40vh' },
})

/**
 * Creates an editor in parent for a live session (see collab.js). onchange
 * runs on every edit, onsave on Ctrl+S.
 */
export function createEditor(parent, { live, name, folder, wrap, onchange, onsave, onlink }) {
  const notes = /\.(md|markdown)$/i.test(name) ? obsidianExtensions({ onlink, folder }) : []
  const theme = new Compartment()
  const wrapping = new Compartment()
  let wrapOn = wrap
  const make = (doc, extra) =>
    EditorState.create({
      doc,
      extensions: [
        basicSetup,
        keymap.of([indentWithTab, { key: 'Mod-s', preventDefault: true, run: () => (onsave?.(), true) }]),
        language(name),
        notes,
        baseTheme,
        theme.of(darkQuery() ? oneDark : []),
        wrapping.of(wrapOn ? EditorView.lineWrapping : []),
        EditorView.updateListener.of((u) => {
          live.changed(u)
          if (u.docChanged) onchange?.()
        }),
        extra,
      ],
    })
  const view = new EditorView({ parent, state: make(live.doc, live.extensions()) })
  live.attach(view, make)
  const media = matchMedia('(prefers-color-scheme: dark)')
  const follow = () => view.dispatch({ effects: theme.reconfigure(darkQuery() ? oneDark : []) })
  media.addEventListener('change', follow)
  const observer = new MutationObserver(follow)
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  return {
    view,
    text: () => view.state.doc.toString(),
    setWrap(on) {
      wrapOn = on
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

export { joinLive } from './collab.js'

// Raw HTML stays text and javascript: links are refused, so a document
// cannot run script in the explorer.
const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
const defaultLink = md.renderer.rules.link_open ?? ((tokens, idx, options, env, self) => self.renderToken(tokens, idx, options))
md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx].attrSet('target', '_blank')
  tokens[idx].attrSet('rel', 'noopener noreferrer')
  return defaultLink(tokens, idx, options, env, self)
}

md.use(obsidianMarkdown)

// Frontmatter shows as a table; checkboxes know their line in the whole file.
export function renderMarkdown(text) {
  const { props, body, lines } = splitFrontmatter(text)
  return renderProps(props, md.utils.escapeHtml) + md.render(body, { lineOffset: lines })
}

export { resolveNote, toggleTask }
