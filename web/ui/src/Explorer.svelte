<script>
  import { api, formatDate, formatSize, kindOf, post, ROOT, stateLabel, typeLabel } from './api.js'
  import { addDropped, addFolderInput, cancel as cancelUpload, enqueue, resume, retry as retryUpload, syncJobs, uploads } from './uploads.svelte.js'
  import Dialog from './Dialog.svelte'
  import FileIcon from './FileIcon.svelte'
  import Icon from './Icon.svelte'
  import Logs from './Logs.svelte'
  import Preview from './Preview.svelte'
  import Properties from './Properties.svelte'
  import Settings from './Settings.svelte'
  import ShareDialog from './ShareDialog.svelte'
  import Shares from './Shares.svelte'
  import SpeedControl from './SpeedControl.svelte'
  import TreeNode from './TreeNode.svelte'
  import UploadPanel from './UploadPanel.svelte'

  let { app, onlogout } = $props()

  const COLUMNS = [
    { key: 'mtime', label: 'Änderungsdatum', width: 150, value: (n) => (mode === 'trash' ? n.trashedAt : n.mtime), text: (n) => formatDate(mode === 'trash' ? n.trashedAt : n.mtime) },
    { key: 'ctime', label: 'Erstellt', width: 150, value: (n) => n.ctime, text: (n) => formatDate(n.ctime) },
    { key: 'type', label: 'Typ', width: 125, value: typeLabel, text: typeLabel },
    { key: 'size', label: 'Größe', width: 95, right: true, value: (n) => n.size, text: (n) => formatSize(n.size) },
    { key: 'enc', label: 'Verschlüsselung', width: 130, value: (n) => (n.isDir ? '' : n.encrypted ? 'a' : 'b'), text: (n) => (n.isDir ? '' : n.encrypted ? 'Verschlüsselt' : 'Unverschlüsselt') },
    { key: 'share', label: 'Freigabe', width: 90, value: (n) => (n.shared ? 0 : 1), text: (n) => (n.shared ? 'Geteilt' : '') },
    { key: 'state', label: 'Zustand', width: 180, value: stateLabel, text: stateLabel },
    { key: 'files', label: 'Dateien', width: 80, right: true, value: (n) => n.fileCount ?? 0, text: (n) => (n.isDir ? String(n.fileCount ?? 0) : '') },
    { key: 'segments', label: 'Segmente', width: 90, right: true, value: (n) => n.segments, text: (n) => String(n.segments) },
    { key: 'md5', label: 'MD5', width: 290, mono: true, value: (n) => n.md5 ?? '', text: (n) => n.md5 ?? '' },
    { key: 'sha256', label: 'SHA-256', width: 470, mono: true, value: (n) => n.sha256 ?? '', text: (n) => n.sha256 ?? '' },
  ]
  const DEFAULT_COLUMNS = ['mtime', 'type', 'size', 'enc', 'state']
  const NAME_WIDTH = 260

  function readColumns() {
    try {
      const saved = JSON.parse(localStorage.getItem('telex-columns'))
      if (Array.isArray(saved?.visible)) return { visible: saved.visible, widths: saved.widths ?? {} }
    } catch {}
    return { visible: DEFAULT_COLUMNS, widths: {} }
  }

  let columnPrefs = $state(readColumns())
  let windowWidth = $state(innerWidth)
  let verify = $state(null)
  let stats = $state(null)

  function saveColumns() {
    try {
      localStorage.setItem('telex-columns', JSON.stringify(columnPrefs))
    } catch {}
  }

  function toggleColumn(key) {
    const on = columnPrefs.visible.includes(key)
    columnPrefs.visible = on ? columnPrefs.visible.filter((k) => k !== key) : COLUMNS.map((c) => c.key).filter((k) => k === key || columnPrefs.visible.includes(k))
    saveColumns()
  }

  const widthOf = (col) => columnPrefs.widths[col.key] ?? col.width
  const shownColumns = $derived(windowWidth < 760 ? COLUMNS.filter((c) => c.key === 'size') : COLUMNS.filter((c) => columnPrefs.visible.includes(c.key)))
  const tableWidth = $derived(shownColumns.reduce((sum, c) => sum + widthOf(c), NAME_WIDTH))

  function startResize(e, col) {
    e.preventDefault()
    e.stopPropagation()
    const startX = e.clientX
    const startWidth = widthOf(col)
    const move = (ev) => (columnPrefs.widths[col.key] = Math.max(50, startWidth + ev.clientX - startX))
    const up = () => {
      removeEventListener('pointermove', move)
      removeEventListener('pointerup', up)
      saveColumns()
    }
    addEventListener('pointermove', move)
    addEventListener('pointerup', up)
  }

  function showColumnMenu(e) {
    e.preventDefault()
    e.stopPropagation()
    const height = COLUMNS.length * 32 + 12
    menu = {
      x: Math.min(e.clientX, innerWidth - 258),
      y: Math.min(e.clientY, innerHeight - height - 8),
      keep: true,
      entries: COLUMNS.map((c) => ({ label: c.label, checked: () => columnPrefs.visible.includes(c.key), action: () => toggleColumn(c.key) })),
    }
  }

  let mode = $state('files') // files | search | trash | shares | logs
  let currentId = $state(ROOT)
  let path = $state([])
  let items = $state([])
  let unfinished = $state([])
  let resumeTarget = null
  let selected = $state(new Set())
  let anchor = $state(null)
  let back = $state([])
  let forward = $state([])
  let view = $state(readView())
  let sort = $state({ key: 'name', dir: 1 })
  let query = $state('')
  let encrypt = $state(true)
  let clipboard = $state([])
  let treeVersion = $state(0)
  let menu = $state(null)
  let modal = $state(null)
  let error = $state('')
  let dragging = $state(false)
  let navOpen = $state(false)
  let uploadMenu = $state(false)
  let jobs = $state([])
  let fileInput = $state()
  let resumeInput = $state()
  let folderInput = $state()
  let rootNode = $state(null)

  function readView() {
    try {
      return localStorage.getItem('telex-view') === 'icons' ? 'icons' : 'details'
    } catch {
      return 'details'
    }
  }

  function setView(v) {
    view = v
    try {
      localStorage.setItem('telex-view', v)
    } catch {}
  }

  const sorted = $derived.by(() => {
    const value = COLUMNS.find((c) => c.key === sort.key)?.value ?? ((n) => n.name.toLocaleLowerCase('de'))
    return [...items].sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      const [x, y] = [value(a), value(b)]
      return (x < y ? -1 : x > y ? 1 : 0) * sort.dir
    })
  })

  // Unfinished uploads: the ones running here, and the ones only the server
  // still knows about, for example after the browser was closed.
  const unfinishedRows = $derived.by(() => {
    if (mode !== 'files') return []
    const local = uploads.items.filter((i) => i.parentId === currentId && ['queued', 'running', 'error'].includes(i.state))
    const ids = new Set(local.map((i) => i.id))
    return [
      ...local.map((item) => ({ key: `local-${item.key}`, name: item.name, size: item.file.size, received: item.sent, encrypted: item.encrypted, item })),
      ...unfinished.filter((u) => u.parentId === currentId && !ids.has(u.id)).map((upload) => ({ ...upload, key: upload.id, upload })),
    ]
  })

  const selection = $derived(sorted.filter((n) => selected.has(n.id)))
  const single = $derived(selection.length === 1 ? selection[0] : null)
  const canEdit = $derived(mode === 'files' || mode === 'search')
  const totalSize = $derived(selection.reduce((sum, n) => sum + n.size, 0))

  async function guard(fn) {
    try {
      error = ''
      return await fn()
    } catch (err) {
      error = err.message
    }
  }

  // Only the newest load counts, an older answer may arrive after it.
  let loads = 0
  async function load() {
    const run = ++loads
    await guard(async () => {
      if (mode === 'trash') {
        items = await api('/trash')
      } else if (mode === 'search') {
        items = await api('/search?q=' + encodeURIComponent(query))
      } else if (mode === 'files') {
        const [info, list, open] = await Promise.all([api(`/nodes/${currentId}`), api(`/nodes/${currentId}/children`), api(`/nodes/${currentId}/uploads`)])
        if (run !== loads) return
        path = info.path
        items = list
        unfinished = open
        encrypt = info.encryptDefault
        if (currentId === ROOT) rootNode = info.node
      }
    })
    const alive = new Set(items.map((n) => n.id))
    selected = new Set([...selected].filter((id) => alive.has(id)))
  }

  function go(id, remember = true) {
    if (remember && (mode !== 'files' || id !== currentId)) {
      back = [...back, currentId]
      forward = []
    }
    currentId = id
    mode = 'files'
    query = ''
    selected = new Set()
    navOpen = false
    load()
  }

  function goBack() {
    const id = back.at(-1)
    back = back.slice(0, -1)
    forward = [...forward, currentId]
    go(id, false)
  }

  function goForward() {
    const id = forward.at(-1)
    forward = forward.slice(0, -1)
    back = [...back, currentId]
    go(id, false)
  }

  function goUp() {
    if (path.length > 1) go(path.at(-2).id)
  }

  function setMode(next) {
    mode = next
    selected = new Set()
    navOpen = false
    load()
  }

  let searchTimer
  function onSearch() {
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => {
      if (query.trim()) {
        mode = 'search'
        selected = new Set()
        load()
      } else if (mode === 'search') {
        mode = 'files'
        load()
      }
    }, 200)
  }

  function refreshAll() {
    treeVersion++
    return load()
  }

  function open(node) {
    if (mode === 'trash') return
    if (node.isDir) {
      go(node.id)
    } else if (kindOf(node) === 'pdf') {
      window.open(`/api/nodes/${node.id}/content?inline=1`, '_blank', 'noopener')
    } else {
      modal = { type: 'preview', node }
    }
  }

  function clickItem(e, node) {
    menu = null
    if (e.pointerType === 'touch' && !e.ctrlKey && selected.size === 0) {
      open(node)
      return
    }
    if (e.shiftKey && anchor !== null) {
      const from = sorted.findIndex((n) => n.id === anchor)
      const to = sorted.findIndex((n) => n.id === node.id)
      const [a, b] = from < to ? [from, to] : [to, from]
      selected = new Set(sorted.slice(a, b + 1).map((n) => n.id))
      return
    }
    if (e.ctrlKey || e.metaKey) {
      const next = new Set(selected)
      next.has(node.id) ? next.delete(node.id) : next.add(node.id)
      selected = next
    } else {
      selected = new Set([node.id])
    }
    anchor = node.id
  }

  function sortBy(key) {
    sort = { key, dir: sort.key === key ? -sort.dir : 1 }
  }

  function download(nodes = selection) {
    if (nodes.length === 0) return
    const a = document.createElement('a')
    a.href = nodes.length === 1 && !nodes[0].isDir
      ? `/api/nodes/${nodes[0].id}/content`
      : `/api/zip?ids=${nodes.map((n) => n.id).join(',')}`
    a.rel = 'noopener'
    a.click()
  }

  function ask(title, label, value, action) {
    modal = { type: 'prompt', title, label, value, action }
  }

  function newFolder(parent = currentId) {
    ask('Neuer Ordner', 'Name', 'Neuer Ordner', async (name) => {
      await post(`/nodes/${parent}/mkdir`, { name })
      await refreshAll()
    })
  }

  async function verifyNodes(ids, deep = false) {
    await guard(async () => {
      await post('/verify', { ids, deep })
      await poll()
    })
  }

  async function endShare(node) {
    await guard(async () => {
      await api(`/nodes/${node.id}/shares`, { method: 'DELETE' })
      await refreshAll()
    })
  }

  function rename(node = single) {
    if (!node) return
    ask('Umbenennen', 'Neuer Name', node.name, async (name) => {
      await api(`/nodes/${node.id}`, { method: 'PATCH', body: { name } })
      await refreshAll()
    })
  }

  async function remove(nodes = selection) {
    if (nodes.length === 0) return
    await guard(async () => {
      await post('/delete', { ids: nodes.map((n) => n.id) })
      await refreshAll()
    })
  }

  function cut() {
    clipboard = selection.map((n) => n.id)
  }

  async function moveTo(ids, target) {
    await guard(async () => {
      await post('/move', { ids, target })
      clipboard = []
      await refreshAll()
    })
  }

  async function paste() {
    if (clipboard.length && mode === 'files') await moveTo(clipboard, currentId)
  }

  async function restore(nodes = selection) {
    await guard(async () => {
      await post('/trash/restore', { ids: nodes.map((n) => n.id) })
      await refreshAll()
    })
  }

  function purge(nodes = selection) {
    if (nodes.length === 0) return
    modal = {
      type: 'confirm',
      title: 'Endgültig löschen',
      text: nodes.length === 1 ? `„${nodes[0].name}“ wird unwiderruflich gelöscht, auch bei Telegram.` : `${nodes.length} Einträge werden unwiderruflich gelöscht, auch bei Telegram.`,
      action: async () => {
        await post('/trash/purge', { ids: nodes.map((n) => n.id) })
        await refreshAll()
      },
    }
  }

  async function logout() {
    await post('/logout')
    onlogout()
  }

  function showMenu(e, node) {
    e.preventDefault()
    e.stopPropagation()
    if (node && !selected.has(node.id)) selected = new Set([node.id])
    const entries = []
    if (mode === 'trash') {
      if (node) {
        entries.push({ label: 'Wiederherstellen', icon: 'restore', action: () => restore() })
        entries.push({ label: 'Endgültig löschen', icon: 'trash', danger: true, action: () => purge() })
      }
    } else if (node) {
      const many = selected.size > 1
      if (!many) entries.push({ label: 'Öffnen', action: () => open(node), bold: true })
      entries.push({ label: many || node.isDir ? 'Als ZIP herunterladen' : 'Herunterladen', icon: 'download', action: () => download() })
      entries.push({ label: many ? 'Auswahl prüfen' : node.isDir ? 'Ordner prüfen' : 'Datei prüfen', icon: 'check', action: () => verifyNodes(selection.map((n) => n.id)) })
      entries.push({ label: 'Inhalt prüfen (lädt herunter)', icon: 'download', action: () => verifyNodes(selection.map((n) => n.id), true) })
      if (!many) entries.push({ label: 'Teilen', icon: 'share', action: () => (modal = { type: 'share', node }) })
      if (!many && node.shared) entries.push({ label: 'Freigabe beenden', icon: 'link', action: () => endShare(node) })
      entries.push({ sep: true })
      entries.push({ label: 'Ausschneiden', icon: 'cut', action: cut })
      if (!many) entries.push({ label: 'Umbenennen', icon: 'rename', action: () => rename(node) })
      entries.push({ label: 'Löschen', icon: 'trash', danger: true, action: () => remove() })
      if (!many) {
        entries.push({ sep: true })
        entries.push({ label: 'Eigenschaften', icon: 'info', action: () => (modal = { type: 'props', node }) })
      }
    } else if (mode === 'files') {
      entries.push({ label: 'Neuer Ordner', icon: 'plus', action: () => newFolder() })
      entries.push({ label: 'Dateien hochladen', icon: 'upload', action: () => fileInput.click() })
      if (clipboard.length) entries.push({ label: `Einfügen (${clipboard.length})`, icon: 'paste', action: paste })
      entries.push({ sep: true })
      entries.push({ label: 'Ordner prüfen', icon: 'check', action: () => verifyNodes([currentId]) })
      entries.push({ label: 'Inhalt prüfen (lädt herunter)', icon: 'download', action: () => verifyNodes([currentId], true) })
      entries.push({ label: 'Aktualisieren', icon: 'refresh', action: refreshAll })
    }
    placeMenu(e, entries)
  }

  function placeMenu(e, entries) {
    if (entries.length === 0) return
    const height = entries.reduce((h, i) => h + (i.sep ? 9 : 32), 12)
    menu = {
      x: Math.min(e.clientX, innerWidth - 258),
      y: Math.max(8, Math.min(e.clientY, innerHeight - height - 8)),
      entries,
    }
  }

  function treeMenu(e, node) {
    e.preventDefault()
    e.stopPropagation()
    const root = node.id === ROOT
    const entries = [
      { label: 'Öffnen', bold: true, action: () => go(node.id) },
      { label: 'Neuer Ordner', icon: 'plus', action: () => newFolder(node.id) },
      { label: 'Als ZIP herunterladen', icon: 'download', action: () => download([node]) },
      { label: 'Ordner prüfen', icon: 'check', action: () => verifyNodes([node.id]) },
      { label: 'Inhalt prüfen (lädt herunter)', icon: 'download', action: () => verifyNodes([node.id], true) },
    ]
    if (!root) {
      entries.push({ label: 'Teilen', icon: 'share', action: () => (modal = { type: 'share', node }) })
      if (node.shared) entries.push({ label: 'Freigabe beenden', icon: 'link', action: () => endShare(node) })
      entries.push({ sep: true })
      entries.push({ label: 'Umbenennen', icon: 'rename', action: () => rename(node) })
      entries.push({ label: 'Löschen', icon: 'trash', danger: true, action: () => remove([node]) })
    }
    entries.push({ sep: true })
    entries.push({ label: 'Eigenschaften', icon: 'info', action: () => (modal = { type: 'props', node }) })
    placeMenu(e, entries)
  }

  function navMenu(e) {
    e.preventDefault()
    e.stopPropagation()
    placeMenu(e, [
      { label: 'Neuer Ordner', icon: 'plus', action: () => newFolder(ROOT) },
      { label: 'Aktualisieren', icon: 'refresh', action: refreshAll },
    ])
  }

  function onKey(e) {
    if (modal || e.target.closest('input, select, textarea, dialog')) return
    const ctrl = e.ctrlKey || e.metaKey
    if (e.key === 'Delete') {
      mode === 'trash' ? purge() : canEdit && remove()
    } else if (e.key === 'F2' && canEdit) {
      e.preventDefault()
      rename()
    } else if (e.key === 'Enter' && single) {
      open(single)
    } else if (e.key === 'Backspace' && mode === 'files') {
      goUp()
    } else if (e.key === 'F5') {
      e.preventDefault()
      refreshAll()
    } else if (ctrl && e.key === 'a') {
      e.preventDefault()
      selected = new Set(items.map((n) => n.id))
    } else if (ctrl && e.key === 'x' && canEdit) {
      cut()
    } else if (ctrl && e.key === 'v') {
      paste()
    } else if (e.key === 'Escape') {
      selected = new Set()
      clipboard = []
      menu = null
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (sorted.length === 0) return
      e.preventDefault()
      const at = sorted.findIndex((n) => n.id === anchor)
      const next = sorted[Math.max(0, Math.min(sorted.length - 1, at + (e.key === 'ArrowDown' ? 1 : -1)))]
      selected = new Set([next.id])
      anchor = next.id
      document.getElementById(`item-${next.id}`)?.scrollIntoView({ block: 'nearest' })
    }
  }

  function onDragStart(e, node) {
    const ids = selected.has(node.id) ? [...selected] : [node.id]
    e.dataTransfer.setData('application/x-telex-nodes', JSON.stringify(ids))
    e.dataTransfer.effectAllowed = 'move'
  }

  function hasFiles(e) {
    return [...e.dataTransfer.types].includes('Files')
  }

  async function onDrop(e) {
    dragging = false
    if (!hasFiles(e) || mode !== 'files') return
    e.preventDefault()
    await guard(() => addDropped(e.dataTransfer, currentId, encrypt))
    await refreshAll()
  }

  function dropOnFolder(e, node) {
    const raw = e.dataTransfer.getData('application/x-telex-nodes')
    if (!raw || !node.isDir) return
    e.preventDefault()
    e.stopPropagation()
    const ids = JSON.parse(raw).filter((id) => id !== node.id)
    if (ids.length) moveTo(ids, node.id)
  }

  function pickFiles(e) {
    for (const f of e.target.files) enqueue(f, currentId, encrypt)
    e.target.value = ''
  }

  function pickResume(e) {
    const [file] = e.target.files
    e.target.value = ''
    if (!file) return
    try {
      error = ''
      resume(file, resumeTarget)
    } catch (err) {
      error = err.message
    }
  }

  function chooseResume(upload) {
    resumeTarget = upload
    resumeInput.click()
  }

  async function discardUpload(row) {
    if (!confirm(`Unfertigen Upload „${row.name}“ verwerfen?`)) return
    await guard(async () => {
      if (row.item) await cancelUpload(row.item)
      else await api(`/uploads/${row.upload.id}`, { method: 'DELETE' })
      await load()
    })
  }

  const rowState = (row) => {
    const part = `${formatSize(row.received)} von ${formatSize(row.size)}`
    const pct = row.size ? Math.floor((row.received / row.size) * 100) : 0
    if (!row.item) return `Unvollständig, ${pct} %, ${part}`
    if (row.item.state === 'queued') return `Wartet, ${part}`
    if (row.item.state === 'error') return `Fehlgeschlagen bei ${pct} %, ${part}`
    return `Wird hochgeladen, ${pct} %, ${part}`
  }

  async function pickFolder(e) {
    const list = [...e.target.files]
    e.target.value = ''
    await guard(() => addFolderInput(list, currentId, encrypt))
    await refreshAll()
  }

  async function submitModal(value) {
    const m = modal
    modal = { ...m, busy: true, error: '' }
    try {
      await m.action(value)
      modal = null
    } catch (err) {
      modal = { ...m, busy: false, error: err.message, value }
    }
  }

  // Conflicts of parallel uploads queue up, so only one question shows at a time.
  let conflictChain = Promise.resolve()
  uploads.resolveConflict = (name) => {
    const answer = conflictChain.then(() => new Promise((resolve) => (modal = { type: 'conflict', name, resolve })))
    conflictChain = answer.catch(() => {})
    return answer
  }
  // Also for uploads into subfolders: the sizes shown here include them.
  uploads.onDone = () => {
    if (mode === 'files') load()
  }

  let lastJobs = 0
  let lastVerified = 0
  async function poll() {
    const folder = currentId
    const [list, check, st, open] = await Promise.all([api('/jobs'), api('/verify'), api('/stats'), api(`/nodes/${folder}/uploads`)].map((p) => p.catch(() => null)))
    if (st) stats = st
    if (open && folder === currentId) unfinished = open
    if (check) {
      verify = check
      if (check.done !== lastVerified) load()
      lastVerified = check.done
    }
    if (!list) return
    jobs = list
    syncJobs(list)
    if (list.length !== lastJobs) load()
    lastJobs = list.length
  }

  $effect(() => {
    load()
    poll()
    const timer = setInterval(poll, 2000)
    return () => clearInterval(timer)
  })

  const pendingTotal = $derived(uploads.items.filter((i) => ['queued', 'running', 'server'].includes(i.state)).length)
