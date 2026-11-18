<script>
  import { api } from './api.js'
  import Explorer from './Explorer.svelte'
  import Gate from './Gate.svelte'
  import { live, on, startLive, stopLive } from './live.js'

  let app = $state(null)

  async function refresh() {
    try {
      app = await api('/state')
    } catch {}
  }

  // Logged in, the event stream reports vault and Telegram changes; before
  // that, and when the stream does not get through, the state is polled.
  const loggedIn = $derived(!!app?.loggedIn)
  $effect(() => {
    if (!loggedIn) return
    startLive()
    const offs = [
      on('state', (s) => (app = { ...app, ...s })),
      on('logout', refresh),
      on('closed', refresh),
    ]
    return () => {
      offs.forEach((off) => off())
      stopLive()
    }
  })

  $effect(() => {
    refresh()
    const timer = setInterval(() => {
      if (!live()) refresh()
    }, 3000)
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
