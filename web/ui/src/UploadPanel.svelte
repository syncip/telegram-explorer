<script>
  import { api, formatSize, post } from './api.js'
  import { formatDuration, track } from './eta.js'
  import { cancel, clearFinished, retry, uploads } from './uploads.svelte.js'
  import Icon from './Icon.svelte'

  let { jobs, verify, onverifychange } = $props()
  let collapsed = $state(false)
  let tick = $state(0)

  $effect(() => {
    const timer = setInterval(() => tick++, 1000)
    return () => clearInterval(timer)
  })

  const visible = $derived(uploads.items.filter((i) => i.state !== 'canceled'))
  const showVerify = $derived(verify && verify.total > 0)
  const total = $derived(visible.length + jobs.length + (showVerify ? 1 : 0))

  const stateText = {
    queued: 'Wartet',
    done: 'Auf dem Server, geht weiter zu Telegram',
    skipped: 'Übersprungen',
    error: 'Fehlgeschlagen',
  }
  const pct = (a, b) => (b > 0 ? Math.round((a / b) * 100) : 100)

  function line(key, done, size, label) {
    tick
    const { speed, seconds } = track(key, done, size)
    const parts = [`${label}: ${formatSize(done)} von ${formatSize(size)}`]
    if (speed > 0) parts.push(`${formatSize(speed)}/s`)
    parts.push(formatDuration(seconds))
    return parts.join(' · ')
  }

  // The overall estimate covers both legs: into the server and on to Telegram.
  const remaining = $derived.by(() => {
    tick
    const web = uploads.items
      .filter((i) => i.state === 'running' || i.state === 'queued')
      .reduce((sum, i) => sum + (i.file.size - i.sent) + i.file.size, 0)
    const tg = jobs.reduce((sum, j) => sum + j.total - j.sent, 0)
    const bytes = web + tg
    if (bytes === 0) return ''
    const active = jobs.find((j) => j.sent > 0 && !j.error)
    const speed = active ? track(`job-${active.id}`, active.sent, active.total).speed : 0
    return speed > 0 ? formatDuration(bytes / speed) : ''
  })

  async function stopVerify() {
    await api('/verify', { method: 'DELETE' })
    onverifychange()
  }
</script>

{#if total > 0}
  <section class="panel" aria-label="Übertragungen">
    <header>
      <button class="title" onclick={() => (collapsed = !collapsed)} aria-expanded={!collapsed}>
        Übertragungen ({total}){remaining ? `, ${remaining}` : ''}
      </button>
      <button class="icon" title="Abgeschlossene entfernen" aria-label="Abgeschlossene entfernen" onclick={clearFinished}><Icon name="check" size={14} /></button>
    </header>
    {#if !collapsed}
      <ul>
        {#if showVerify}
          <li>
            <div class="line">
              <span class="name">{verify.running ? `Prüfung: ${verify.current}` : 'Prüfung abgeschlossen'}</span>
              <button class="icon sm" aria-label={verify.running ? 'Prüfung abbrechen' : 'Ausblenden'} onclick={stopVerify}><Icon name="close" size={12} /></button>
            </div>
            <progress max="100" value={pct(verify.bytesDone, verify.bytesTotal)}></progress>
            {#if verify.running}
              <small>{verify.done} von {verify.total} Dateien · {line('verify', verify.bytesDone, verify.bytesTotal, 'Geladen')}</small>
            {:else}
              <small class:bad={verify.bad?.length}>
                {verify.done} geprüft, {verify.bad?.length ?? 0} beschädigt, {verify.failed?.length ?? 0} nicht prüfbar
              </small>
            {/if}
          </li>
        {/if}
        {#each visible as item (item.key)}
          <li>
            <div class="line">
              <span class="name">{item.name}</span>
              {#if item.state === 'error'}
                <button class="icon sm" aria-label="Wiederholen" onclick={() => retry(item)}><Icon name="restore" size={13} /></button>
              {/if}
              {#if item.state === 'queued' || item.state === 'running' || item.state === 'error'}
                <button class="icon sm" aria-label="Abbrechen" onclick={() => cancel(item)}><Icon name="close" size={12} /></button>
              {/if}
            </div>
            {#if item.state === 'running'}
              <progress max="100" value={pct(item.sent, item.file.size)}></progress>
              <small>{item.error || line(item.key, item.sent, item.file.size, 'Zum Server')}</small>
            {:else}
              <small class:bad={item.state === 'error'}>{item.state === 'error' ? item.error : stateText[item.state]}</small>
            {/if}
          </li>
        {/each}
        {#each jobs as job (job.id)}
          <li>
            <div class="line">
              <span class="name">{job.name}</span>
              {#if job.state === 'failed' || job.error}
                <button class="icon sm" aria-label="Jetzt wiederholen" onclick={() => post(`/nodes/${job.id}/retry`)}><Icon name="restore" size={13} /></button>
              {/if}
            </div>
            <progress max="100" value={pct(job.sent, job.total)}></progress>
            {#if job.error}
              <small class="bad">Neuer Versuch folgt: {job.error}</small>
            {:else if job.sent === 0}
              <small>Wartet auf Telegram</small>
            {:else}
              <small>{line(`job-${job.id}`, job.sent, job.total, 'Zu Telegram')}</small>
            {/if}
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
    max-height: 280px;
    overflow: auto;
  }
  li {
    padding: 8px 12px;
    border-bottom: 1px solid var(--line);
  }
  li:last-child {
    border-bottom: 0;
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
