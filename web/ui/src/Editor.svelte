<script>
  import { untrack } from 'svelte'
  import { ApiError, formatSize } from './api.js'
  import Dialog from './Dialog.svelte'
  import Icon from './Icon.svelte'

  // node is the file as it was opened; after a save it is the new version.
  let { node: opened, onclose, onsaved } = $props()

  const OPEN_LIMIT = 5 << 20
  const WRAP_KEY = 'telex-editor-wrap'

  let node = $state(opened)
  let dialog = $state()
  let host = $state()
  let editor = null
  let lib = null
  let error = $state('')
  let loading = $state(true)
  let dirty = $state(false)
  let saving = $state(false)
  let savedAt = $state(0)
  let ask = $state(null) // 'close' or { conflict, current }
  let preview = $state(false)
  let previewHtml = $state('')
  let wrap = $state(readWrap())
  let bom = false
  let lineBreak = '\n'

  const isMarkdown = $derived(/\.(md|markdown)$/i.test(node.name))

  function readWrap() {
    try {
      return localStorage.getItem(WRAP_KEY) !== '0'
    } catch {
      return true
    }
  }

  // Runs once: load reads node, and a save must not reload the editor.
  $effect(() => {
    dialog.showModal()
    untrack(load)
    const leave = (e) => {
      if (dirty) e.preventDefault()
    }
    addEventListener('beforeunload', leave)
    return () => {
      removeEventListener('beforeunload', leave)
      editor?.destroy()
    }
  })

  async function fetchText(id) {
    const res = await fetch(`/api/nodes/${id}/content`, { headers: { 'X-Telex': '1' } })
    if (!res.ok) {
      let msg = `Fehler ${res.status}`
      try {
        msg = (await res.json()).error ?? msg
      } catch {}
      throw new Error(msg)
    }
    const bytes = new Uint8Array(await res.arrayBuffer())
    bom = bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf
    let text
    try {
      text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
    } catch {
      throw new Error('Die Datei ist kein Text in UTF-8 und lässt sich hier nicht bearbeiten.')
    }
    lineBreak = text.includes('\r\n') ? '\r\n' : '\n'
    return text
  }

  async function load() {
    if (node.size > OPEN_LIMIT) {
      error = `Die Datei ist ${formatSize(node.size)} groß. Der Editor öffnet Dateien bis 5 MB.`
      loading = false
      return
    }
    try {
      const [text, mod] = await Promise.all([fetchText(node.id), lib ?? import('./codemirror.js')])
      lib = mod
      editor?.destroy()
      editor = lib.createEditor(host, {
        text,
        name: node.name,
        lineBreak,
        wrap,
        onchange: () => {
          dirty = true
          if (preview) renderPreview()
        },
        onsave: () => save(),
      })
      dirty = false
      savedAt = 0
      if (preview) renderPreview()
      editor.focus()
    } catch (err) {
      error = err.message
    }
    loading = false
  }

  function renderPreview() {
    if (lib && editor) previewHtml = lib.renderMarkdown(editor.text())
  }

  async function save(force = false) {
    if (!editor || saving) return
    saving = true
    error = ''
    try {
      const text = (bom ? '﻿' : '') + editor.text()
      const res = await fetch(`/api/nodes/${node.id}/content${force ? '?force=1' : ''}`, {
        method: 'PUT',
        headers: { 'X-Telex': '1', 'Content-Type': 'text/plain; charset=utf-8' },
        body: text,
      })
      const data = await res.json().catch(() => null)
      if (res.status === 409 && !force && data?.current !== undefined) {
        ask = { conflict: data.error, current: data.current }
        return
      }
      if (!res.ok) throw new ApiError(data?.error ?? `Fehler ${res.status}`, res.status)
      node = data
      dirty = false
      savedAt = Date.now()
      onsaved?.(node)
      if (ask === 'close') onclose()
    } catch (err) {
      error = err.status === 423 ? 'Der Tresor ist gesperrt. Entsperre ihn und speichere dann noch einmal.' : err.message
    } finally {
      saving = false
    }
  }

  async function reloadCurrent(id) {
    ask = null
    loading = true
    error = ''
    try {
      const res = await fetch(`/api/nodes/${id}`, { headers: { 'X-Telex': '1' } })
      if (!res.ok) throw new Error('Die aktuelle Version lässt sich nicht laden.')
      const info = await res.json()
      node = info.node ?? info
    } catch (err) {
      error = err.message
      loading = false
      return
    }
    await load()
  }

  function close() {
    if (dirty) ask = 'close'
    else onclose()
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

  const status = $derived(
    saving ? 'Wird gespeichert …' : dirty ? 'Nicht gespeichert' : savedAt ? 'Gespeichert' : formatSize(node.size),
  )
</script>

<dialog bind:this={dialog} class="editor" aria-label={`${node.name} bearbeiten`} oncancel={(e) => { e.preventDefault(); close() }}>
  <header>
    <button class="icon" aria-label="Schließen" title="Schließen" onclick={close}><Icon name="close" size={16} /></button>
    <div class="title">
      <h2>{node.name}</h2>
      <span class:warn={dirty}>{status}</span>
    </div>
    {#if isMarkdown}
      <button class="tool" class:on={preview} aria-pressed={preview} onclick={togglePreview} title="Markdown-Vorschau">
        <Icon name="info" /><span>Vorschau</span>
      </button>
    {/if}
    <button class="tool" class:on={wrap} aria-pressed={wrap} onclick={toggleWrap} title="Lange Zeilen umbrechen">
      <Icon name="list" /><span>Umbruch</span>
    </button>
    <button class="primary" disabled={!dirty || saving || loading} onclick={() => save()} title="Speichern (Strg+S)">Speichern</button>
  </header>
  {#if error}<p class="err" role="alert">{error}</p>{/if}
  <div class="panes" class:split={preview}>
    <div class="code" bind:this={host} class:hidden={loading}></div>
    {#if loading}<p class="hint">Wird geladen …</p>{/if}
    {#if preview}
      <!-- markdown-it runs with html: false, so this holds no markup from the file. -->
      <article class="md">{@html previewHtml}</article>
    {/if}
  </div>
</dialog>

{#if ask === 'close'}
  <Dialog title="Änderungen speichern?" onclose={() => (ask = null)}>
    <p>„{node.name}“ hat Änderungen, die noch nicht gespeichert sind.</p>
    <div class="actions">
      <button class="primary" disabled={saving} onclick={() => save()}>Speichern</button>
      <button class="secondary" onclick={() => { dirty = false; onclose() }}>Verwerfen</button>
      <button class="secondary" onclick={() => (ask = null)}>Abbrechen</button>
    </div>
  </Dialog>
{:else if ask?.conflict}
  <Dialog title="Datei wurde geändert" onclose={() => (ask = null)}>
    <p>{ask.conflict} Deine Fassung ist noch nicht gespeichert.</p>
    <p>Beim Überschreiben wandert die andere Fassung in den Papierkorb.</p>
    <div class="actions">
      <button class="primary" onclick={() => { ask = null; save(true) }}>Überschreiben</button>
      <button class="secondary" onclick={() => reloadCurrent(ask.current)}>Andere Fassung laden</button>
      <button class="secondary" onclick={() => (ask = null)}>Abbrechen</button>
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
  dialog.editor[open] {
    display: flex;
    flex-direction: column;
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
    margin-left: -1.3em;
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
