// What makes the editor feel like Obsidian: [[Wikilinks]] and ![[embeds]],
// #tags, checklists that tick in the preview, frontmatter as properties and
// callouts like "> [!note]". Everything here renders escaped text only.
import { autocompletion } from '@codemirror/autocomplete'
import { Decoration, EditorView, MatchDecorator, ViewPlugin } from '@codemirror/view'

const IMAGE = /\.(png|jpe?g|gif|webp|bmp|svg|avif)$/i

// Finding notes ---------------------------------------------------------------

async function search(q) {
  const res = await fetch('/api/search?q=' + encodeURIComponent(q), { headers: { 'X-Telex': '1' } })
  return res.ok ? res.json() : []
}

const cache = new Map()

/**
 * Finds the file a link like [[Einkauf]], [[Ordner/Einkauf]] or
 * ![[bild.png]] points to: the same name, a note with .md added, preferring
 * the folder of the current note.
 */
export async function resolveNote(target, folder) {
  const name = target.split('/').pop().trim()
  if (!name) return null
  const key = `${folder}/${name.toLowerCase()}`
  if (cache.has(key)) return cache.get(key)
  const wanted = [name.toLowerCase(), name.toLowerCase() + '.md']
  const hits = (await search(name)).filter((n) => !n.isDir && wanted.includes(n.name.toLowerCase()))
  hits.sort((a, b) => (b.parentId === folder) - (a.parentId === folder) || a.name.length - b.name.length)
  const found = hits[0] ?? null
  cache.set(key, found)
  setTimeout(() => cache.delete(key), 30000)
  return found
}

// Editor ----------------------------------------------------------------------

const linkMarks = new MatchDecorator({
  regexp: /!?\[\[[^\]\n]+\]\]|(?<=^|\s)#[\p{L}\p{N}_/-]*\p{L}[\p{L}\p{N}_/-]*/gu,
  decoration: (m) => Decoration.mark({ class: m[0].startsWith('#') ? 'cm-tag' : 'cm-wikilink' }),
})

const linkPlugin = ViewPlugin.fromClass(
  class {
    constructor(view) {
      this.decorations = linkMarks.createDeco(view)
    }
    update(u) {
      this.decorations = linkMarks.updateDeco(u, this.decorations)
    }
  },
  { decorations: (v) => v.decorations },
)

const theme = EditorView.baseTheme({
  '.cm-wikilink': { color: 'var(--accent, #4c6ef5)', textDecoration: 'underline', textUnderlineOffset: '3px' },
  '.cm-tag': { color: 'var(--accent, #4c6ef5)', background: 'color-mix(in srgb, currentColor 12%, transparent)', borderRadius: '4px' },
})

function linkAt(view, pos) {
  const line = view.state.doc.lineAt(pos)
  for (const m of line.text.matchAll(/!?\[\[([^\]\n]+)\]\]/g)) {
    const from = line.from + m.index
    if (pos >= from && pos <= from + m[0].length) return m[1].split('|')[0].split('#')[0]
  }
  return null
}

/**
 * Extensions for Markdown notes. onlink(target) opens a linked note; Ctrl or
 * Cmd and a click on a link calls it. folder is the note's folder id.
 */
