<script>
  // unit is the length of one step in minutes, 0 means no end, KEEP no change.
  let { amount = $bindable(), unit = $bindable(), keep = '' } = $props()

  const UNITS = [
    { label: 'Minuten', minutes: 1 },
    { label: 'Stunden', minutes: 60 },
    { label: 'Tage', minutes: 1440 },
    { label: 'Wochen', minutes: 10080 },
    { label: 'Monate', minutes: 43200 },
    { label: 'Jahre', minutes: 525600 },
  ]
  const MAX_MINUTES = 100 * 525600

  const until = $derived(amount > 0 && unit * amount <= MAX_MINUTES ? new Date(Date.now() + amount * unit * 60000).toLocaleString('de-DE', { dateStyle: 'medium', timeStyle: 'short' }) : '')
  const hint = $derived(unit === KEEP ? '' : unit === 0 ? 'Der Link läuft nicht ab.' : until ? `Läuft ab am ${until}` : 'Höchstens 100 Jahre')
</script>

<script module>
  export const KEEP = -1
</script>

<fieldset>
  <legend>Gültig für</legend>
  <div class="duration">
    {#if unit > 0}
      <input type="number" min="1" max={Math.floor(MAX_MINUTES / unit)} step="1" required aria-label="Dauer" bind:value={amount} />
    {/if}
    <select aria-label="Einheit" bind:value={unit}>
      {#if keep}<option value={KEEP}>{keep}</option>{/if}
      {#each UNITS as u (u.minutes)}<option value={u.minutes}>{u.label}</option>{/each}
      <option value={0}>Unbegrenzt</option>
    </select>
  </div>
  {#if hint}<small>{hint}</small>{/if}
</fieldset>

<style>
  fieldset {
    display: grid;
    gap: 4px;
    min-width: 0;
    margin: 0 0 12px;
    padding: 0;
    border: 0;
  }
  legend {
    padding: 0 0 4px;
  }
  .duration {
    display: flex;
    gap: 8px;
  }
  .duration > * {
    flex: 1;
    min-width: 0;
  }
  small {
    color: var(--muted);
  }
</style>
