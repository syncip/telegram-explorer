<script>
  let { name, size = 16 } = $props()

  const glyphs = {
    back: 'M11 4 6 9l5 5M6.5 9H19',
    forward: 'M9 4l5 5-5 5M1 9h12.5',
    up: 'M4 9l5-5 5 5M9 4.5V17',
    refresh: 'M15 9a6 6 0 1 1-1.8-4.3M15 2v3.5h-3.5',
    search: 'M8 14A6 6 0 1 0 8 2a6 6 0 0 0 0 12zm4.5-1.5L17 17',
    plus: 'M9 3v12M3 9h12',
    upload: 'M9 12V3M5 7l4-4 4 4M3 15h12',
    download: 'M9 3v9M5 8l4 4 4-4M3 15h12',
    trash: 'M3 5h12M7 5V3h4v2m3 0-1 11H5L4 5',
    rename: 'M3 15l1-4 8-8 3 3-8 8zM11 4l3 3',
    share: 'M12 3l4 4-4 4M16 7H8a5 5 0 0 0-5 5v3',
    cut: 'M5 14a2 2 0 1 0 0-4 2 2 0 0 0 0 4zm8 0a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM6.5 10.5 13 2M11.5 10.5 5 2',
    paste: 'M6 3h6v3H6zM4 4H3v12h12V4h-1',
    list: 'M3 5h12M3 9h12M3 13h12',
    grid: 'M3 3h5v5H3zM10 3h5v5h-5zM3 10h5v5H3zM10 10h5v5h-5z',
    columns: 'M3 3h12v12H3zM7 3v12M11 3v12',
    lock: 'M5 8h8v7H5zM6.5 8V5.5a2.5 2.5 0 0 1 5 0V8',
    unlock: 'M5 8h8v7H5zM6.5 8V5.5a2.5 2.5 0 0 1 4.8-1',
    settings: 'M9 11.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5zM9 2v2M9 14v2M2 9h2M14 9h2M4 4l1.5 1.5M12.5 12.5 14 14M4 14l1.5-1.5M12.5 5.5 14 4',
    close: 'M4 4l10 10M14 4 4 14',
    chevron: 'M7 4l5 5-5 5',
    menu: 'M3 5h12M3 9h12M3 13h12',
    logout: 'M7 3H3v12h4M11 5l4 4-4 4M15 9H7',
    restore: 'M3 9a6 6 0 1 0 2-4.5L3 6.5M3 3v3.5h3.5',
    link: 'M7.5 10.5a3 3 0 0 0 4.2 0l3-3a3 3 0 0 0-4.2-4.2L9 4.8M10.5 7.5a3 3 0 0 0-4.2 0l-3 3a3 3 0 0 0 4.2 4.2L9 13.2',
    check: 'M3 9.5l4 4 8-9',
    info: 'M9 8v5M9 5.2v.1M9 16A7 7 0 1 0 9 2a7 7 0 0 0 0 14z',
    copy: 'M6 6h9v9H6zM3 12V3h9',
  }
</script>

<svg width={size} height={size} viewBox="0 0 18 18" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={glyphs[name]} />
</svg>
