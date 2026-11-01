<script>
  import { api } from './api.js'
  import Explorer from './Explorer.svelte'
  import Gate from './Gate.svelte'

  let app = $state(null)

  async function refresh() {
    app = await api('/state')
  }

  $effect(() => {
    refresh()
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  })

  // A short reconnect must not throw the user out of the explorer.
  let seenReady = $state(false)
  const open = $derived(app && app.loggedIn && !app.locked)
  $effect(() => {
    if (!open) seenReady = false
    else if (app.telegram?.step === 'ready') seenReady = true
  })
  const ready = $derived(open && (app.telegram?.step === 'ready' || (seenReady && app.telegram?.step === 'connecting')))
</script>

{#if app}
  {#if ready}
    <Explorer {app} onlogout={refresh} />
  {:else}
    <Gate state={app} onchange={refresh} />
  {/if}
{/if}
