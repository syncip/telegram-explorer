<script>
  import Icon from './Icon.svelte'

  let { title, onclose, wide = false, children } = $props()
  let dialog = $state()

  $effect(() => {
    dialog.showModal()
  })
</script>

<dialog bind:this={dialog} class:wide oncancel={(e) => { e.preventDefault(); onclose() }} aria-label={title}>
  <header>
    <h2>{title}</h2>
    <button class="icon" aria-label="Schließen" onclick={onclose}><Icon name="close" size={14} /></button>
  </header>
  <div class="body">{@render children()}</div>
</dialog>

<style>
  dialog {
    width: min(440px, calc(100vw - 32px));
    max-height: calc(100vh - 32px);
    padding: 0;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--surface);
    color: var(--text);
    box-shadow: var(--shadow-lg);
    overflow: hidden;
  }
  dialog[open] {
    display: flex;
    flex-direction: column;
  }
  dialog.wide {
    width: min(960px, calc(100vw - 32px));
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.35);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 12px 8px 20px;
    gap: 12px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .body {
    padding: 8px 20px 20px;
    overflow: auto;
  }
</style>
