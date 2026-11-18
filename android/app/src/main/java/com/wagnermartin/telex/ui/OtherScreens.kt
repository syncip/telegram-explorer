package com.wagnermartin.telex.ui

import android.content.Intent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.AdminPanelSettings
import androidx.compose.material.icons.filled.ContentCopy
import androidx.compose.material.icons.filled.DeleteForever
import androidx.compose.material.icons.filled.Devices
import androidx.compose.material.icons.filled.History
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.LinkOff
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.Restore
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material.icons.filled.Share
import androidx.compose.material.icons.filled.Storage
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.wagnermartin.telex.SettingsActivity
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.WebAdminActivity
import com.wagnermartin.telex.data.Node
import com.wagnermartin.telex.data.Share
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.launch

/** Loads a list and reloads it when the server reports a change. */
@OptIn(kotlinx.coroutines.FlowPreview::class)
@Composable
private fun <T> liveList(load: suspend () -> List<T>): Triple<List<T>?, String?, () -> Unit> {
    val app = LocalContext.current.applicationContext as TelexApp
    var list by remember { mutableStateOf<List<T>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(tick) {
        try {
            list = load()
            error = null
        } catch (e: Exception) {
            error = e.message
        }
    }
    LaunchedEffect(Unit) { app.live.files.debounce(300).collect { tick++ } }
    return Triple(list, error, { tick++ })
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SharesScreen(snackbar: SnackbarHostState) {
    val context = LocalContext.current
    val app = context.applicationContext as TelexApp
    val api = app.api ?: return
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboardManager.current
    val (list, error, reload) = liveList { api.shares() }
    var ending by remember { mutableStateOf<Share?>(null) }

    Scaffold(contentWindowInsets = WindowInsets(0), topBar = { TopAppBar({ Text("Freigaben") }, windowInsets = WindowInsets(0)) }) { padding ->
        PullToRefreshBox(false, reload, Modifier.padding(padding).fillMaxSize()) {
            when {
                error != null && list == null -> Empty(Icons.Default.LinkOff, error)
                list == null -> Unit
                list.isEmpty() -> Empty(Icons.Default.Link, "Noch nichts geteilt.\nIn „Dateien“ über ⋮ › „Link teilen“ erzeugst du einen Link.")
                else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
                    items(list, key = { it.token }) { sh ->
                        val link = api.shareLink(sh.token)
                        var menu by remember { mutableStateOf(false) }
                        ListItem(
                            leadingContent = { NodeVisual(Node(sh.nodeId, name = sh.name, isDir = sh.isDir), null, 44.dp) },
                            headlineContent = { Text(sh.name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
                            supportingContent = {
                                Text(buildString {
                                    append("${sh.downloads}")
                                    if (sh.maxDownloads > 0) append(" von ${sh.maxDownloads}")
                                    append(" Downloads")
                                    if (sh.expiresAt > 0) append(" · bis ${formatDate(sh.expiresAt)}")
                                    if (sh.hasPassword) append(" · mit Kennwort")
                                    if (sh.trashed) append(" · im Papierkorb")
                                })
                            },
                            trailingContent = {
                                IconButton({ menu = true }) { Icon(Icons.Default.MoreVert, "Aktionen") }
                                DropdownMenu(menu, { menu = false }) {
                                    DropdownMenuItem({ Text("Link kopieren") }, {
                                        menu = false
                                        clipboard.setText(AnnotatedString(link))
                                        scope.launch { snackbar.showSnackbar("Link kopiert") }
                                    }, leadingIcon = { Icon(Icons.Default.ContentCopy, null) })
                                    DropdownMenuItem({ Text("Link senden") }, {
                                        menu = false
                                        app.expectExternal()
                                        context.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, link), null))
                                    }, leadingIcon = { Icon(Icons.Default.Share, null) })
                                    DropdownMenuItem({ Text("Freigabe beenden", color = MaterialTheme.colorScheme.error) }, { menu = false; ending = sh },
                                        leadingIcon = { Icon(Icons.Default.LinkOff, null, tint = MaterialTheme.colorScheme.error) })
                                }
                            },
                            modifier = Modifier.clickable {
                                clipboard.setText(AnnotatedString(link))
                                scope.launch { snackbar.showSnackbar("Link kopiert") }
                            },
                        )
                    }
                    item {
                        Text(
                            "Kennwort, Ablaufdatum und Download-Grenze stellst du in der Webansicht ein (Mehr › Webansicht).",
                            Modifier.padding(24.dp), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }
    ending?.let { sh ->
        ConfirmDialog("Freigabe beenden?", "Der Link zu „${sh.name}“ funktioniert danach nicht mehr.", "Beenden", { ending = null }) {
            scope.launch {
                runCatching { api.deleteShare(sh.token) }.onFailure { snackbar.showSnackbar(it.message ?: "Fehler") }
                reload()
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrashScreen(snackbar: SnackbarHostState) {
    val app = LocalContext.current.applicationContext as TelexApp
    val api = app.api ?: return
    val scope = rememberCoroutineScope()
    val (list, error, reload) = liveList { api.trash() }
    var purging by remember { mutableStateOf<List<Node>?>(null) }

    fun act(block: suspend () -> Unit, done: String) = scope.launch {
        try {
            block()
            snackbar.showSnackbar(done)
        } catch (e: Exception) {
            snackbar.showSnackbar(e.message ?: "Fehler")
        }
        reload()
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0),
        topBar = {
            TopAppBar({ Text("Papierkorb") }, windowInsets = WindowInsets(0), actions = {
                if (!list.isNullOrEmpty()) TextButton({ purging = list }) { Text("Leeren", color = MaterialTheme.colorScheme.error) }
            })
        },
    ) { padding ->
        PullToRefreshBox(false, reload, Modifier.padding(padding).fillMaxSize()) {
            when {
                error != null && list == null -> Empty(Icons.Outlined.DeleteOutline, error)
                list == null -> Unit
                list.isEmpty() -> Empty(Icons.Outlined.DeleteOutline, "Der Papierkorb ist leer.")
                else -> LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 24.dp)) {
                    items(list, key = { it.id }) { n ->
                        var menu by remember { mutableStateOf(false) }
                        ListItem(
                            leadingContent = { NodeVisual(n, null, 44.dp) },
                            headlineContent = { Text(n.name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
                            supportingContent = { Text((if (n.isDir) "Ordner" else formatSize(n.size)) + " · gelöscht ${formatDate(n.trashedAt)}") },
                            trailingContent = {
                                IconButton({ menu = true }) { Icon(Icons.Default.MoreVert, "Aktionen") }
                                DropdownMenu(menu, { menu = false }) {
                                    DropdownMenuItem({ Text("Wiederherstellen") }, { menu = false; act({ api.restore(listOf(n.id)) }, "„${n.name}“ wiederhergestellt") },
                                        leadingIcon = { Icon(Icons.Default.Restore, null) })
                                    DropdownMenuItem({ Text("Endgültig löschen", color = MaterialTheme.colorScheme.error) }, { menu = false; purging = listOf(n) },
                                        leadingIcon = { Icon(Icons.Default.DeleteForever, null, tint = MaterialTheme.colorScheme.error) })
                                }
                            },
                            modifier = Modifier.clickable { menu = true },
                        )
                    }
                }
            }
        }
    }
    purging?.let { nodes ->
        ConfirmDialog(
            "Endgültig löschen?",
            (if (nodes.size == 1) "„${nodes[0].name}“ wird" else "${nodes.size} Einträge werden") + " auch bei Telegram gelöscht. Das lässt sich nicht rückgängig machen.",
            "Endgültig löschen", { purging = null },
        ) { act({ api.purge(nodes.map { it.id }) }, "Endgültig gelöscht") }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MoreScreen(vm: AppViewModel) {
    val context = LocalContext.current
    val app = vm.app
    val stats by app.live.stats.collectAsState()
    val connected by app.live.connected.collectAsState()
    var confirmLogout by remember { mutableStateOf(false) }

    fun web(hash: String) {
        context.startActivity(Intent(context, WebAdminActivity::class.java).putExtra(WebAdminActivity.EXTRA_HASH, hash))
    }

    Scaffold(contentWindowInsets = WindowInsets(0), topBar = { TopAppBar({ Text("Mehr") }, windowInsets = WindowInsets(0)) }) { padding ->
        LazyColumn(Modifier.padding(padding).fillMaxSize()) {
            item {
                ListItem(
                    leadingContent = { Icon(Icons.Default.Storage, null) },
                    headlineContent = { Text(vm.username?.let { "Angemeldet als $it" } ?: "Angemeldet") },
                    supportingContent = {
                        Column {
                            Text(app.prefs.serverUrl ?: "")
                            stats?.let { Text("${it.files} Dateien · ${formatSize(it.size)} · ${it.shares} Freigaben") }
                            Text(if (connected) "Live verbunden" else "Nicht live verbunden", color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    },
                )
                HorizontalDivider()
            }
            item { MoreItem(Icons.Default.Settings, "App-Einstellungen", "Sperre, Fingerabdruck, Schriftgröße") { context.startActivity(Intent(context, SettingsActivity::class.java)) } }
            item { MoreItem(Icons.Default.AdminPanelSettings, "Server-Einstellungen", "Kennwort, 2FA, SFTP, WebDAV, Download-Helfer") { web("settings") } }
            item { MoreItem(Icons.Default.Devices, "Angemeldete Geräte", "In den Server-Einstellungen unter „Sicherheit“") { web("settings") } }
            item { MoreItem(Icons.Default.History, "Protokoll", "Uploads, Downloads, Anmeldungen") { web("logs") } }
            item { MoreItem(Icons.Default.Link, "Webansicht", "Das komplette Webinterface") { web("") } }
            item { HorizontalDivider() }
            item { MoreItem(Icons.Default.Lock, "App sperren", null) { app.lock() } }
            item { MoreItem(Icons.AutoMirrored.Filled.Logout, "Abmelden", "Meldet dieses Gerät beim Server ab") { confirmLogout = true } }
        }
    }
    if (confirmLogout) {
        ConfirmDialog("Abmelden?", "Danach musst du dich mit Kennwort neu anmelden. Uploads im Hintergrund werden abgebrochen.", "Abmelden", { confirmLogout = false }) {
            com.wagnermartin.telex.upload.Uploads.cancelAll(context)
            vm.logout()
        }
    }
}

@Composable
private fun MoreItem(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String, detail: String?, onClick: () -> Unit) {
    ListItem(
        leadingContent = { Icon(icon, null, tint = MaterialTheme.colorScheme.primary) },
        headlineContent = { Text(title, style = MaterialTheme.typography.titleMedium) },
        supportingContent = detail?.let { { Text(it) } },
        modifier = Modifier.clickable(onClick = onClick).padding(vertical = 2.dp),
    )
}
