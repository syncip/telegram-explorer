<script>
  import { api, formatSize, post } from './api.js'
  import { formatDuration, track } from './eta.js'
  import { cancel, clearFinished, retry, uploads } from './uploads.svelte.js'
  import Icon from './Icon.svelte'

  let { verify, onverifychange } = $props()
  let collapsed = $state(false)
  let tick = $state(0)

  $effect(() => {
    const timer = setInterval(() => tick++, 1000)
    return () => clearInterval(timer)
  })

  const STEPS = ['Server', 'Telegram', 'Fertig']
  const AUTO_CLOSE = 10000
  const ACTIVE = ['queued', 'running', 'server']

  const visible = $derived(uploads.items.filter((i) => i.state !== 'canceled'))
  const active = $derived(visible.filter((i) => ACTIVE.includes(i.state)).length + (verify?.running ? 1 : 0))
  const showVerify = $derived(verify && verify.total > 0)
  const total = $derived(visible.length + (showVerify ? 1 : 0))
  const pct = (a, b) => (b > 0 ? Math.round((a / b) * 100) : 100)

  const stepOf = (item) => (item.state === 'finished' ? 2 : item.state === 'server' ? 1 : 0)
  const hashing = (item) => item.state === 'running' && item.sent >= item.size
  const failed = (item) => item.state === 'error' || item.job?.state === 'failed'

  function line(key, done, size, label) {
    tick
    const { speed, seconds } = track(key, done, size)
    const parts = [`${label}: ${formatSize(done)} von ${formatSize(size)}`]
    if (speed > 0) parts.push(`${formatSize(speed)}/s`)
    parts.push(formatDuration(seconds))
    return parts.join(' · ')
  }

  function detail(item) {
    if (item.state === 'queued') return 'Wartet auf den Start'
    if (item.state === 'skipped') return 'Übersprungen, Datei existiert bereits'
    if (item.state === 'error') return item.error
    if (item.state === 'finished') return 'Bei Telegram gespeichert, Größe bestätigt'
    if (item.state === 'running') {
      if (item.error) return item.error
      if (hashing(item)) return 'Komplett auf dem Server, wird abgeschlossen'
      return line(item.key, item.sent, item.size, 'Zum Server')
    }
    const job = item.job
    if (job?.state === 'failed') return job.error || 'Übertragung zu Telegram fehlgeschlagen'
    if (job?.error) return `Neuer Versuch folgt: ${job.error}`
    if (!job?.sent) return 'Liegt auf dem Server, wartet auf Telegram'
    return line(`job-${item.nodeId}`, job.sent, job.total, 'Zu Telegram')
  }

  const barValue = (item) => (item.state === 'server' ? pct(item.job?.sent ?? 0, item.size) : pct(item.sent, item.size))

  // The overall estimate covers both legs: into the server and on to Telegram.
  const remaining = $derived.by(() => {
    tick
    let bytes = 0
    let speed = 0
    for (const i of uploads.items) {
      if (i.state === 'running' || i.state === 'queued') bytes += i.size - i.sent + i.size
      if (i.state === 'running') speed ||= track(i.key, i.sent, i.size).speed
      if (i.state === 'server' && i.job) {
        bytes += i.job.total - i.job.sent
        if (i.job.sent > 0) speed = track(`job-${i.nodeId}`, i.job.sent, i.job.total).speed || speed
      }
    }
    return bytes > 0 && speed > 0 ? formatDuration(bytes / speed) : ''
  })

  async function stopVerify() {
    await api('/verify', { method: 'DELETE' })
    onverifychange()
  }

  function clearDone() {
    clearFinished()
    if (showVerify && !verify.running) stopVerify()
  }

  // Once nothing runs and nothing went wrong, the panel clears itself after a
  // while, unless the pointer rests on it.
  let hovered = $state(false)
  const failures = $derived(visible.some(failed) || (showVerify && !verify.running && (verify.bad?.length || verify.failed?.length)))
  const signature = $derived(JSON.stringify([visible.map((i) => [i.key, i.state, i.sent, i.job?.sent]), verify?.done, verify?.running]))
  $effect(() => {
    signature
    if (total === 0 || active > 0 || failures || hovered) return
    const timer = setTimeout(clearDone, AUTO_CLOSE)
    return () => clearTimeout(timer)
  })
</script>

