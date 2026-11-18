package com.wagnermartin.telex.ui

import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Send
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Link
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Folder
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.Menu
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import androidx.lifecycle.viewmodel.compose.viewModel
import com.wagnermartin.telex.Biometrics
import com.wagnermartin.telex.Files
import com.wagnermartin.telex.R
import com.wagnermartin.telex.WebAdminActivity
import com.wagnermartin.telex.data.Node
import com.wagnermartin.telex.upload.Uploads
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/** Everything the app shows: the gate screens, then the tabs, with the lock on top. */
@Composable
fun Root(vm: AppViewModel) {
    val gate by vm.gate.collectAsState()
    val locked by vm.app.locked.collectAsState()
    var changingServer by rememberSaveable { mutableStateOf(false) }

    Box(Modifier.fillMaxSize()) {
        Surface(Modifier.fillMaxSize()) {
            when (val g = gate) {
                Gate.Loading -> LoadingScreen()
                Gate.NoServer -> ServerScreen(vm)
                Gate.Login -> if (changingServer) ServerScreen(vm) { changingServer = false } else LoginScreen(vm) { changingServer = true }
                Gate.Unlock -> UnlockScreen(vm)
                Gate.NeedsSetup -> NeedsSetup(vm)
                is Gate.Telegram -> TelegramWait(g)
                is Gate.Offline -> Offline(vm, g.message)
                Gate.Ready -> Main(vm)
            }
        }
        if (locked) LockScreen(vm)
    }
}

@Composable
private fun NeedsSetup(vm: AppViewModel) {
    val context = LocalContext.current
    InfoScreen(Icons.Default.Lock, "Noch nicht eingerichtet", "Richte den Telegram Explorer zuerst im Browser ein. Den Einrichtungscode findest du im Log des Containers.") {
        Button({ context.startActivity(Intent(Intent.ACTION_VIEW, android.net.Uri.parse(vm.app.prefs.serverUrl))) }, Modifier.fillMaxWidth().height(56.dp)) { Text("Im Browser öffnen") }
        TextButton({ vm.refresh() }) { Text("Erneut prüfen") }
    }
}

@Composable
private fun TelegramWait(g: Gate.Telegram) {
    val context = LocalContext.current
    val connecting = g.status?.step == "connecting" || g.status == null
    InfoScreen(
        Icons.AutoMirrored.Outlined.Send,
        if (connecting) "Verbinde mit Telegram …" else "Telegram-Anmeldung fehlt",
        g.status?.error ?: if (connecting) "Einen Moment, der Server verbindet sich." else "Melde den Server einmal in der Webansicht bei Telegram an (Telefonnummer und Code).",
    ) {
        if (connecting) LinearProgressIndicator(Modifier.fillMaxWidth())
        else Button({ context.startActivity(Intent(context, WebAdminActivity::class.java)) }, Modifier.fillMaxWidth().height(56.dp)) { Text("Webansicht öffnen") }
    }
}

@Composable
private fun Offline(vm: AppViewModel, message: String) {
    InfoScreen(Icons.Default.CloudOff, "Server nicht erreichbar", message) {
        Button({ vm.refresh() }, Modifier.fillMaxWidth().height(56.dp)) { Text("Erneut versuchen") }
        OutlinedButton({ vm.app.prefs.serverUrl = null; vm.app.serverChanged(); vm.refresh() }, Modifier.fillMaxWidth().height(56.dp)) { Text("Server ändern") }
    }
}

@Composable
private fun LockScreen(vm: AppViewModel) {
    val activity = LocalContext.current as FragmentActivity
    val app = vm.app
    fun prompt() {
        val auth = Biometrics.appLockAuthenticators(activity, app.prefs)
        if (auth == null) {
            app.unlockApp()
            return
        }
        Biometrics.prompt(activity, activity.getString(R.string.lock_prompt_title), activity.getString(R.string.lock_prompt_subtitle), auth, onSuccess = { app.unlockApp() })
    }
    LaunchedEffect(Unit) { prompt() }
    Surface(Modifier.fillMaxSize()) {
        InfoScreen(Icons.Default.Lock, "Telegram Explorer ist gesperrt", "Mit Fingerabdruck entsperren") {
            Button({ prompt() }, Modifier.fillMaxWidth().height(56.dp)) { Text("Entsperren") }
        }
    }
}

private enum class Tab(val label: String, val icon: androidx.compose.ui.graphics.vector.ImageVector, val selectedIcon: androidx.compose.ui.graphics.vector.ImageVector) {
    FILES("Dateien", Icons.Outlined.Folder, Icons.Default.Folder),
    SHARES("Freigaben", Icons.Outlined.Link, Icons.Default.Link),
    TRASH("Papierkorb", Icons.Outlined.Delete, Icons.Default.Delete),
    MORE("Mehr", Icons.Outlined.Menu, Icons.Default.Menu),
}

