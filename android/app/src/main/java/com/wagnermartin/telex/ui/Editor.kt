package com.wagnermartin.telex.ui

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.TextDecrease
import androidx.compose.material.icons.filled.TextIncrease
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.wagnermartin.telex.data.Api
import com.wagnermartin.telex.data.ApiException
import com.wagnermartin.telex.data.Node
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.nio.ByteBuffer
import java.nio.charset.CharacterCodingException
import java.nio.charset.CodingErrorAction

private val EDITABLE = Regex(
    """.*\.(txt|text|md|markdown|csv|tsv|log|json|ya?ml|toml|ini|cfg|conf|env|properties|xml|html?|css|scss|js|mjs|cjs|ts|go|py|sh|bash|ps1|bat|cmd|sql|c|cpp|h|hpp|cs|java|kt|rs|php|rb|lua|srt|vtt|gitignore|dockerignore)""",
    RegexOption.IGNORE_CASE,
)
private val EDITABLE_NAME = Regex("""\.env(\..*)?|dockerfile|makefile|readme""", RegexOption.IGNORE_CASE)
private val PROSE = Regex(""".*\.(txt|text|md|markdown)|readme""", RegexOption.IGNORE_CASE)

/** Text on a phone stays smaller than in the browser, the field gets slow above. */
const val EDIT_LIMIT = 2L shl 20

/** Notes, data and code files the app opens in its own text editor. */
fun editable(n: Node) = !n.isDir && (EDITABLE.matches(n.name) || EDITABLE_NAME.matches(n.name))

/** The text of a file plus what it takes to write it back byte for byte. */
private class Loaded(val text: String, val bom: Boolean, val crlf: Boolean)

private fun decode(bytes: ByteArray): Loaded {
    val bom = bytes.size >= 3 && bytes[0] == 0xEF.toByte() && bytes[1] == 0xBB.toByte() && bytes[2] == 0xBF.toByte()
    val text = try {
        Charsets.UTF_8.newDecoder()
            .onMalformedInput(CodingErrorAction.REPORT)
            .onUnmappableCharacter(CodingErrorAction.REPORT)
            .decode(ByteBuffer.wrap(bytes, if (bom) 3 else 0, bytes.size - if (bom) 3 else 0))
            .toString()
    } catch (e: CharacterCodingException) {
        throw IllegalStateException("Die Datei ist kein Text in UTF-8 und lässt sich hier nicht bearbeiten.")
    }
    val crlf = text.contains("\r\n")
    // The text field works with \n only; Windows line ends come back on save.
    return Loaded(if (crlf) text.replace("\r\n", "\n") else text, bom, crlf)
}

private fun encode(text: String, l: Loaded): ByteArray {
    val body = (if (l.crlf) text.replace("\n", "\r\n") else text).toByteArray(Charsets.UTF_8)
    return if (l.bom) byteArrayOf(0xEF.toByte(), 0xBB.toByte(), 0xBF.toByte()) + body else body
}

