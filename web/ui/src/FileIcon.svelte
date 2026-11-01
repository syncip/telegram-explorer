<script>
  import { kindOf } from './api.js'

  let { node, size = 20, open = false } = $props()
  const kind = $derived(kindOf(node))
  const tint = { image: '#2f9e6e', video: '#8a5cf6', audio: '#e0568a', archive: '#c98a2c', pdf: '#d13438', doc: '#2b7cd3', code: '#5b6b7b', file: '#8a8f98' }
</script>

<span class="wrap" style="width:{size}px;height:{size}px">
  {#if kind === 'folder'}
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path fill="#e8a820" d="M2 6.5A2.5 2.5 0 0 1 4.5 4h4.3c.6 0 1.2.2 1.6.7L12 6.3h7.5A2.5 2.5 0 0 1 22 8.8v8.7a2.5 2.5 0 0 1-2.5 2.5h-15A2.5 2.5 0 0 1 2 17.5z" />
      <path fill={open ? '#ffe28a' : '#f8cf5a'} d="M2 9.6c0-.9.7-1.6 1.6-1.6h16.8c.9 0 1.6.7 1.6 1.6v7.9a2.5 2.5 0 0 1-2.5 2.5h-15A2.5 2.5 0 0 1 2 17.5z" />
    </svg>
  {:else}
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path fill="var(--file-paper)" stroke="var(--file-edge)" d="M6 2.5h8.5L19.5 7.5V20a1.5 1.5 0 0 1-1.5 1.5H6A1.5 1.5 0 0 1 4.5 20V4A1.5 1.5 0 0 1 6 2.5z" />
      <path fill="var(--file-edge)" d="M14.5 2.5V6a1.5 1.5 0 0 0 1.5 1.5h3.5z" />
      <rect x="7.5" y="11" width="9" height="7" rx="1.2" fill={tint[kind]} />
    </svg>
  {/if}
  {#if node.state === 'pending'}
    <i class="badge sync" title="Wird zu Telegram übertragen"></i>
  {:else if node.state === 'failed'}
    <i class="badge fail" title="Upload fehlgeschlagen"></i>
  {:else if !node.isDir && !node.encrypted}
    <i class="badge plain" title="Unverschlüsselt, in Telegram direkt lesbar"></i>
  {/if}
</span>

<style>
  .wrap {
    position: relative;
    display: inline-flex;
    flex: none;
  }
  .badge {
    position: absolute;
    right: -2px;
    bottom: -2px;
    width: 9px;
    height: 9px;
    border-radius: 50%;
    border: 1.5px solid var(--surface);
  }
  .sync {
    background: var(--accent);
    animation: pulse 1.4s ease-in-out infinite;
  }
  .fail {
    background: var(--danger);
  }
  .plain {
    background: var(--warning);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .sync {
      animation: none;
    }
  }
</style>
