package com.wagnermartin.telex.ui

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.DriveFileMove
import androidx.compose.material.icons.automirrored.filled.OpenInNew
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.automirrored.filled.Sort
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.CameraAlt
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Download
import androidx.compose.material.icons.filled.DriveFileRenameOutline
import androidx.compose.material.icons.filled.GridView
import androidx.compose.material.icons.filled.Info
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PhotoLibrary
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.SelectAll
import androidx.compose.material.icons.filled.UploadFile
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.automirrored.filled.ViewList
import androidx.compose.material.icons.outlined.Circle
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarDuration
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.SwipeToDismissBox
import androidx.compose.material3.SwipeToDismissBoxValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.material3.rememberSwipeToDismissBoxState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.core.content.FileProvider
import com.wagnermartin.telex.Files
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.data.Node
import com.wagnermartin.telex.upload.Uploads
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

/** What a long press or the ⋮ menu can do with files. */
enum class Action(val label: String, val icon: ImageVector) {
    OPEN_WITH("Öffnen mit …", Icons.AutoMirrored.Filled.OpenInNew),
    SEND("Senden an …", Icons.AutoMirrored.Filled.Send),
    DOWNLOAD("Herunterladen", Icons.Default.Download),
    LINK("Link teilen", Icons.Default.Link),
    RENAME("Umbenennen", Icons.Default.DriveFileRenameOutline),
    MOVE("Verschieben", Icons.AutoMirrored.Filled.DriveFileMove),
    INFO("Eigenschaften", Icons.Default.Info),
    DELETE("Löschen", Icons.Default.Delete),
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FilesScreen(
    vm: FilesViewModel,
    snackbar: SnackbarHostState,
    onOpen: (Node, List<Node>) -> Unit,
    fetch: suspend (Node) -> File?,
) {
    val context = LocalContext.current
    val app = context.applicationContext as TelexApp
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboardManager.current
    val haptics = LocalHapticFeedback.current
    val s by vm.state.collectAsState()
    val sort by vm.sort.collectAsState()
    val grid by vm.grid.collectAsState()
    val api = vm.api
    val items = remember(s.items, sort) { vm.sorted(s.items, sort) }

    var addSheet by remember { mutableStateOf(false) }
    var naming by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf<Node?>(null) }
    var moving by remember { mutableStateOf<Set<Long>?>(null) }
    var info by remember { mutableStateOf<Node?>(null) }
    var sortMenu by remember { mutableStateOf(false) }
    var searchText by rememberSaveable { mutableStateOf("") }
    // Searches while typing, after a short pause.
    LaunchedEffect(searchText) {
        if (vm.state.value.query == null) return@LaunchedEffect
        kotlinx.coroutines.delay(350)
        if (vm.state.value.query != searchText) vm.search(searchText)
    }

    LaunchedEffect(Unit) { vm.messages.collect { snackbar.showSnackbar(it) } }

    BackHandler(enabled = s.selecting || s.query != null || s.folder != ROOT) {
        if (s.selecting) vm.clearSelection() else vm.up()
    }

