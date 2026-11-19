<script>
  import { untrack } from 'svelte'
  import { formatSize } from './api.js'
  import { isApp } from './native.js'
  import Dialog from './Dialog.svelte'
  import Icon from './Icon.svelte'

  // Everyone who opens the file edits the same text live; the server stores it.
  // onopen(node) switches to another note, for a clicked [[link]].
  let { node: opened, onclose, onsaved, onopen, standalone = false } = $props()

  const OPEN_LIMIT = 5 << 20
  const fontRaw = Number(new URLSearchParams(location.search).get('font')) || 0
  const fontParam = fontRaw ? Math.min(32, Math.max(10, fontRaw)) : 0
  const WRAP_KEY = 'telex-editor-wrap'

  let node = $state(opened)
  let dialog = $state()
  let host = $state()
  let editor = null
  let live = null
  let lib = null
  let error = $state('')
  let loading = $state(true)
  let status = $state({ online: true })
  let ask = $state(null) // 'close' or { conflict: text }
  let preview = $state(false)
  let previewHtml = $state('')
  let wrap = $state(readWrap())

  const isMarkdown = $derived(/\.(md|markdown)$/i.test(node.name))

  function readWrap() {
    try {
      return localStorage.getItem(WRAP_KEY) !== '0'
    } catch {
      return true
    }
  }

  // How this device shows up at the others' cursors.
  function deviceName() {
    const fromApp = new URLSearchParams(location.search).get('device')
    if (fromApp) return fromApp.slice(0, 30)
    const ua = navigator.userAgent
    const os = /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iPhone' : /Windows/.test(ua) ? 'Windows' : /Mac/.test(ua) ? 'Mac' : /Linux/.test(ua) ? 'Linux' : ''
    if (isApp) return 'Handy'
    return os ? `Browser (${os})` : 'Browser'
  }

  // Runs once: load reads node, and a new version must not reload the editor.
  $effect(() => {
    if (!standalone) dialog.showModal()
    untrack(load)
    const leave = (e) => {
      if (live?.pending) e.preventDefault()
    }
    const gone = () => live?.leave()
    addEventListener('beforeunload', leave)
    addEventListener('pagehide', gone)
    return () => {
      removeEventListener('beforeunload', leave)
      removeEventListener('pagehide', gone)
      live?.leave()
      editor?.destroy()
    }
  })

  async function load() {
    if (node.size > OPEN_LIMIT) {
      error = `Die Datei ist ${formatSize(node.size)} groß. Der Editor öffnet Dateien bis 5 MB.`
      loading = false
      return
    }
    try {
      lib = await import('./codemirror.js')
      live = await lib.joinLive(node.id, {
        name: deviceName(),
        onstatus: (s) => {
          status = s
          if (s.node && s.node.id !== node.id) {
            node = s.node
            onsaved?.(node)
          }
        },
        onconflict: (text) => (ask = { conflict: text }),
      })
      editor = lib.createEditor(host, {
        live,
        name: node.name,
        folder: node.parentId,
        onlink: openLink,
        wrap,
        onchange: () => {
          if (preview) renderPreview()
        },
        onsave: () => live.flush(),
      })
      if (preview) renderPreview()
      editor.focus()
    } catch (err) {
      error = err.status === 423 ? 'Der Tresor ist gesperrt. Entsperre ihn und öffne die Datei dann noch einmal.' : err.message
    }
    loading = false
  }

  function renderPreview() {
    if (lib && editor) previewHtml = lib.renderMarkdown(editor.text())
  }

  // Embedded images find their files once the preview shows them.
  let article = $state()
  $effect(() => {
    previewHtml
    if (!article || !lib) return
    for (const img of article.querySelectorAll('img.embed[data-note]:not([src])')) {
      lib.resolveNote(img.dataset.note, node.parentId).then((n) => {
        if (n) img.src = `/api/nodes/${n.id}/content?inline=1&v=${n.size}-${n.mtime}`
        else img.replaceWith(Object.assign(document.createElement('span'), { className: 'missing', textContent: `„${img.dataset.note}“ nicht gefunden` }))
      })
    }
  })

  // Opens the note a [[link]] points to; one that does not exist yet can be created.
  async function openLink(target) {
    const found = await lib.resolveNote(target, node.parentId)
    if (found) {
      await leaveFor(found)
      return
    }
    ask = { create: target.split('/').pop().trim() }
  }

  async function leaveFor(next) {
    if (live?.pending) await live.flush()
    onopen?.(next)
  }

  async function createNote(name) {
    ask = null
    const file = /\.\w+$/.test(name) ? name : name + '.md'
    try {
      const res = await fetch(`/api/nodes/${node.parentId}/textfile`, {
        method: 'POST',
        headers: { 'X-Telex': '1', 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: file }),
      })
      const data = await res.json()
      if (!res.ok) throw new Error(data?.error ?? `Fehler ${res.status}`)
      await leaveFor(data)
    } catch (err) {
      error = err.message
    }
  }

  function previewClick(e) {
    const box = e.target.closest('input.task-box')
    if (box) {
      e.preventDefault()
      lib.toggleTask(editor.view, Number(box.dataset.line))
      return
    }
    const link = e.target.closest('a.wikilink')
    if (link) {
      e.preventDefault()
      openLink(link.dataset.note)
    }
  }

  export async function close() {
    if (live?.pending) {
      await live.flush()
      if (live.pending) {
        ask = 'close'
        return
      }
    }
    onclose()
  }

  function toggleWrap() {
    wrap = !wrap
    editor?.setWrap(wrap)
    try {
      localStorage.setItem(WRAP_KEY, wrap ? '1' : '0')
    } catch {}
  }

  function togglePreview() {
    preview = !preview
    if (preview) renderPreview()
  }

  const time = (unix) => new Date(unix * 1000).toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' })

  const statusText = $derived(
    loading
      ? 'Wird geladen …'
      : !status.online
        ? 'Offline · Änderungen werden nachgereicht'
        : status.pending
          ? 'Wird gespeichert …'
          : status.savedAt
            ? `Gespeichert ${time(status.savedAt)}`
            : formatSize(node.size),
  )
  const peers = $derived(status.peers ?? [])