export function obsidianExtensions({ onlink, folder }) {
  const complete = async (ctx) => {
    const m = ctx.matchBefore(/\[\[[^\]\n|#]*/)
    if (!m) return null
    const typed = m.text.slice(2)
    const list = typed ? await search(typed) : []
    const notes = list.filter((n) => !n.isDir).sort((a, b) => (b.parentId === folder) - (a.parentId === folder))
    const after = ctx.state.sliceDoc(ctx.pos, ctx.pos + 2)
    return {
      from: m.from + 2,
      options: notes.slice(0, 50).map((n) => {
        const label = n.name.replace(/\.md$/i, '')
        return { label, detail: /\.md$/i.test(n.name) ? '' : 'Datei', apply: after === ']]' ? label : label + ']]' }
      }),
      validFor: /^[^\]\n|#]*$/,
    }
  }
  return [
    linkPlugin,
    theme,
    autocompletion({ override: [complete], activateOnTyping: true }),
    EditorView.domEventHandlers({
      mousedown(e, view) {
        if (!(e.ctrlKey || e.metaKey)) return false
        const pos = view.posAtCoords({ x: e.clientX, y: e.clientY })
        const target = pos === null ? null : linkAt(view, pos)
        if (!target) return false
        e.preventDefault()
        onlink(target)
        return true
      },
    }),
  ]
}

// Preview ---------------------------------------------------------------------

/** Splits YAML frontmatter off; lines counts the lines it took. */
export function splitFrontmatter(text) {
  const m = /^---\r?\n([\s\S]*?)\r?\n(?:---|\.\.\.)\s*(?:\r?\n|$)/.exec(text)
  if (!m) return { props: null, body: text, lines: 0 }
  const props = []
  for (const line of m[1].split(/\r?\n/)) {
    const item = /^\s+-\s+(.*)$/.exec(line)
    if (item && props.length) {
      props[props.length - 1].values.push(item[1])
      continue
    }
    const kv = /^([^:#\s][^:]*):\s*(.*)$/.exec(line)
    if (kv) {
      const raw = kv[2].trim()
      const values = /^\[.*\]$/.test(raw) ? raw.slice(1, -1).split(',').map((v) => v.trim()).filter(Boolean) : raw ? [raw] : []
      props.push({ key: kv[1].trim(), values })
    }
  }
  return { props, body: text.slice(m[0].length), lines: m[0].split('\n').length - 1 }
}

/** Adds wikilinks, embeds, tags, tickable checklists and callouts to markdown-it. */
export function obsidianMarkdown(md) {
  const esc = md.utils.escapeHtml

  md.inline.ruler.before('link', 'wikilink', (state, silent) => {
    const src = state.src
    let start = state.pos
    let embed = false
    if (src.charCodeAt(start) === 0x21 && src.startsWith('[[', start + 1)) {
      embed = true
      start++
    } else if (!src.startsWith('[[', start)) return false
    const end = src.indexOf(']]', start + 2)
    if (end < 0) return false
    const inner = src.slice(start + 2, end)
    if (!inner.trim() || inner.includes('\n') || inner.includes('[')) return false
    if (!silent) state.push('wikilink', '', 0).meta = { inner, embed }
    state.pos = end + 2
    return true
  })
  md.renderer.rules.wikilink = (tokens, idx) => {
    const { inner, embed } = tokens[idx].meta
    const [target, alias] = inner.split('|')
    const file = target.split('#')[0].trim()
    if (embed && IMAGE.test(file)) {
      return `<img class="embed" data-note="${esc(file)}" alt="${esc(alias ?? file)}">`
    }
    return `<a class="wikilink" href="#" data-note="${esc(file)}">${esc((alias ?? target).trim())}</a>`
  }

  md.inline.ruler.push('tag', (state, silent) => {
    if (state.src.charCodeAt(state.pos) !== 0x23) return false
    const prev = state.pos > 0 ? state.src[state.pos - 1] : ' '
    if (!/\s/.test(prev)) return false
    const m = /^#([\p{L}\p{N}_/-]*\p{L}[\p{L}\p{N}_/-]*)/u.exec(state.src.slice(state.pos))
    if (!m) return false
    if (!silent) state.push('tag', '', 0).content = m[1]
    state.pos += m[0].length
    return true
  })
  md.renderer.rules.tag = (tokens, idx) => `<span class="tag">#${esc(tokens[idx].content)}</span>`

  md.core.ruler.push('obsidian_blocks', (state) => {
    const tokens = state.tokens
    const offset = state.env?.lineOffset ?? 0
    for (let i = 0; i < tokens.length; i++) {
      const t = tokens[i]
      // "- [ ] Milch": a checkbox that knows its source line.
      if (t.type === 'list_item_open' && tokens[i + 2]?.type === 'inline') {
        const inline = tokens[i + 2]
        const m = /^\[([ xX])\]\s+/.exec(inline.content)
        if (m && inline.children?.[0]?.type === 'text') {
          const done = m[1] !== ' '
          t.attrJoin('class', done ? 'task done' : 'task')
          inline.children[0].content = inline.children[0].content.slice(m[0].length)
          const box = new state.Token('task_box', '', 0)
          box.meta = { done, line: (t.map?.[0] ?? 0) + offset }
          inline.children.unshift(box)
        }
      }
      // "> [!warning] Titel": a callout box.
      if (t.type === 'blockquote_open' && tokens[i + 2]?.type === 'inline') {
        const inline = tokens[i + 2]
        const first = inline.children?.[0]
        const m = first?.type === 'text' && /^\[!(\w+)\][+-]?\s*/.exec(first.content)
        if (m) {
          const kind = m[1].toLowerCase()
          t.attrJoin('class', `callout callout-${kind}`)
          const title = new state.Token('callout_title', '', 0)
          title.content = first.content.slice(m[0].length) || kind[0].toUpperCase() + kind.slice(1)
          inline.children.splice(0, inline.children[1]?.type === 'softbreak' ? 2 : 1, title)
        }
      }
    }
  })
  md.renderer.rules.task_box = (tokens, idx) => {
    const { done, line } = tokens[idx].meta
    return `<input type="checkbox" class="task-box" data-line="${line}"${done ? ' checked' : ''}> `
  }
  md.renderer.rules.callout_title = (tokens, idx) => `<div class="callout-title">${esc(tokens[idx].content)}</div>`
}

/** The frontmatter as a table of properties. */
export function renderProps(props, esc) {
  if (!props?.length) return ''
  const rows = props
    .map((p) => {
      const vals = p.values.map((v) => (/^tags?$/i.test(p.key) ? `<span class="tag">#${esc(v.replace(/^#/, ''))}</span>` : esc(v)))
      return `<tr><th>${esc(p.key)}</th><td>${vals.join(', ')}</td></tr>`
    })
    .join('')
  return `<table class="props">${rows}</table>`
}

/** Ticks or unticks the checkbox on a line of the note. */
export function toggleTask(view, line) {
  const doc = view.state.doc
  if (line < 0 || line >= doc.lines) return
  const l = doc.line(line + 1)
  const m = /^(\s*(?:[-*+]|\d+[.)])\s+\[)([ xX])\]/.exec(l.text)
  if (!m) return
  const at = l.from + m[1].length
  view.dispatch({ changes: { from: at, to: at + 1, insert: m[2] === ' ' ? 'x' : ' ' } })
}