/**
 * A plain full-screen text editor in large type. Saving stores a new version
 * on the server; the previous one goes to the trash.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TextEditor(opened: Node, api: Api, onSaved: () -> Unit, onClose: () -> Unit) {
    val scope = rememberCoroutineScope()
    var node by remember { mutableStateOf(opened) }
    var loaded by remember { mutableStateOf<Loaded?>(null) }
    var text by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var saving by remember { mutableStateOf(false) }
    var saved by remember { mutableStateOf(false) }
    var asking by remember { mutableStateOf(false) }
    var conflict by remember { mutableStateOf<ApiException?>(null) }
    // Set when the save came from the close prompt, so overwriting closes too.
    var closeAfterConflict by remember { mutableStateOf(false) }
    var fontSize by rememberSaveable { mutableIntStateOf(18) }
    var reload by remember { mutableIntStateOf(0) }
    val dirty = loaded != null && text != loaded!!.text
    val prose = PROSE.matches(node.name)

    // Keyed on reload only: a save changes node.id but must not reload the text.
    LaunchedEffect(reload) {
        loaded = null
        error = null
        if (node.size > EDIT_LIMIT) {
            error = "Die Datei ist ${formatSize(node.size)} groß. Die App bearbeitet Dateien bis ${formatSize(EDIT_LIMIT)}, größere im Browser."
            return@LaunchedEffect
        }
        try {
            val l = withContext(Dispatchers.Default) { decode(api.bytes(node.id)) }
            text = l.text
            loaded = l
            saved = false
        } catch (e: Exception) {
            error = e.message ?: "Die Datei lässt sich nicht laden."
        }
    }

    fun save(force: Boolean = false, closeAfter: Boolean = false) {
        val l = loaded ?: return
        if (saving) return
        val content = text
        saving = true
        error = null
        scope.launch {
            try {
                val bytes = withContext(Dispatchers.Default) { encode(content, l) }
                node = api.saveText(node.id, bytes, force)
                // The saved text is the new baseline; the id changed, so keep it from reloading.
                loaded = Loaded(content, l.bom, l.crlf)
                saved = true
                onSaved()
                if (closeAfter) onClose()
            } catch (e: ApiException) {
                when {
                    e.conflict && !force && e.current != null -> {
                        closeAfterConflict = closeAfter
                        conflict = e
                    }
                    e.locked -> error = "Der Tresor ist gesperrt. Entsperre ihn und speichere dann noch einmal."
                    else -> error = e.message
                }
            } catch (e: Exception) {
                error = e.message ?: "Speichern fehlgeschlagen"
            } finally {
                saving = false
            }
        }
    }

    fun close() = if (dirty) asking = true else onClose()

    BackHandler { close() }

    Surface(Modifier.fillMaxSize()) {
        Scaffold(
            topBar = {
                TopAppBar(
                    navigationIcon = { IconButton(::close) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Schließen") } },
                    title = {
                        Column {
                            Text(node.name, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(
                                when {
                                    saving -> "Wird gespeichert …"
                                    dirty -> "Nicht gespeichert"
                                    saved -> "Gespeichert"
                                    else -> formatSize(node.size)
                                },
                                style = MaterialTheme.typography.bodySmall,
                                color = if (dirty) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    },
                    actions = {
                        IconButton({ fontSize = (fontSize - 2).coerceAtLeast(12) }) { Icon(Icons.Default.TextDecrease, "Schrift kleiner") }
                        IconButton({ fontSize = (fontSize + 2).coerceAtMost(32) }) { Icon(Icons.Default.TextIncrease, "Schrift größer") }
                        Button({ save() }, enabled = dirty && !saving, modifier = Modifier.padding(end = 8.dp)) { Text("Speichern") }
                    },
                )
            },
        ) { padding ->
            Column(Modifier.padding(padding).consumeWindowInsets(padding).imePadding().fillMaxSize()) {
                error?.let {
                    Surface(color = MaterialTheme.colorScheme.errorContainer, modifier = Modifier.fillMaxWidth()) {
                        Text(it, Modifier.padding(16.dp, 10.dp), color = MaterialTheme.colorScheme.onErrorContainer)
                    }
                }
                when {
                    loaded != null -> BasicTextField(
                        text, { text = it },
                        Modifier.fillMaxSize().padding(16.dp),
                        textStyle = TextStyle(
                            fontSize = fontSize.sp,
                            lineHeight = (fontSize * 1.45f).sp,
                            fontFamily = if (prose) FontFamily.Default else FontFamily.Monospace,
                            color = MaterialTheme.colorScheme.onSurface,
                        ),
                        cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                        keyboardOptions = KeyboardOptions(
                            capitalization = if (prose) KeyboardCapitalization.Sentences else KeyboardCapitalization.None,
                            autoCorrectEnabled = prose,
                        ),
                    )
                    error == null -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
                }
            }
        }
    }

    if (asking) {
        AlertDialog(
            onDismissRequest = { asking = false },
            title = { Text("Änderungen speichern?") },
            text = { Text("„${node.name}“ hat Änderungen, die noch nicht gespeichert sind.") },
            confirmButton = { TextButton({ asking = false; save(closeAfter = true) }) { Text("Speichern") } },
            dismissButton = {
                TextButton({ asking = false; onClose() }) { Text("Verwerfen", color = MaterialTheme.colorScheme.error) }
            },
        )
    }

    conflict?.let { c ->
        AlertDialog(
            onDismissRequest = { conflict = null },
            title = { Text("Datei wurde geändert") },
            text = { Text("${c.message} Deine Fassung ist noch nicht gespeichert. Beim Überschreiben wandert die andere Fassung in den Papierkorb.") },
            confirmButton = { TextButton({ conflict = null; save(force = true, closeAfter = closeAfterConflict) }) { Text("Überschreiben") } },
            dismissButton = {
                Row {
                    TextButton({ conflict = null }) { Text("Abbrechen") }
                    TextButton({
                        conflict = null
                        scope.launch {
                            try {
                                node = api.node(c.current!!).node
                                reload++
                            } catch (e: Exception) {
                                error = "Die andere Fassung lässt sich nicht laden: ${e.message}"
                            }
                        }
                    }) { Text("Andere Fassung laden") }
                }
            },
        )
    }
}
