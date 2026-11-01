<script>
  import { api, formatSize, post } from './api.js'
  import { cancel, clearFinished, retry, uploads } from './uploads.svelte.js'
  import Icon from './Icon.svelte'

  let { jobs } = $props()
  let collapsed = $state(false)

  const visible = $derived(uploads.items.filter((i) => i.state !== 'canceled'))
  const total = $derived(visible.length + jobs.length)
  const stateText = {
    queued: 'Wartet',
    done: 'Auf dem Server, geht weiter zu Telegram',
    skipped: 'Übersprungen',
    error: 'Fehlgeschlagen',
  }
  const pct = (a, b) => (b > 0 ? Math.round((a / b) * 100) : 100)
</script>

{#if total > 0}
  <section class="panel" aria-label="Übertragungen">
    <header>
      <button class="title" onclick={() => (collapsed = !collapsed)} aria-expanded={!collapsed}>Übertragungen ({total})</button>
      <button class="icon" title="Abgeschlossene entfernen" aria-label="Abgeschlossene entfernen" onclick={clearFinished}><Icon name="check" size={14} /></button>
    </header>
    {#if !collapsed}
      <ul>
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
              <small>{item.error || `Zum Server: ${formatSize(item.sent)} von ${formatSize(item.file.size)}`}</small>
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
            <small class:bad={job.error}>{job.error ? `Neuer Versuch folgt: ${job.error}` : `Zu Telegram: ${formatSize(job.sent)} von ${formatSize(job.total)}`}</small>
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
    width: min(340px, calc(100vw - 32px));
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
