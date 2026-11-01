<script>
  import { api } from './api.js'
  import FileIcon from './FileIcon.svelte'
  import Icon from './Icon.svelte'
  import TreeNode from './TreeNode.svelte'

  let { node, label, depth = 0, currentId, version, onnavigate, ondropnodes, startOpen = false } = $props()

  let open = $state(startOpen)
  let children = $state(null)
  let over = $state(false)

  async function load() {
    const list = await api(`/nodes/${node.id}/children`)
    children = list.filter((n) => n.isDir)
  }

  $effect(() => {
    version
    if (open) load().catch(() => {})
  })

  function ondrop(e) {
    over = false
    const ids = e.dataTransfer.getData('application/x-telex-nodes')
    if (!ids) return
    e.preventDefault()
    e.stopPropagation()
    ondropnodes(JSON.parse(ids), node.id)
  }
</script>

<div
  class="row"
  class:active={currentId === node.id}
  class:over
  style="padding-left:{depth * 14 + 4}px"
  role="treeitem"
  tabindex="-1"
  aria-selected={currentId === node.id}
  aria-expanded={open}
  ondragover={(e) => {
    if ([...e.dataTransfer.types].includes('application/x-telex-nodes')) {
      e.preventDefault()
      over = true
    }
  }}
  ondragleave={() => (over = false)}
  {ondrop}
>
  <button class="twist" class:open aria-label={open ? 'Zuklappen' : 'Aufklappen'} onclick={() => (open = !open)}>
    <Icon name="chevron" size={11} />
  </button>
  <button class="name" onclick={() => onnavigate(node.id)}>
    <FileIcon {node} size={18} {open} />
    <span>{label ?? node.name}</span>
  </button>
</div>
{#if open && children}
  {#each children as child (child.id)}
    <TreeNode node={child} depth={depth + 1} {currentId} {version} {onnavigate} {ondropnodes} />
  {/each}
{/if}

<style>
  .row {
    display: flex;
    align-items: center;
    height: 30px;
    border-radius: 4px;
    margin: 1px 0;
  }
  .row:hover {
    background: var(--hover);
  }
  .row.active {
    background: var(--selected);
  }
  .row.over {
    outline: 2px solid var(--accent);
    outline-offset: -2px;
  }
  button {
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .twist {
    width: 20px;
    height: 26px;
    display: grid;
    place-items: center;
    flex: none;
    color: var(--muted);
    padding: 0;
  }
  .twist :global(svg) {
    transition: transform 0.1s;
  }
  .twist.open :global(svg) {
    transform: rotate(90deg);
  }
  .name {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    text-align: left;
    height: 100%;
    padding: 0 6px 0 0;
  }
  .name span {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