@Composable
private fun Main(vm: AppViewModel) {
    val context = LocalContext.current
    val files: FilesViewModel = viewModel()
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var tab by rememberSaveable { mutableStateOf(Tab.FILES) }
    var viewing by remember { mutableStateOf<Pair<List<Node>, Node>?>(null) }
    var info by remember { mutableStateOf<Node?>(null) }
    val shared by vm.shared.collectAsState()
    val stats by vm.app.live.stats.collectAsState()

    // Fetching a file for another app, with a dialog that can cancel it.
    var fetching by remember { mutableStateOf<Node?>(null) }
    var fetchProgress by remember { mutableFloatStateOf(0f) }
    var fetchJob by remember { mutableStateOf<Job?>(null) }
    val fetch: suspend (Node) -> File? = { node ->
        fetching = node
        fetchProgress = 0f
        try {
            fetchJob = currentCoroutineContext()[Job]
            Files.fetch(context, node) { p -> fetchProgress = p }
        } catch (e: CancellationException) {
            null
        } catch (e: Exception) {
            snackbar.showSnackbar("„${node.name}“ konnte nicht geladen werden: ${e.message}")
            null
        } finally {
            fetching = null
            fetchJob = null
        }
    }

    // A pending fingerprint offer from the login comes up once the files show.
    var offer by remember { mutableStateOf(pendingLogin) }
    LaunchedEffect(Unit) { pendingLogin = null }

    Scaffold(
        // The files tab shows the notes itself, so they sit above its add button.
        snackbarHost = { if (tab != Tab.FILES) SnackbarHost(snackbar) },
        bottomBar = {
            NavigationBar {
                Tab.entries.forEach { t ->
                    NavigationBarItem(
                        selected = tab == t,
                        onClick = { if (tab == t && t == Tab.FILES) files.open(ROOT) else tab = t },
                        icon = {
                            val trash = stats?.trashFiles ?: 0
                            if (t == Tab.TRASH && trash > 0) BadgedBox({ Badge { Text("$trash") } }) { Icon(if (tab == t) t.selectedIcon else t.icon, null) }
                            else Icon(if (tab == t) t.selectedIcon else t.icon, null)
                        },
                        label = { Text(t.label) },
                    )
                }
            }
        },
    ) { padding ->
        Box(Modifier.padding(padding).fillMaxSize()) {
            when (tab) {
                Tab.FILES -> FilesScreen(files, snackbar, onOpen = { n, list ->
                    if (viewable(n)) viewing = list.filter(::viewable) to n
                    else scope.launch { fetch(n)?.let { Files.open(context, it, n) } }
                }, fetch = fetch)
                Tab.SHARES -> SharesScreen(snackbar)
                Tab.TRASH -> TrashScreen(snackbar)
                Tab.MORE -> MoreScreen(vm)
            }
        }
    }

    viewing?.let { (list, start) -> Viewer(list, start, fetch, onInfo = { info = it }) { viewing = null } }
    info?.let { n -> PropertiesSheet(n, files.api) { info = null } }

    fetching?.let { n ->
        AlertDialog(
            onDismissRequest = {},
            title = { Text("Wird geladen") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text(n.name)
                    LinearProgressIndicator({ fetchProgress }, Modifier.fillMaxWidth())
                    Text("${formatSize((fetchProgress * n.size).toLong())} von ${formatSize(n.size)}", style = MaterialTheme.typography.bodySmall)
                }
            },
            confirmButton = { TextButton({ fetchJob?.cancel() }) { Text("Abbrechen") } },
        )
    }

    if (shared.isNotEmpty()) {
        val api = files.api
        if (api != null) {
            FolderPicker(
                api,
                if (shared.size == 1) "Speichern unter …" else "${shared.size} Dateien speichern unter …",
                "Hier hochladen",
                files.state.collectAsState().value.folder,
                onDismiss = { vm.shareDone() },
            ) { folder ->
                val uris = shared
                vm.shareDone()
                scope.launch {
                    val items = withContext(Dispatchers.IO) {
                        uris.mapNotNull { Uploads.describe(context, it) }.mapNotNull { runCatching { Uploads.keep(context, it) }.getOrNull() }
                    }
                    if (items.isEmpty()) {
                        snackbar.showSnackbar(context.getString(R.string.share_read_failed))
                        return@launch
                    }
                    if (items.size < uris.size) snackbar.showSnackbar(context.getString(R.string.share_read_failed))
                    val name = if (folder.id == ROOT) "Dateien" else folder.name
                    Uploads.enqueue(context, folder.id, name, items)
                    tab = Tab.FILES
                    files.open(folder.id)
                    snackbar.showSnackbar(if (items.size == 1) "„${items[0].name}“ wird nach „$name“ hochgeladen" else "${items.size} Dateien werden nach „$name“ hochgeladen")
                }
            }
        }
    }

    offer?.let { s -> SaveSecretsDialog(vm, s) { offer = null } }
}
