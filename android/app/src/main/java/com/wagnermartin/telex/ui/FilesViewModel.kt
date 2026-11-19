package com.wagnermartin.telex.ui

import android.app.Application
import androidx.core.content.edit
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.data.Api
import com.wagnermartin.telex.data.Node
import kotlinx.coroutines.FlowPreview
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.debounce
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

const val ROOT = 1L

enum class Sort(val label: String) { NAME("Name"), DATE("Datum"), SIZE("Größe"), TYPE("Typ") }

data class FilesState(
    val folder: Long = ROOT,
    val path: List<Node> = emptyList(),
    val items: List<Node> = emptyList(),
    val loading: Boolean = true,
    val refreshing: Boolean = false,
    val error: String? = null,
    val selected: Set<Long> = emptySet(),
    val query: String? = null,
    val encryptDefault: Boolean = true,
) {
    val title get() = if (query != null) "Suche" else path.lastOrNull()?.takeIf { it.id != ROOT }?.name ?: "Dateien"
    val selecting get() = selected.isNotEmpty()
    val selectedNodes get() = items.filter { it.id in selected }
}

/** The folder browser: one folder or a search, its selection and the actions on it. */
@OptIn(FlowPreview::class)
class FilesViewModel(application: Application) : AndroidViewModel(application) {
    private val app = application as TelexApp
    val api: Api? get() = app.api

    private val _state = MutableStateFlow(FilesState())
    val state: StateFlow<FilesState> = _state.asStateFlow()

    private val _sort = MutableStateFlow(Sort.entries.getOrNull(app.prefs.sp.getInt("sort", 0)) ?: Sort.NAME)
    val sort: StateFlow<Sort> = _sort.asStateFlow()

    private val _grid = MutableStateFlow(app.prefs.sp.getBoolean("grid", false))
    val grid: StateFlow<Boolean> = _grid.asStateFlow()

    private val _messages = MutableSharedFlow<String>(extraBufferCapacity = 4)

    /** Short notes for a snackbar. */
    val messages: SharedFlow<String> = _messages.asSharedFlow()

    private var loadJob: Job? = null

    init {
        viewModelScope.launch {
            app.live.files.debounce(300).collect { load(quiet = true) }
        }
        load()
    }

    fun say(text: String) {
        _messages.tryEmit(text)
    }

    fun open(folder: Long) {
        _state.update { it.copy(folder = folder, query = null, selected = emptySet(), loading = true, error = null) }
        load()
    }

    /** Goes one folder up; false at the top. */
    fun up(): Boolean {
        val s = _state.value
        if (s.query != null) {
            _state.update { it.copy(query = null) }
            load()
            return true
        }
        if (s.folder == ROOT) return false
        open(s.path.getOrNull(s.path.size - 2)?.id ?: ROOT)
        return true
    }

    fun search(q: String?) {
        _state.update { it.copy(query = q, selected = emptySet()) }
        load()
    }

    fun refresh() {
        _state.update { it.copy(refreshing = true) }
        load(quiet = true)
    }

    fun load(quiet: Boolean = false) {
        val api = app.api ?: return
        val s = _state.value
        loadJob?.cancel()
        if (!quiet) _state.update { it.copy(loading = true) }
        loadJob = viewModelScope.launch {
            try {
                if (s.query != null) {
                    val list = if (s.query.isBlank()) emptyList() else api.search(s.query)
                    _state.update { it.copy(items = list, loading = false, refreshing = false, error = null) }
                } else {
                    val info = api.node(s.folder)
                    val list = api.children(s.folder)
                    _state.update {
                        it.copy(
                            path = info.path, items = list, encryptDefault = info.encryptDefault,
                            loading = false, refreshing = false, error = null,
                            selected = it.selected.filter { id -> list.any { n -> n.id == id } }.toSet(),
                        )
                    }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                if (e is com.wagnermartin.telex.data.ApiException && e.status == 404 && s.folder != ROOT) {
                    open(ROOT)
                    return@launch
                }
                _state.update { it.copy(loading = false, refreshing = false, error = e.message ?: e.javaClass.simpleName) }
            }
        }
    }

    fun sorted(items: List<Node>, sort: Sort): List<Node> {
        val cmp = when (sort) {
            Sort.NAME -> compareBy<Node, String>(String.CASE_INSENSITIVE_ORDER) { it.name }
            Sort.DATE -> compareByDescending<Node> { it.mtime }.thenBy { it.name.lowercase() }
            Sort.SIZE -> compareByDescending<Node> { if (it.isDir) 0 else it.size }.thenBy { it.name.lowercase() }
            Sort.TYPE -> compareBy<Node> { it.name.substringAfterLast('.', "").lowercase() }.thenBy { it.name.lowercase() }
        }
        return items.sortedWith(compareByDescending<Node> { it.isDir }.then(cmp))
    }

    fun setSort(s: Sort) {
        _sort.value = s
        app.prefs.sp.edit { putInt("sort", s.ordinal) }
    }

    fun toggleGrid() {
        _grid.value = !_grid.value
        app.prefs.sp.edit { putBoolean("grid", _grid.value) }
    }

    fun toggle(id: Long) = _state.update { it.copy(selected = if (id in it.selected) it.selected - id else it.selected + id) }
    fun selectAll() = _state.update { it.copy(selected = it.items.map { n -> n.id }.toSet()) }
    fun clearSelection() = _state.update { it.copy(selected = emptySet()) }

    private fun act(done: String? = null, block: suspend (Api) -> Unit) {
        val api = app.api ?: return
        viewModelScope.launch {
            try {
                block(api)
                done?.let { say(it) }
            } catch (e: Exception) {
                say(e.message ?: "Fehler")
            }
            load(quiet = true)
        }
    }

    fun mkdir(name: String) = act("Ordner „$name“ angelegt") { it.mkdir(_state.value.folder, name.trim()) }

    /** Creates an empty text file in the open folder and hands it to the editor. */
    fun newTextFile(name: String, onCreated: (Node) -> Unit) = act {
        val node = it.newTextFile(_state.value.folder, name.trim())
        onCreated(node)
    }

    fun rename(node: Node, name: String) = act { it.rename(node.id, name.trim()); clearSelection() }

    fun move(ids: Set<Long>, target: Long, targetName: String) = act("${count(ids.size)} nach „$targetName“ verschoben") {
        it.move(ids, target)
        clearSelection()
    }

    /** Moves to the trash; the snackbar offers to undo. */
    fun delete(ids: Set<Long>, onUndo: (suspend () -> Unit) -> Unit) {
        val api = app.api ?: return
        viewModelScope.launch {
            try {
                api.delete(ids)
                clearSelection()
                onUndo { api.restore(ids); load(quiet = true) }
            } catch (e: Exception) {
                say(e.message ?: "Fehler")
            }
            load(quiet = true)
        }
    }

    suspend fun shareLink(node: Node): String? {
        val api = app.api ?: return null
        return try {
            api.shareLink(api.createShare(node.id)).also { load(quiet = true) }
        } catch (e: Exception) {
            say(e.message ?: "Fehler")
            null
        }
    }

    companion object {
        fun count(n: Int) = if (n == 1) "1 Eintrag" else "$n Einträge"
    }
}