</script>

{#snippet body()}
  <header>
    <button class="icon" aria-label="Schließen" title="Schließen" onclick={close}><Icon name="close" size={16} /></button>
    <div class="title">
      <h2>{node.name}</h2>
      <span class:warn={!status.online || status.problem}>{statusText}</span>
    </div>
    {#if peers.length}
      <span class="peers" title={`Bearbeitet gerade auch: ${peers.map((p) => p.name || 'Gerät').join(', ')}`}>
        <span class="dot"></span>{peers.length === 1 ? peers[0].name || '1 Gerät' : `${peers.length} Geräte`}
      </span>
    {/if}
    {#if isMarkdown}
      <button class="tool" class:on={preview} aria-pressed={preview} onclick={togglePreview} title="Markdown-Vorschau">
        <Icon name="info" /><span>Vorschau</span>
      </button>
    {/if}
    <button class="tool" class:on={wrap} aria-pressed={wrap} onclick={toggleWrap} title="Lange Zeilen umbrechen">
      <Icon name="list" /><span>Umbruch</span>
    </button>
  </header>
  {#if error || status.problem}<p class="err" role="alert">{error || status.problem}</p>{/if}
  <div class="panes" class:split={preview}>
    <div class="code" bind:this={host} class:hidden={loading}></div>
    {#if loading}<p class="hint">Wird geladen …</p>{/if}
    {#if preview}
      <!-- markdown-it runs with html: false, so this holds no markup from the file. -->
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
      <article class="md" bind:this={article} onclick={previewClick}>{@html previewHtml}</article>
    {/if}
  </div>
{/snippet}

{#if standalone}
  <!-- The app asks for its larger type with ?font=. -->
  <div class="editor standalone" style:--editor-font={fontParam ? `${fontParam}px` : null}>{@render body()}</div>
{:else}
  <dialog bind:this={dialog} class="editor" aria-label={`${node.name} bearbeiten`} oncancel={(e) => { e.preventDefault(); close() }}>
    {@render body()}
  </dialog>
{/if}

{#if ask === 'close'}
  <Dialog title="Noch nicht übertragen" onclose={() => (ask = null)}>
    <p>Die letzten Änderungen an „{node.name}“ sind noch nicht beim Server. Bleibt der Editor offen, werden sie übertragen, sobald die Verbindung wieder steht.</p>
    <div class="actions">
      <button class="primary" onclick={() => (ask = null)}>Offen lassen</button>
      <button class="secondary" onclick={() => { ask = null; onclose() }}>Trotzdem schließen</button>
    </div>
  </Dialog>
{:else if ask?.create}
  <Dialog title="Notiz anlegen?" onclose={() => (ask = null)}>
    <p>„{ask.create}“ gibt es noch nicht. Soll die Notiz im selben Ordner angelegt werden?</p>
    <div class="actions">
      <button class="primary" onclick={() => createNote(ask.create)}>Anlegen und öffnen</button>
      <button class="secondary" onclick={() => (ask = null)}>Abbrechen</button>
    </div>
  </Dialog>
{:else if ask?.conflict !== undefined}
  <Dialog title="Datei wurde inzwischen geändert" onclose={() => (ask = null)}>
    <p>Während die Verbindung weg war, hat sich „{node.name}“ auf dem Server geändert. Der Editor zeigt jetzt die Fassung des Servers.</p>
    <p>Übernimmst du deine Fassung, ersetzt sie den Text für alle.</p>
    <div class="actions">
      <button class="primary" onclick={() => { live.replaceWith(ask.conflict); ask = null }}>Meine Fassung übernehmen</button>
      <button class="secondary" onclick={() => (ask = null)}>Server-Fassung behalten</button>
    </div>
  </Dialog>
{/if}

<style>
  dialog.editor {
    width: 100vw;
    height: 100vh;
    height: 100dvh;
    max-width: none;
    max-height: none;
    margin: 0;
    padding: 0;
    border: 0;
    background: var(--surface);
    color: var(--text);
  }
  dialog.editor[open],
  .standalone {
    display: flex;
    flex-direction: column;
  }
  .standalone {
    position: fixed;
    inset: 0;
    background: var(--surface);
    color: var(--text);
  }
  .peers {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    padding: 0 10px;
    height: 28px;
    border-radius: 14px;
    background: var(--hover);
    font-size: 13px;
    white-space: nowrap;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #2f9e44;
  }
  header {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 6px 10px;
    border-bottom: 1px solid var(--line);
    flex: none;
  }
  .title {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    padding: 0 6px;
  }
  h2 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .title span {
    font-size: 12px;
    color: var(--muted);
  }
  .title span.warn {
    color: var(--warning-text);
  }
  .tool {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 34px;
    padding: 0 10px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: none;
    color: var(--text);
    cursor: pointer;
  }
  .tool:hover {
    background: var(--hover);
  }
  .tool.on {
    background: var(--selected);
    border-color: var(--selected-line);
  }
  .icon {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    border: 0;
    border-radius: 4px;
    background: none;
    color: var(--text);
    cursor: pointer;
  }
  .icon:hover {
    background: var(--hover);
  }
  .err {
    margin: 0;
    padding: 8px 14px;
    background: color-mix(in srgb, var(--danger) 12%, transparent);
    color: var(--danger);
  }
  .panes {
    flex: 1;
    min-height: 0;
    display: flex;
  }
  .code {
    flex: 1;
    min-width: 0;
    height: 100%;
  }
  .hidden {
    display: none;
  }
  .hint {
    margin: 20px;
    color: var(--muted);
  }
  .md {
    flex: 1;
    min-width: 0;
    overflow: auto;
    padding: 16px 24px;
    border-left: 1px solid var(--line);
    background: var(--surface-2);
    line-height: 1.6;
    overflow-wrap: anywhere;
  }
  .md :global(pre),
  .md :global(code) {
    font-family: Consolas, 'Cascadia Mono', monospace;
    background: var(--hover);
    border-radius: 4px;
  }
  .md :global(pre) {
    padding: 10px;
    overflow: auto;
  }
  .md :global(table) {
    border-collapse: collapse;
  }
  .md :global(th),
  .md :global(td) {
    border: 1px solid var(--line);
    padding: 4px 8px;
  }
  .md :global(img) {
    max-width: 100%;
  }
  .md :global(li.task) {
    list-style: none;
    margin-left: -1.4em;
  }
  .md :global(li.task.done) {
    color: var(--muted);
    text-decoration: line-through;
  }
  .md :global(.task-box) {
    width: 16px;
    height: 16px;
    margin: 0 4px 0 0;
    vertical-align: -2px;
    cursor: pointer;
    accent-color: var(--accent);
  }
  .md :global(a.wikilink) {
    color: var(--accent);
  }
  .md :global(.tag) {
    padding: 0 5px;
    border-radius: 4px;
    background: color-mix(in srgb, var(--accent) 14%, transparent);
    color: var(--accent);
    font-size: 0.92em;
  }
  .md :global(table.props) {
    margin-bottom: 16px;
    font-size: 0.92em;
  }
  .md :global(table.props th) {
    text-align: left;
    color: var(--muted);
    font-weight: 500;
  }
  .md :global(.callout) {
    margin: 12px 0;
    padding: 8px 14px;
    border-left: 4px solid var(--accent);
    border-radius: 4px;
    background: color-mix(in srgb, var(--accent) 8%, transparent);
  }
  .md :global(.callout-warning),
  .md :global(.callout-caution) {
    border-color: #e67700;
    background: color-mix(in srgb, #e67700 10%, transparent);
  }
  .md :global(.callout-danger),
  .md :global(.callout-error) {
    border-color: var(--danger);
    background: color-mix(in srgb, var(--danger) 10%, transparent);
  }
  .md :global(.callout-tip),
  .md :global(.callout-success) {
    border-color: #2f9e44;
    background: color-mix(in srgb, #2f9e44 10%, transparent);
  }
  .md :global(.callout-title) {
    font-weight: 600;
    margin-bottom: 4px;
  }
  .md :global(.missing) {
    color: var(--muted);
    font-style: italic;
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 16px;
  }
  @media (max-width: 700px) {
    .tool span {
      display: none;
    }
    .peers {
      max-width: 110px;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .panes.split .code {
      display: none;
    }
    .md {
      border-left: 0;
    }
    dialog.editor {
      --editor-font: 16px;
    }
  }
</style>