{#if total > 0}
  <section class="panel" aria-label="Übertragungen" onmouseenter={() => (hovered = true)} onmouseleave={() => (hovered = false)}>
    <header>
      <button class="title" onclick={() => (collapsed = !collapsed)} aria-expanded={!collapsed}>
        {active ? `Übertragungen (${active} aktiv)` : 'Übertragungen abgeschlossen'}{remaining ? `, ${remaining}` : ''}
      </button>
      <button class="icon" title="Abgeschlossene entfernen" aria-label="Abgeschlossene entfernen" onclick={clearDone}><Icon name="check" size={14} /></button>
    </header>
    {#if !collapsed}
      <ul>
        {#if showVerify}
          <li>
            <div class="line">
              <span class="name">{verify.running ? `Prüfung: ${verify.current}` : 'Prüfung abgeschlossen'}</span>
              {#if verify.running}
                <button class="icon sm" title="Prüfung abbrechen" aria-label="Prüfung abbrechen" onclick={stopVerify}><Icon name="close" size={12} /></button>
              {:else}
                <button class="icon sm" title="Bestätigen" aria-label="Bestätigen" onclick={stopVerify}><Icon name="check" size={14} /></button>
              {/if}
            </div>
            <progress max="100" value={verify.bytesTotal > 0 ? pct(verify.bytesDone, verify.bytesTotal) : pct(verify.done, verify.total)}></progress>
            {#if verify.running}
              <small>{verify.done} von {verify.total} Dateien{verify.bytesTotal > 0 ? ` · ${line('verify', verify.bytesDone, verify.bytesTotal, 'Geladen')}` : ''}</small>
            {:else}
              <small class:bad={verify.bad?.length}>
                {verify.done} geprüft, {verify.bad?.length ?? 0} fehlen oder beschädigt, {verify.failed?.length ?? 0} nicht prüfbar
              </small>
            {/if}
          </li>
        {/if}
        {#each visible as item (item.key)}
          <li>
            <div class="line">
              <span class="name" title={item.name}>{item.name}</span>
              {#if item.state === 'error'}
                <button class="icon sm" aria-label="Wiederholen" onclick={() => retry(item)}><Icon name="restore" size={13} /></button>
              {/if}
              {#if item.state === 'server' && (item.job?.error || item.job?.state === 'failed')}
                <button class="icon sm" aria-label="Jetzt wiederholen" onclick={() => post(`/nodes/${item.nodeId}/retry`)}><Icon name="restore" size={13} /></button>
              {/if}
              {#if item.state === 'queued' || item.state === 'running' || item.state === 'error'}
                <button class="icon sm" aria-label="Abbrechen" onclick={() => cancel(item)}><Icon name="close" size={12} /></button>
              {/if}
            </div>
            {#if item.state !== 'skipped'}
              <ol class="steps" class:failed={failed(item)}>
                {#each STEPS as step, i (step)}
                  <li class:done={i < stepOf(item) || item.state === 'finished'} class:current={i === stepOf(item) && item.state !== 'finished'}>
                    {#if i < stepOf(item) || item.state === 'finished'}<Icon name="check" size={11} />{/if}{step}
                  </li>
                {/each}
              </ol>
            {/if}
            {#if hashing(item)}
              <progress></progress>
            {:else if item.state === 'running' || (item.state === 'server' && item.job?.sent > 0)}
              <progress max="100" value={barValue(item)}></progress>
            {/if}
            <small class:bad={failed(item)}>{detail(item)}</small>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}

<style>
  .panel {
    position: fixed;
    right: 16px;
    bottom: 34px;
    width: min(380px, calc(100vw - 32px));
    z-index: 30;
    background: var(--surface);
    border: 1px solid var(--line);
    border-radius: 8px;
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    padding: 2px 4px 2px 12px;
    border-bottom: 1px solid var(--line);
  }
  .title {
    flex: 1;
    border: 0;
    background: none;
    text-align: left;
    font-weight: 600;
    height: 32px;
    cursor: pointer;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 340px;
    overflow: auto;
  }
  ul > li {
    padding: 8px 12px;
    border-bottom: 1px solid var(--line);
  }
  ul > li:last-child {
    border-bottom: 0;
  }
  .steps {
    display: flex;
    gap: 4px;
    list-style: none;
    margin: 2px 0 4px;
    padding: 0;
    font-size: 12px;
    color: var(--muted);
  }
  .steps li {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 1px 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
  }
  .steps li.done {
    color: var(--accent);
    border-color: var(--accent);
  }
  .steps li.current {
    color: var(--text);
    border-color: var(--text);
    font-weight: 600;
  }
  .steps.failed li.current {
    color: var(--danger);
    border-color: var(--danger);
  }
  .line {
    display: flex;
    align-items: center;
    gap: 2px;
    min-height: 24px;
  }
  .name {
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sm {
    width: 24px;
    height: 24px;
  }
  progress {
    display: block;
    width: 100%;
    height: 4px;
    margin: 3px 0;
    accent-color: var(--accent);
  }
  small {
    color: var(--muted);
    overflow-wrap: anywhere;
  }
  small.bad {
    color: var(--danger);
  }
</style>
