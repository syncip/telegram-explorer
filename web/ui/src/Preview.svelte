<script>
  import { formatSize, kindOf } from './api.js'
  import Dialog from './Dialog.svelte'

  let { node, onclose, ondownload } = $props()

  const TEXT_LIMIT = 256 * 1024
  const url = $derived(`/api/nodes/${node.id}/content?inline=1`)
  const kind = $derived(kindOf(node))
  const previewable = $derived(/^(image\/(png|jpeg|gif|webp)|video\/|audio\/)/.test(node.mime))
  const textual = $derived(node.mime.startsWith('text/plain') || ['code', 'doc'].includes(kind) && /\.(txt|md|csv|log|json|ya?ml|go|js|ts|py|sh|ps1|sql|xml|css|c|cpp|h|cs|java|rs)$/i.test(node.name))

  let text = $state(null)

  $effect(() => {
    if (!textual) return
    fetch(`/api/nodes/${node.id}/content`, { headers: { Range: `bytes=0-${TEXT_LIMIT - 1}` } })
      .then((r) => r.text())
      .then((t) => (text = t))
      .catch((e) => (text = String(e)))
  })
</script>

<Dialog title={node.name} {onclose} wide>
  <div class="stage">
    {#if previewable && node.mime.startsWith('image/')}
      <img src={url} alt={node.name} />
    {:else if previewable && node.mime.startsWith('video/')}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video src={url} controls autoplay></video>
    {:else if previewable}
      <audio src={url} controls autoplay></audio>
    {:else if textual}
      {#if text === null}
        <p>Wird geladen</p>
      {:else}
        <pre>{text}</pre>
        {#if node.size > TEXT_LIMIT}<p>Nur der Anfang der Datei wird angezeigt.</p>{/if}
      {/if}
    {:else}
      <p>Für diesen Dateityp gibt es keine Vorschau.</p>
    {/if}
  </div>
  <div class="foot">
    <span>{formatSize(node.size)}</span>
    <button class="primary" onclick={ondownload}>Herunterladen</button>
  </div>
</Dialog>

<style>
  .stage {
    display: grid;
    place-items: center;
    min-height: 200px;
  }
  img,
  video {
    max-width: 100%;
    max-height: 68vh;
    display: block;
  }
  audio {
    width: 100%;
  }
  pre {
    width: 100%;
    max-height: 62vh;
    overflow: auto;
    margin: 0;
    padding: 12px;
    background: var(--surface-2);
    border: 1px solid var(--line);
    border-radius: 4px;
    font: 13px/1.5 Consolas, 'Cascadia Mono', monospace;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
  .foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 14px;
    color: var(--muted);
  }
</style>
