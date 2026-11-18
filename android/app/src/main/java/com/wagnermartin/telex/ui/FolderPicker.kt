package com.wagnermartin.telex.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.CreateNewFolder
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.wagnermartin.telex.data.Api
import com.wagnermartin.telex.data.Node
import kotlinx.coroutines.launch

/**
 * A full-screen folder chooser, for moving files and as the target of files
 * shared from other apps. [exclude] keeps a folder from being moved into itself.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun FolderPicker(
    api: Api,
    title: String,
    confirm: String,
    start: Long = ROOT,
    exclude: Set<Long> = emptySet(),
    onDismiss: () -> Unit,
    onPick: (folder: Node) -> Unit,
) {
    val scope = rememberCoroutineScope()
    var folder by remember { mutableStateOf(start) }
    var path by remember { mutableStateOf<List<Node>>(emptyList()) }
    var folders by remember { mutableStateOf<List<Node>?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var naming by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(0) }

    LaunchedEffect(folder, reload) {
        folders = null
        try {
            val info = api.node(folder)
            path = info.path
            folders = api.children(folder).filter { it.isDir }.sortedBy { it.name.lowercase() }
            error = null
        } catch (e: Exception) {
            if (folder != ROOT) folder = ROOT else error = e.message
        }
    }

    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        BackHandler { if (folder != ROOT) folder = path.getOrNull(path.size - 2)?.id ?: ROOT else onDismiss() }
        Surface(Modifier.fillMaxSize()) {
            Scaffold(
                topBar = {
                    TopAppBar(
                        title = { Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                        navigationIcon = { IconButton(onDismiss) { Icon(Icons.Default.Close, "Abbrechen") } },
                        actions = { IconButton({ naming = true }) { Icon(Icons.Default.CreateNewFolder, "Neuer Ordner") } },
                    )
                },
                bottomBar = {
                    Surface(tonalElevation = 3.dp) {
                        Row(
                            Modifier.fillMaxWidth().navigationBarsPadding().padding(16.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(12.dp),
                        ) {
                            TextButton(onDismiss, Modifier.height(56.dp)) { Text("Abbrechen") }
                            Button(
                                { path.lastOrNull()?.let(onPick) },
                                Modifier.weight(1f).height(56.dp),
                                enabled = path.isNotEmpty() && folder !in exclude,
                            ) { Text(confirm, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                        }
                    }
                },
            ) { padding ->
                Column(Modifier.padding(padding).fillMaxSize()) {
                    Breadcrumb(path) { folder = it }
                    HorizontalDivider()
                    val list = folders
                    when {
                        error != null -> Text(error!!, Modifier.padding(24.dp), color = MaterialTheme.colorScheme.error)
                        list == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                        list.isEmpty() -> Text("Keine Unterordner", Modifier.padding(24.dp), color = MaterialTheme.colorScheme.onSurfaceVariant)
                        else -> LazyColumn(Modifier.fillMaxSize()) {
                            items(list, key = { it.id }) { f ->
                                val disabled = f.id in exclude
                                ListItem(
                                    headlineContent = { Text(f.name, maxLines = 2, overflow = TextOverflow.Ellipsis) },
                                    leadingContent = { Icon(Icons.Default.Folder, null, Modifier.size(36.dp), tint = FolderYellow.copy(alpha = if (disabled) 0.4f else 1f)) },
                                    trailingContent = { Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null) },
                                    modifier = Modifier.clickable(enabled = !disabled) { folder = f.id }.padding(vertical = 4.dp),
                                )
                            }
                        }
                    }
                }
            }
        }
    }

    if (naming) {
        NameDialog("Neuer Ordner", "Neuer Ordner", "Anlegen", { naming = false }) { name ->
            naming = false
            scope.launch {
                try {
                    folder = api.mkdir(folder, name.trim()).id
                } catch (e: Exception) {
                    error = e.message
                    reload++
                }
            }
        }
    }
}

/** The path to the current folder as tappable steps, scrolled to the end. */
@Composable
fun Breadcrumb(path: List<Node>, onOpen: (Long) -> Unit) {
    val state = rememberLazyListState()
    LaunchedEffect(path.size) { if (path.isNotEmpty()) state.scrollToItem(path.size - 1) }
    LazyRow(state = state, modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        itemsIndexed(path, key = { _, n -> n.id }) { i, n ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (i > 0) Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                TextButton({ onOpen(n.id) }, enabled = i < path.size - 1) {
                    Text(if (n.id == ROOT) "Dateien" else n.name, maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.titleSmall)
                }
            }
        }
        item { Spacer(Modifier.size(8.dp)) }
    }
}