    // Uploads ---------------------------------------------------------------
    val notifyPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    fun upload(uris: List<Uri>) {
        if (uris.isEmpty()) return
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) notifyPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
        val folder = s.folder
        val folderName = s.title
        scope.launch {
            val prepared = withContext(Dispatchers.IO) {
                uris.mapNotNull { Uploads.describe(context, it) }.mapNotNull { runCatching { Uploads.keep(context, it) }.getOrNull() }
            }
            if (prepared.isEmpty()) {
                snackbar.showSnackbar("Die Datei konnte nicht gelesen werden.")
                return@launch
            }
            Uploads.enqueue(context, folder, folderName, prepared)
            snackbar.showSnackbar(if (prepared.size == 1) "„${prepared[0].name}“ wird hochgeladen" else "${prepared.size} Dateien werden hochgeladen")
        }
    }
    val pickFiles = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { upload(it) }
    val pickMedia = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(100)) { upload(it) }
    var cameraFile by rememberSaveable { mutableStateOf<String?>(null) }
    fun cameraUpload(ok: Boolean) {
        val f = cameraFile?.let(::File) ?: return
        cameraFile = null
        if (!ok || f.length() == 0L) {
            f.delete()
            return
        }
        Uploads.enqueue(context, s.folder, s.title, listOf(Uploads.Item(Uri.fromFile(f), f.name, f.length(), temp = true)))
        scope.launch { snackbar.showSnackbar("„${f.name}“ wird hochgeladen") }
    }
    val takePhoto = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { cameraUpload(it) }
    val takeVideo = rememberLauncherForActivityResult(ActivityResultContracts.CaptureVideo()) { cameraUpload(it) }
    fun cameraUri(ext: String, prefix: String): Uri {
        val dir = File(context.filesDir, "outbox").apply { mkdirs() }
        val stamp = SimpleDateFormat("yyyyMMdd_HHmmss", Locale.US).format(Date())
        val f = File(dir, "${prefix}_$stamp.$ext")
        cameraFile = f.path
        return FileProvider.getUriForFile(context, "${context.packageName}.files", f)
    }

    // Actions ---------------------------------------------------------------
    fun delete(ids: Set<Long>) {
        vm.delete(ids) { undo ->
            scope.launch {
                val r = snackbar.showSnackbar("${FilesViewModel.count(ids.size)} im Papierkorb", "Rückgängig", duration = SnackbarDuration.Long)
                if (r == SnackbarResult.ActionPerformed) undo()
            }
        }
    }

    fun run(action: Action, nodes: List<Node>) {
        if (nodes.isEmpty()) return
        val one = nodes.singleOrNull()
        when (action) {
            Action.OPEN_WITH -> one?.let { n -> scope.launch { fetch(n)?.let { Files.open(context, it, n) } } }
            Action.SEND -> scope.launch {
                val files = mutableListOf<Pair<File, Node>>()
                for (n in nodes.filter { !it.isDir }) files += (fetch(n) ?: return@launch) to n
                Files.send(context, files)
                vm.clearSelection()
            }
            Action.DOWNLOAD -> {
                Files.download(context, nodes)
                vm.clearSelection()
            }
            Action.LINK -> one?.let { n ->
                scope.launch {
                    val link = vm.shareLink(n) ?: return@launch
                    clipboard.setText(AnnotatedString(link))
                    val r = snackbar.showSnackbar("Link kopiert", "Teilen")
                    if (r == SnackbarResult.ActionPerformed) {
                        app.expectExternal()
                        context.startActivity(
                            android.content.Intent.createChooser(
                                android.content.Intent(android.content.Intent.ACTION_SEND).setType("text/plain").putExtra(android.content.Intent.EXTRA_TEXT, link),
                                null,
                            ),
                        )
                    }
                }
            }
            Action.RENAME -> renaming = one
            Action.MOVE -> moving = nodes.map { it.id }.toSet()
            Action.INFO -> info = one
            Action.DELETE -> delete(nodes.map { it.id }.toSet())
        }
    }

    fun click(n: Node) {
        when {
            s.selecting -> vm.toggle(n.id)
            n.isDir -> vm.open(n.id)
            else -> onOpen(n, items.filter { !it.isDir })
        }
    }

    fun longClick(n: Node) {
        haptics.performHapticFeedback(HapticFeedbackType.LongPress)
        vm.toggle(n.id)
    }

    val searchFocus = remember { FocusRequester() }

    Scaffold(
        contentWindowInsets = WindowInsets(0),
        snackbarHost = { androidx.compose.material3.SnackbarHost(snackbar) },
        topBar = {
            when {
                s.selecting -> TopAppBar(
                    windowInsets = WindowInsets(0),
                    colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.secondaryContainer),
                    navigationIcon = { IconButton({ vm.clearSelection() }) { Icon(Icons.Default.Close, "Auswahl beenden") } },
                    title = { Text("${s.selected.size} ausgewählt") },
                    actions = {
                        val sel = s.selectedNodes
                        IconButton({ run(Action.DOWNLOAD, sel) }) { Icon(Icons.Default.Download, "Herunterladen") }
                        IconButton({ run(Action.MOVE, sel) }) { Icon(Icons.AutoMirrored.Filled.DriveFileMove, "Verschieben") }
                        IconButton({ run(Action.DELETE, sel) }) { Icon(Icons.Default.Delete, "Löschen") }
                        var more by remember { mutableStateOf(false) }
                        IconButton({ more = true }) { Icon(Icons.Default.MoreVert, "Mehr") }
                        DropdownMenu(more, { more = false }) {
                            DropdownMenuItem({ Text("Alle auswählen") }, { more = false; vm.selectAll() }, leadingIcon = { Icon(Icons.Default.SelectAll, null) })
                            if (sel.none { it.isDir }) DropdownMenuItem({ Text(Action.SEND.label) }, { more = false; run(Action.SEND, sel) }, leadingIcon = { Icon(Action.SEND.icon, null) })
                            if (sel.size == 1) {
                                listOf(Action.LINK, Action.RENAME, Action.INFO).forEach { a ->
                                    DropdownMenuItem({ Text(a.label) }, { more = false; run(a, sel) }, leadingIcon = { Icon(a.icon, null) })
                                }
                            }
                        }
                    },
                )
                s.query != null -> TopAppBar(
                    windowInsets = WindowInsets(0),
                    navigationIcon = { IconButton({ searchText = ""; vm.search(null) }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Suche beenden") } },
                    title = {
                        LaunchedEffect(Unit) { searchFocus.requestFocus() }
                        TextField(
                            searchText, { searchText = it },
                            Modifier.fillMaxWidth().focusRequester(searchFocus),
                            placeholder = { Text("In allen Dateien suchen") },
                            singleLine = true,
                            colors = TextFieldDefaults.colors(focusedContainerColor = Color.Transparent, unfocusedContainerColor = Color.Transparent),
                            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                            keyboardActions = KeyboardActions(onSearch = { vm.search(searchText) }),
                        )
                    },
                    actions = { if (searchText.isNotEmpty()) IconButton({ searchText = ""; vm.search("") }) { Icon(Icons.Default.Close, "Leeren") } },
                )
                else -> TopAppBar(
                    windowInsets = WindowInsets(0),
                    navigationIcon = { if (s.folder != ROOT) IconButton({ vm.up() }) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Zurück") } },
                    title = { Text(s.title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                    actions = {
                        IconButton({ vm.search("") }) { Icon(Icons.Default.Search, "Suchen") }
                        IconButton({ vm.toggleGrid() }) { Icon(if (grid) Icons.AutoMirrored.Filled.ViewList else Icons.Default.GridView, if (grid) "Liste" else "Raster") }
                        IconButton({ sortMenu = true }) { Icon(Icons.AutoMirrored.Filled.Sort, "Sortieren") }
                        DropdownMenu(sortMenu, { sortMenu = false }) {
                            Sort.entries.forEach { o ->
                                DropdownMenuItem(
                                    { Text(o.label) }, { sortMenu = false; vm.setSort(o) },
                                    trailingIcon = { if (o == sort) Icon(Icons.Default.CheckCircle, null, tint = MaterialTheme.colorScheme.primary) },
                                )
                            }
                        }
                    },
                )
            }
        },
        floatingActionButton = {
            AnimatedVisibility(!s.selecting && s.query == null) {
                ExtendedFloatingActionButton(
                    onClick = { addSheet = true },
                    icon = { Icon(Icons.Default.Add, null) },
                    text = { Text("Hinzufügen") },
                )
            }
        },
    ) { padding ->
        Column(Modifier.padding(padding).fillMaxSize()) {
            if (s.query == null && s.path.size > 1) Breadcrumb(s.path) { vm.open(it) }
            TransferBar()
            PullToRefreshBox(s.refreshing, { vm.refresh() }, Modifier.fillMaxSize()) {
                when {
                    s.error != null && items.isEmpty() -> Empty(Icons.Outlined.FolderOpen, s.error!!)
                    s.loading && items.isEmpty() -> Unit
                    items.isEmpty() -> Empty(
                        if (s.query != null) Icons.Default.Search else Icons.Outlined.FolderOpen,
                        if (s.query == null) "Dieser Ordner ist leer.\nMit „Hinzufügen“ lädst du Dateien hoch." else if (s.query.orEmpty().isBlank()) "Suchbegriff eingeben" else "Nichts gefunden",
                    )
                    grid -> LazyVerticalGrid(
                        GridCells.Adaptive(112.dp), Modifier.fillMaxSize(),
                        contentPadding = PaddingValues(8.dp, 8.dp, 8.dp, 96.dp),
                        horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        items(items, key = { it.id }) { n ->
                            Tile(n, api, n.id in s.selected, s.selecting, { click(n) }, { longClick(n) })
                        }
                    }
                    else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
                        items(items, key = { it.id }) { n ->
                            SwipeRow(enabled = !s.selecting, onDelete = { delete(setOf(n.id)) }) {
                                Row(n, api, n.id in s.selected, s.selecting, s.query != null, { click(n) }, { longClick(n) }) { a -> run(a, listOf(n)) }
                            }
                        }
                    }
                }
            }
        }
    }

    if (addSheet) {
        ModalBottomSheet({ addSheet = false }) {
            Column(Modifier.padding(bottom = 24.dp)) {
                Text(
                    "Nach „${s.title}“ " + if (s.encryptDefault) "· verschlüsselt" else "· unverschlüsselt",
                    Modifier.padding(horizontal = 24.dp, vertical = 8.dp), style = MaterialTheme.typography.titleMedium,
                )
                SheetItem(Icons.Default.PhotoLibrary, "Fotos und Videos") {
                    addSheet = false; app.expectExternal()
                    pickMedia.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
                }
                SheetItem(Icons.Default.UploadFile, "Dateien") { addSheet = false; app.expectExternal(); pickFiles.launch(arrayOf("*/*")) }
                SheetItem(Icons.Default.CameraAlt, "Foto aufnehmen") { addSheet = false; app.expectExternal(); takePhoto.launch(cameraUri("jpg", "IMG")) }
                SheetItem(Icons.Default.Videocam, "Video aufnehmen") { addSheet = false; app.expectExternal(); takeVideo.launch(cameraUri("mp4", "VID")) }
                HorizontalDivider(Modifier.padding(vertical = 8.dp))
                SheetItem(Icons.Default.CreateNewFolder, "Neuer Ordner") { addSheet = false; naming = true }
            }
        }
    }

    if (naming) NameDialog("Neuer Ordner", "Neuer Ordner", "Anlegen", { naming = false }) { naming = false; vm.mkdir(it) }
    renaming?.let { n -> NameDialog("Umbenennen", n.name, "Umbenennen", { renaming = null }) { renaming = null; vm.rename(n, it) } }
    moving?.let { ids ->
        if (api != null) {
            FolderPicker(api, "Verschieben: ${FilesViewModel.count(ids.size)}", "Hierher verschieben", s.folder, exclude = ids, onDismiss = { moving = null }) { target ->
                moving = null
                vm.move(ids, target.id, if (target.id == ROOT) "Dateien" else target.name)
            }
        }
    }
    info?.let { n -> PropertiesSheet(n, api) { info = null } }
}

@Composable
fun SheetItem(icon: ImageVector, text: String, onClick: () -> Unit) {
    ListItem(
        headlineContent = { Text(text, style = MaterialTheme.typography.titleMedium) },
        leadingContent = { Icon(icon, null, tint = MaterialTheme.colorScheme.primary) },
        modifier = Modifier.combinedClickable(onClick = onClick).padding(horizontal = 8.dp, vertical = 2.dp),
        colors = ListItemDefaults.colors(containerColor = Color.Transparent),
    )
}

@Composable
fun Empty(icon: ImageVector, text: String) {
    // Inside the pull-to-refresh box the empty state still has to scroll.
    LazyColumn(Modifier.fillMaxSize()) {
        item {
            Column(Modifier.fillMaxWidth().padding(top = 96.dp, start = 32.dp, end = 32.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                Icon(icon, null, Modifier.size(72.dp), tint = MaterialTheme.colorScheme.outline)
                Spacer(Modifier.height(16.dp))
                Text(text, textAlign = TextAlign.Center, style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SwipeRow(enabled: Boolean, onDelete: () -> Unit, content: @Composable () -> Unit) {
    val state = rememberSwipeToDismissBoxState(confirmValueChange = {
        if (it == SwipeToDismissBoxValue.EndToStart) onDelete()
        false
    })
    SwipeToDismissBox(
        state,
        enableDismissFromStartToEnd = false,
        gesturesEnabled = enabled,
        backgroundContent = {
            Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.errorContainer).padding(horizontal = 24.dp), contentAlignment = Alignment.CenterEnd) {
                Icon(Icons.Default.Delete, "Löschen", tint = MaterialTheme.colorScheme.onErrorContainer)
            }
        },
    ) { content() }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Row(
    n: Node,
    api: com.wagnermartin.telex.data.Api?,
    selected: Boolean,
    selecting: Boolean,
    showFolder: Boolean,
    onClick: () -> Unit,
    onLongClick: () -> Unit,
    onAction: (Action) -> Unit,
) {
    var menu by remember { mutableStateOf(false) }
    val detail = buildString {
        if (n.isDir) append(if (n.fileCount == 1L) "1 Datei" else "${n.fileCount} Dateien") else append(formatSize(n.size))
        append(" · ")
        append(formatDate(n.mtime))
        if (n.pending) append(" · wird übertragen")
        if (n.failed) append(" · Upload fehlgeschlagen")
    }
    ListItem(
        modifier = Modifier.combinedClickable(onClick = onClick, onLongClick = onLongClick),
        colors = ListItemDefaults.colors(containerColor = if (selected) MaterialTheme.colorScheme.secondaryContainer else MaterialTheme.colorScheme.surface),
        leadingContent = {
            Box {
                NodeVisual(n, api, 48.dp)
                if (selecting) SelectMark(selected, Modifier.align(Alignment.TopStart))
            }
        },
        headlineContent = { Text(n.name, maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyLarge) },
        supportingContent = { Text(detail, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        trailingContent = if (selecting) null else {
            {
                Box {
                    IconButton({ menu = true }) { Icon(Icons.Default.MoreVert, "Aktionen für ${n.name}") }
                    DropdownMenu(menu, { menu = false }) {
                        Action.entries.filter { a -> !(n.isDir && (a == Action.OPEN_WITH || a == Action.SEND)) }.forEach { a ->
                            DropdownMenuItem(
                                { Text(a.label, color = if (a == Action.DELETE) MaterialTheme.colorScheme.error else Color.Unspecified) },
                                { menu = false; onAction(a) },
                                leadingIcon = { Icon(a.icon, null, tint = if (a == Action.DELETE) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant) },
                            )
                        }
                    }
                }
            }
        },
    )
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun Tile(n: Node, api: com.wagnermartin.telex.data.Api?, selected: Boolean, selecting: Boolean, onClick: () -> Unit, onLongClick: () -> Unit) {
    Column(
        Modifier.clip(RoundedCornerShape(14.dp))
            .background(if (selected) MaterialTheme.colorScheme.secondaryContainer else Color.Transparent)
            .combinedClickable(onClick = onClick, onLongClick = onLongClick)
            .padding(6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(Modifier.fillMaxWidth().aspectRatio(1f), contentAlignment = Alignment.Center) {
            NodeVisual(n, api, if (n.thumb) 112.dp else 72.dp, Modifier.then(if (n.thumb) Modifier.fillMaxSize() else Modifier), corner = 12.dp)
            if (selecting) SelectMark(selected, Modifier.align(Alignment.TopStart).padding(4.dp))
        }
        Spacer(Modifier.height(4.dp))
        Text(n.name, maxLines = 2, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center, style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun SelectMark(selected: Boolean, modifier: Modifier) {
    Icon(
        if (selected) Icons.Default.CheckCircle else Icons.Outlined.Circle, null,
        modifier.size(24.dp).clip(RoundedCornerShape(12.dp)).background(MaterialTheme.colorScheme.surface),
        tint = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.outline,
    )
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PropertiesSheet(n: Node, api: com.wagnermartin.telex.data.Api?, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismiss) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 32.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                NodeVisual(n, api, 64.dp)
                Spacer(Modifier.width(16.dp))
                Text(n.name, style = MaterialTheme.typography.titleLarge)
            }
            fun line(label: String, value: String) = label to value
            val lines = buildList {
                add(line("Typ", if (n.isDir) "Ordner" else n.mime.ifEmpty { "Datei" }))
                add(line("Größe", if (n.isDir) "${n.fileCount} Dateien" else "${formatSize(n.size)} (${"%,d".format(Locale.GERMANY, n.size)} Bytes)"))
                add(line("Geändert", formatDate(n.mtime)))
                add(line("Hochgeladen", formatDate(n.ctime)))
                if (!n.isDir) {
                    add(line("Verschlüsselt", if (n.encrypted) "Ja" else "Nein, in Telegram direkt lesbar"))
                    add(line("Status", when { n.pending -> "Wird zu Telegram übertragen"; n.failed -> "Fehler: ${n.error ?: "unbekannt"}"; else -> "In Telegram gespeichert" }))
                    if (n.segments > 1) add(line("Segmente", n.segments.toString()))
                    n.sha256?.let { add(line("SHA-256", it)) }
                }
                if (n.shared) add(line("Freigabe", "Wird über einen Link geteilt"))
            }
            lines.forEach { (k, v) ->
                Column {
                    Text(k, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Text(v, style = MaterialTheme.typography.bodyLarge)
                }
            }
        }
    }
}