</script>

<svelte:window bind:innerWidth={windowWidth} onkeydown={onKey} onclick={() => { menu = null; uploadMenu = false }} />

{#snippet uploadActions(row)}
  <span class="tmp-actions">
    {#if row.item?.state === 'error'}<button class="secondary" onclick={() => retryUpload(row.item)}>Wiederholen</button>{/if}
    {#if !row.item}<button class="secondary" title="Die Datei noch einmal auswählen, der Upload läuft dort weiter" onclick={() => chooseResume(row.upload)}>Fortsetzen</button>{/if}
    <button class="secondary" onclick={() => discardUpload(row)}>Verwerfen</button>
  </span>
{/snippet}

<input type="file" hidden bind:this={resumeInput} onchange={pickResume} />
<input type="file" multiple hidden bind:this={fileInput} onchange={pickFiles} />
<input type="file" webkitdirectory hidden bind:this={folderInput} onchange={pickFolder} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="app" oncontextmenu={(e) => { if (!e.target.closest('input, textarea')) e.preventDefault() }}>
  <div class="bar toolbar" role="toolbar" aria-label="Aktionen">
    <button class="icon only-narrow" aria-label="Navigation" onclick={(e) => { e.stopPropagation(); navOpen = !navOpen }}><Icon name="menu" /></button>
    {#if mode === 'trash'}
      <button class="tool" disabled={!selection.length} onclick={() => restore()}><Icon name="restore" /><span>Wiederherstellen</span></button>
      <button class="tool" disabled={!selection.length} onclick={() => purge()}><Icon name="trash" /><span>Endgültig löschen</span></button>
      <button class="tool" disabled={!items.length} onclick={() => purge(items)}><Icon name="trash" /><span>Papierkorb leeren</span></button>
    {:else if mode === 'files' || mode === 'search'}
      <button class="tool" aria-label="Neuer Ordner" title="Neuer Ordner" disabled={mode !== 'files'} onclick={() => newFolder()}><Icon name="plus" /><span>Neuer Ordner</span></button>
      <div class="split">
        <button class="tool" aria-label="Hochladen" title="Hochladen" disabled={mode !== 'files'} onclick={(e) => { e.stopPropagation(); uploadMenu = !uploadMenu }} aria-haspopup="menu" aria-expanded={uploadMenu}>
          <Icon name="upload" /><span>Hochladen</span>
        </button>
        {#if uploadMenu}
          <div class="menu drop" role="menu">
            <button role="menuitem" onclick={() => fileInput.click()}>Dateien auswählen</button>
            <button role="menuitem" onclick={() => folderInput.click()}>Ordner auswählen</button>
          </div>
        {/if}
      </div>
      <button
        class="tool mode"
        class:plain={!encrypt}
        disabled={mode !== 'files'}
        title="{encrypt ? 'Verschlüsselt' : 'Unverschlüsselt'}: gilt für den nächsten Upload in diesem Ordner"
        aria-label="Uploads verschlüsseln"
        aria-pressed={encrypt}
        onclick={() => (encrypt = !encrypt)}
      >
        <Icon name={encrypt ? 'lock' : 'unlock'} /><span>{encrypt ? 'Verschlüsselt' : 'Unverschlüsselt'}</span>
      </button>
      <i class="sep"></i>
      <button class="tool" aria-label="Herunterladen" title="Herunterladen" disabled={!selection.length} onclick={() => download()}><Icon name="download" /></button>
      <button class="tool" aria-label="Teilen" title="Teilen" disabled={!single} onclick={() => (modal = { type: 'share', node: single })}><Icon name="share" /></button>
      <button class="tool" aria-label="Umbenennen" title="Umbenennen (F2)" disabled={!single} onclick={() => rename()}><Icon name="rename" /></button>
      <button class="tool" aria-label="Ausschneiden" title="Ausschneiden (Strg+X)" disabled={!selection.length} onclick={cut}><Icon name="cut" /></button>
      <button class="tool" aria-label="Einfügen" title="Einfügen (Strg+V)" disabled={!clipboard.length || mode !== 'files'} onclick={paste}><Icon name="paste" /></button>
      <button class="tool" aria-label="Löschen" title="Löschen (Entf)" disabled={!selection.length} onclick={() => remove()}><Icon name="trash" /></button>
      <i class="sep"></i>
      <button class="tool" aria-label="Ordner prüfen" title="Prüft ohne Download, ob alle Dateien in diesem Ordner noch bei Telegram liegen" disabled={mode !== 'files'} onclick={() => verifyNodes([currentId])}><Icon name="check" /><span>Ordner prüfen</span></button>
    {/if}
    <span class="grow"></span>
    {#if (mode === 'files' || mode === 'search' || mode === 'trash') && view === 'details' && windowWidth >= 760}
      <button class="tool" aria-label="Spalten" title="Spalten auswählen" onclick={showColumnMenu}><Icon name="columns" /><span>Spalten</span></button>
    {/if}
    {#if mode !== 'shares' && mode !== 'logs'}
      <button class="tool" aria-label="Ansicht wechseln" title={view === 'details' ? 'Große Symbole' : 'Details'} onclick={() => setView(view === 'details' ? 'icons' : 'details')}>
        <Icon name={view === 'details' ? 'grid' : 'list'} />
      </button>
    {/if}
  </div>

  <div class="bar address">
    <button class="icon" aria-label="Zurück" disabled={!back.length} onclick={goBack}><Icon name="back" /></button>
    <button class="icon" aria-label="Vorwärts" disabled={!forward.length} onclick={goForward}><Icon name="forward" /></button>
    <button class="icon" aria-label="Übergeordneter Ordner" disabled={mode !== 'files' || path.length < 2} onclick={goUp}><Icon name="up" /></button>
    <nav class="crumbs" aria-label="Pfad">
      {#if mode === 'trash'}
        <span class="crumb static">Papierkorb</span>
      {:else if mode === 'shares'}
        <span class="crumb static">Freigaben</span>
      {:else if mode === 'logs'}
        <span class="crumb static">Protokoll</span>
      {:else if mode === 'search'}
        <span class="crumb static">Suchergebnisse für „{query}“</span>
      {:else}
        {#each path as part, i (part.id)}
          {#if i > 0}<Icon name="chevron" size={10} />{/if}
          <button class="crumb" onclick={() => go(part.id)}>{part.name || 'Dieser Speicher'}</button>
        {/each}
      {/if}
    </nav>
    <button class="icon" aria-label="Aktualisieren" onclick={refreshAll}><Icon name="refresh" /></button>
    <label class="search">
      <Icon name="search" size={14} />
      <input type="search" placeholder="Suchen" aria-label="Suchen" bind:value={query} oninput={onSearch} />
    </label>
  </div>

  {#if error}
    <div class="error" role="alert">
      <span>{error}</span>
      <button class="icon" aria-label="Meldung schließen" onclick={() => (error = '')}><Icon name="close" size={12} /></button>
    </div>
  {/if}

  <div class="body">
    <aside class="nav" class:open={navOpen} aria-label="Navigation" oncontextmenu={navMenu}>
      <div class="tree" role="tree" aria-label="Ordner">
        {#if rootNode}
          <TreeNode node={rootNode} label="Dieser Speicher" currentId={mode === 'files' ? currentId : 0} version={treeVersion} onnavigate={go} ondropnodes={moveTo} oncontext={treeMenu} startOpen />
        {/if}
      </div>
      <div class="links">
        <button class:active={mode === 'shares'} onclick={() => setMode('shares')}><Icon name="link" /><span>Freigaben</span></button>
        <button class:active={mode === 'trash'} onclick={() => setMode('trash')}><Icon name="trash" /><span>Papierkorb</span></button>
        <button class:active={mode === 'logs'} onclick={() => setMode('logs')}><Icon name="list" /><span>Protokoll</span></button>
        <button onclick={() => (modal = { type: 'settings' })}><Icon name="settings" /><span>Einstellungen</span></button>
        <button onclick={logout}><Icon name="logout" /><span>Abmelden</span></button>
      </div>
    </aside>

    <main
      class="pane"
      class:dragging
      aria-label="Dateien"
      oncontextmenu={(e) => showMenu(e, null)}
      onclick={(e) => { if (e.target === e.currentTarget || e.target.classList.contains('grid')) selected = new Set() }}
      ondragover={(e) => { if (hasFiles(e) && mode === 'files') { e.preventDefault(); dragging = true } }}
      ondragleave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) dragging = false }}
      ondrop={onDrop}
    >
      {#if mode === 'shares'}
        <Shares onerror={(m) => (error = m)} />
      {:else if mode === 'logs'}
        <Logs onerror={(m) => (error = m)} />
      {:else if items.length === 0 && unfinishedRows.length === 0}
        <div class="empty">
          {#if mode === 'trash'}
            <p>Der Papierkorb ist leer.</p>
          {:else if mode === 'search'}
            <p>Keine Treffer.</p>
          {:else}
            <p>Dieser Ordner ist leer.</p>
            <small>Dateien hierher ziehen oder über „Hochladen“ auswählen.</small>
          {/if}
        </div>
      {:else if view === 'details'}
        <table style="min-width:{tableWidth}px">
          <thead>
            <tr oncontextmenu={showColumnMenu}>
              <th><button onclick={() => sortBy('name')}>Name {sort.key === 'name' ? (sort.dir > 0 ? '▲' : '▼') : ''}</button></th>
              {#each shownColumns as col (col.key)}
                <th class:right={col.right} style="width:{widthOf(col)}px">
                  <button onclick={() => sortBy(col.key)}>{col.label} {sort.key === col.key ? (sort.dir > 0 ? '▲' : '▼') : ''}</button>
                  <!-- svelte-ignore a11y_no_static_element_interactions -->
                  <span class="grip" onpointerdown={(e) => startResize(e, col)}></span>
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each sorted as node (node.id)}
              <tr
                id="item-{node.id}"
                class:selected={selected.has(node.id)}
                class:cut={clipboard.includes(node.id)}
                draggable={canEdit}
                onclick={(e) => clickItem(e, node)}
                ondblclick={() => open(node)}
                oncontextmenu={(e) => showMenu(e, node)}
                ondragstart={(e) => onDragStart(e, node)}
                ondragover={(e) => { if (node.isDir && !hasFiles(e)) e.preventDefault() }}
                ondrop={(e) => dropOnFolder(e, node)}
              >
                <td class="c-name">
                  <div class="cell">
                    <FileIcon {node} /><span class="label">{node.name}</span>
                    {#if node.shared}<i class="shared" title="Wird geteilt"><Icon name="link" size={14} /></i>{/if}
                  </div>
                </td>
                {#each shownColumns as col (col.key)}
                  <td class:right={col.right} class:mono={col.mono} class:bad={col.key === 'state' && node.verifyStatus === 'bad'} title={col.text(node)}>{col.text(node)}</td>
                {/each}
              </tr>
            {/each}
            {#each unfinishedRows as row (row.key)}
              <tr class="tmp">
                <td class="c-name">
                  <div class="cell">
                    <FileIcon node={{ name: row.name, isDir: false, encrypted: row.encrypted }} /><span class="label">{row.name}<span class="ext">.tmp</span></span>
                  </div>
                </td>
                <td colspan={Math.max(1, shownColumns.length)}>
                  <div class="tmp-line">
                    <progress max={row.size || 1} value={row.received}></progress>
                    <span class="tmp-text" title={rowState(row)}>{rowState(row)}</span>
                    {@render uploadActions(row)}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <div class="grid">
          {#each sorted as node (node.id)}
            <button
              id="item-{node.id}"
              class="tile"
              class:selected={selected.has(node.id)}
              class:cut={clipboard.includes(node.id)}
              draggable={canEdit}
              onclick={(e) => clickItem(e, node)}
              ondblclick={() => open(node)}
              oncontextmenu={(e) => showMenu(e, node)}
              ondragstart={(e) => onDragStart(e, node)}
              ondragover={(e) => { if (node.isDir && !hasFiles(e)) e.preventDefault() }}
              ondrop={(e) => dropOnFolder(e, node)}
            >
              <FileIcon {node} size={56} />
              <span>{node.name}</span>
              {#if node.shared}<i class="shared corner" title="Wird geteilt"><Icon name="link" size={14} /></i>{/if}
            </button>
          {/each}
          {#each unfinishedRows as row (row.key)}
            <div class="tile tmp" title={rowState(row)}>
              <FileIcon node={{ name: row.name, isDir: false, encrypted: row.encrypted }} size={56} />
              <span>{row.name}<span class="ext">.tmp</span></span>
              <progress max={row.size || 1} value={row.received}></progress>
              {@render uploadActions(row)}
            </div>
          {/each}
        </div>
      {/if}
      {#if dragging}
        <div class="dropzone"><Icon name="upload" size={28} /><p>Hier ablegen, {encrypt ? 'wird verschlüsselt' : 'wird NICHT verschlüsselt'}</p></div>
      {/if}
    </main>
  </div>

  <footer class="bar status">
    <span>{items.length} {items.length === 1 ? 'Element' : 'Elemente'}</span>
    {#if selection.length}
      <span>{selection.length} ausgewählt{totalSize ? `, ${formatSize(totalSize)}` : ''}</span>
    {/if}
    <span class="grow"></span>
    {#if stats}
      <span
        class="stats"
        title="Ordner: {stats.folders} · Verschlüsselt: {stats.encrypted} · Unverschlüsselt: {stats.plain} · Nicht geprüft: {stats.unverified} · Aktive Freigaben: {stats.shares} · Bei Telegram belegt: {formatSize(stats.stored)} · Papierkorb: {stats.trashFiles} Dateien, {formatSize(stats.trashSize)}"
      >
        <span>{stats.files} Dateien</span>
        <span>{stats.segments} Segmente</span>
        <span>{formatSize(stats.size)}</span>
        <span>Freigaben: {stats.shares}</span>
        <span>Papierkorb: {formatSize(stats.trashSize)}</span>
        <span>Letzter Upload: {stats.lastUpload ? formatDate(stats.lastUpload) : 'noch keiner'}</span>
        {#if stats.corrupted}<span class="alarm">{stats.corrupted} beschädigt</span>{/if}
      </span>
    {/if}
    {#if stats?.speed}<SpeedControl speed={stats.speed} onchange={poll} onerror={(msg) => (error = msg)} />{/if}
    <span class="tg" title={app.telegram?.user}>{pendingTotal ? `${pendingTotal} Übertragung${pendingTotal === 1 ? '' : 'en'} aktiv` : 'Alles synchron'}</span>
  </footer>
</div>

<UploadPanel {verify} onverifychange={poll} />

{#if menu}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_interactive_supports_focus -->
  <div class="menu" role="menu" style="left:{menu.x}px;top:{menu.y}px" onclick={(e) => { if (menu.keep) e.stopPropagation() }}>
    {#each menu.entries as entry, i (i)}
      {#if entry.sep}
        <hr />
      {:else}
        <button role="menuitem" class:danger={entry.danger} class:bold={entry.bold} onclick={entry.action}>
          <span class="mi">
            {#if entry.checked}
              {#if entry.checked()}<Icon name="check" />{/if}
            {:else if entry.icon}
              <Icon name={entry.icon} />
            {/if}
          </span>{entry.label}
        </button>
      {/if}
    {/each}
  </div>
{/if}

{#if modal?.type === 'prompt'}
  <Dialog title={modal.title} onclose={() => (modal = null)}>
    <form class="form" onsubmit={(e) => { e.preventDefault(); submitModal(new FormData(e.target).get('value')) }}>
      <label>{modal.label}<input name="value" value={modal.value} required autofocus onfocus={(e) => e.target.select()} /></label>
      {#if modal.error}<p class="err" role="alert">{modal.error}</p>{/if}
      <div class="actions">
        <button type="submit" class="primary" disabled={modal.busy}>OK</button>
        <button type="button" class="secondary" onclick={() => (modal = null)}>Abbrechen</button>
      </div>
    </form>
  </Dialog>
{:else if modal?.type === 'confirm'}
  <Dialog title={modal.title} onclose={() => (modal = null)}>
    <p>{modal.text}</p>
    {#if modal.error}<p class="err" role="alert">{modal.error}</p>{/if}
    <div class="actions">
      <button class="primary" disabled={modal.busy} onclick={() => submitModal()}>Löschen</button>
      <button class="secondary" onclick={() => (modal = null)}>Abbrechen</button>
    </div>
  </Dialog>
{:else if modal?.type === 'conflict'}
  <Dialog title="Datei existiert bereits" onclose={() => { modal.resolve('skip'); modal = null }}>
    <p>„{modal.name}“ ist in diesem Ordner schon vorhanden. Die alte Version wandert beim Ersetzen in den Papierkorb.</p>
    <div class="actions">
      <button class="primary" onclick={() => { modal.resolve('replace'); modal = null }}>Ersetzen</button>
      <button class="secondary" onclick={() => { modal.resolve('skip'); modal = null }}>Überspringen</button>
    </div>
  </Dialog>
{:else if modal?.type === 'preview'}
  <Preview node={modal.node} onclose={() => (modal = null)} ondownload={() => download([modal.node])} />
{:else if modal?.type === 'share'}
  <ShareDialog node={modal.node} onclose={() => (modal = null)} />
{:else if modal?.type === 'props'}
  <Properties node={modal.node} onclose={() => (modal = null)} onchange={load} />
{:else if modal?.type === 'settings'}
  <Settings {app} onclose={() => (modal = null)} />
{/if}

<style>
  .app {
    height: 100vh;
    height: 100dvh;
    display: flex;
    flex-direction: column;
    background: var(--surface-2);
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 8px;
    flex: none;
  }
  .toolbar {
    height: 48px;
    background: var(--surface);
    border-bottom: 1px solid var(--line);
    overflow-x: auto;
  }
  .tool {
    display: inline-flex;
    align-items: center;
    gap: 7px;
    height: 34px;
    padding: 0 9px;
    border: 0;
    border-radius: 4px;
    background: none;
    cursor: pointer;
    white-space: nowrap;
    flex: none;
  }
  .tool:hover:not(:disabled) {
    background: var(--hover);
  }
  .tool:disabled {
    opacity: 0.38;
    cursor: default;
  }
  .tool.mode {
    color: var(--accent);
    font-weight: 600;
  }
  .tool.mode.plain {
    color: var(--warning-text);
  }
  .sep {
    width: 1px;
    height: 22px;
    background: var(--line-strong);
    margin: 0 6px;
    flex: none;
  }
  .grow {
    flex: 1;
  }
  .split {
    position: static;
  }
  .only-narrow {
    display: none;
  }
  .address {
    height: 44px;
    gap: 4px;
    background: var(--surface);
    border-bottom: 1px solid var(--line);
  }
  .crumbs {
    flex: 1;
    min-width: 0;
    height: 32px;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 6px;
    margin: 0 4px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted);
    overflow: hidden;
  }
  .crumb {
    border: 0;
    background: none;
    color: var(--text);
    padding: 2px 6px;
    border-radius: 3px;
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    min-width: 0;
  }
  button.crumb:hover {
    background: var(--hover);
  }
  .crumb.static {
    cursor: default;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 220px;
    height: 32px;
    padding: 0 10px;
    border: 1px solid var(--line);
    border-radius: 4px;
    background: var(--surface-2);
    color: var(--muted);
    flex: none;
  }
  .search input {
    border: 0;
    background: none;
    height: 100%;
    padding: 0;
    width: 100%;
    color: var(--text);
    outline: none;
  }
  .error {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 2px 8px 2px 14px;
    background: color-mix(in srgb, var(--danger) 14%, var(--surface));
    color: var(--text);
    flex: none;
  }
  .body {
    flex: 1;
    min-height: 0;
    display: flex;
    position: relative;
  }
  .nav {
    width: 240px;
    flex: none;
    display: flex;
    flex-direction: column;
    background: var(--surface-2);
    border-right: 1px solid var(--line);
    padding: 6px;
    min-height: 0;
  }
  .tree {
    flex: 1;
    overflow: auto;
    min-height: 0;
  }
  .links {
    border-top: 1px solid var(--line);
    padding-top: 6px;
    margin-top: 6px;
  }
  .links button {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    height: 30px;
    padding: 0 10px;
    border: 0;
    border-radius: 4px;
    background: none;
    cursor: pointer;
  }
  .links button:hover {
    background: var(--hover);
  }
  .links button.active {
    background: var(--selected);
  }
  .pane {
    flex: 1;
    min-width: 0;
    overflow: auto;
    position: relative;
    background: var(--surface);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    table-layout: fixed;
  }
  th {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--surface);
    border-bottom: 1px solid var(--line);
    text-align: left;
    font-weight: 400;
    color: var(--muted);
    padding: 0;
  }
  .grip {
    position: absolute;
    top: 0;
    right: -3px;
    width: 7px;
    height: 100%;
    cursor: col-resize;
    z-index: 2;
    touch-action: none;
  }
  /* The last handle would otherwise stick out and cause a scrollbar. */
  th:last-child .grip {
    right: 0;
  }
  th.right button,
  td.right {
    text-align: right;
  }
  td.mono {
    font: 12px Consolas, 'Cascadia Mono', monospace;
  }
  td.bad {
    color: var(--danger);
    font-weight: 600;
  }
  .shared {
    display: inline-flex;
    flex: none;
    color: var(--accent);
    font-style: normal;
  }
  .shared.corner {
    position: absolute;
    top: 6px;
    right: 6px;
  }
  .stats {
    display: flex;
    gap: 14px;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
  }
  .alarm {
    color: var(--danger);
    font-weight: 600;
  }
  th button {
    width: 100%;
    height: 30px;
    padding: 0 10px;
    border: 0;
    border-right: 1px solid var(--line);
    background: none;
    color: inherit;
    text-align: left;
    cursor: pointer;
    white-space: nowrap;
    overflow: hidden;
  }
  th button:hover {
    background: var(--hover);
  }
  td {
    height: 30px;
    padding: 0 10px;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  td.c-name {
    color: var(--text);
  }
  tr {
    cursor: default;
  }
  tbody tr:hover {
    background: var(--hover);
  }
  tr.selected,
  tbody tr.selected:hover {
    background: var(--selected);
  }
  tr.cut,
  .tile.cut {
    opacity: 0.5;
  }
  .cell {
    display: flex;
    align-items: center;
    gap: 9px;
    min-width: 0;
  }
  .label {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(116px, 1fr));
    gap: 4px;
    padding: 10px;
    align-content: start;
    min-height: 100%;
  }
  .tile {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 6px;
    padding: 12px 6px 8px;
    border: 1px solid transparent;
    border-radius: 4px;
    background: none;
    cursor: default;
    min-width: 0;
  }
  .tile:hover {
    background: var(--hover);
  }
  .tile.selected {
    background: var(--selected);
    border-color: var(--selected-line);
  }
  .tile span {
    max-width: 100%;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow-wrap: anywhere;
  }
  .empty {
    height: 100%;
    min-height: 240px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 4px;
    color: var(--muted);
  }
  .empty p {
    margin: 0;
  }
  .dropzone {
    position: absolute;
    inset: 8px;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 6px;
    border: 2px dashed var(--accent);
    border-radius: 8px;
    background: color-mix(in srgb, var(--surface) 86%, transparent);
    color: var(--accent);
    pointer-events: none;
  }
  .dropzone p {
    margin: 0;
    font-weight: 600;
  }
  .status {
    height: 26px;
    gap: 16px;
    font-size: 12px;
    color: var(--muted);
    background: var(--surface);
    border-top: 1px solid var(--line);
    padding: 0 12px;
  }
  .tmp {
    color: var(--muted);
  }
  .tmp .ext {
    opacity: 0.7;
  }
  .tmp progress {
    width: 120px;
    height: 6px;
    flex: none;
    accent-color: var(--accent);
  }
  .tile.tmp progress {
    width: 100%;
  }
  .tmp-line {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .tmp-text {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tmp-actions {
    display: inline-flex;
    flex-wrap: wrap;
    gap: 6px;
    flex: none;
  }
  .tmp-actions button {
    height: 26px;
    padding: 0 8px;
  }
  .menu {
    position: fixed;
    z-index: 50;
    width: 250px;
    padding: 4px;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 8px;
    box-shadow: var(--shadow);
  }
  .menu.drop {
    top: 44px;
    width: 190px;
  }
  .menu button {
    display: flex;
    align-items: center;
    width: 100%;
    height: 32px;
    padding: 0 8px;
    border: 0;
    border-radius: 4px;
    background: none;
    text-align: left;
    cursor: pointer;
  }
  .menu button:hover {
    background: var(--hover);
  }
  .menu button.danger {
    color: var(--danger);
  }
  .menu button.bold {
    font-weight: 600;
  }
  .mi {
    width: 26px;
    display: inline-flex;
    flex: none;
  }
  .menu hr {
    border: 0;
    border-top: 1px solid var(--line);
    margin: 4px -4px;
  }
  .form label {
    display: block;
  }
  .form input {
    display: block;
    width: 100%;
    margin-top: 4px;
  }
  .actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    margin-top: 16px;
  }
  .err {
    color: var(--danger);
    margin: 10px 0 0;
  }
  @media (max-width: 1050px) {
    .tool.mode span,
    .tool:not(.mode) span {
      display: none;
    }
    .search {
      width: 150px;
    }
  }
  @media (max-width: 760px) {
    .only-narrow {
      display: inline-grid;
    }
    .nav {
      position: absolute;
      inset: 0 auto 0 0;
      z-index: 20;
      box-shadow: var(--shadow-lg);
      transform: translateX(-105%);
      visibility: hidden;
      transition: transform 0.12s;
    }
    .nav.open {
      transform: none;
      visibility: visible;
    }
    .stats span:nth-child(n + 4) {
      display: none;
    }
    .search {
      width: 42px;
      padding: 0 13px;
      overflow: hidden;
    }
    .search:focus-within {
      width: 150px;
    }
  }
</style>
