<script>
  // The editor alone, for /?edit=ID: the Android app shows this in its own
  // screen. Closing hands back to the app, or to the explorer in a browser.
  import { api } from './api.js'
  import { onBack } from './native.js'
  import Editor from './Editor.svelte'

  let { id } = $props()
  let node = $state(null)
  let error = $state('')
  let editor = $state()

  $effect(() => {
    api(`/nodes/${id}`)
      .then((info) => (node = info.node))
      .catch((err) => (error = err.message))
  })

  // The app's back button closes the editor the same way its button does.
  $effect(() => onBack(() => (editor?.close(), true)))

  function close() {
    if (window.TelexEditor) window.TelexEditor.close()
    else location.href = '/'
  }

  function open(next) {
    history.replaceState(null, '', location.pathname + location.search.replace(/([?&]edit=)\d+/, `$1${next.id}`))
    node = next
  }
</script>

{#if error}
  <p class="err">{error}</p>
{:else if node}
  {#key node.id}
    <Editor bind:this={editor} {node} standalone onclose={close} onopen={open} />
  {/key}
{/if}

<style>
  .err {
    margin: 24px;
    color: var(--danger);
  }
</style>
