package com.wagnermartin.telex.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.InsertDriveFile
import androidx.compose.material.icons.filled.Archive
import androidx.compose.material.icons.filled.AudioFile
import androidx.compose.material.icons.filled.Code
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Image
import androidx.compose.material.icons.filled.LockOpen
import androidx.compose.material.icons.filled.Movie
import androidx.compose.material.icons.filled.PictureAsPdf
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Sync
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import coil3.compose.SubcomposeAsyncImage
import com.wagnermartin.telex.data.Api
import com.wagnermartin.telex.data.Node
import java.text.DateFormat
import java.util.Date
import java.util.Locale

fun formatSize(n: Long): String {
    if (n < 1024) return "$n B"
    val units = listOf("KB", "MB", "GB", "TB")
    var v = n.toDouble() / 1024
    var i = 0
    while (v >= 1024 && i < units.size - 1) {
        v /= 1024
        i++
    }
    return String.format(Locale.GERMANY, if (v < 10) "%.1f %s" else "%.0f %s", v, units[i])
}

fun formatDate(unix: Long): String =
    if (unix == 0L) "" else DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT, Locale.GERMANY).format(Date(unix * 1000))

private val CODE = Regex("""\.(js|ts|go|py|c|cpp|h|cs|java|kt|rs|html?|css|json|ya?ml|sh|ps1|xml|sql)$""", RegexOption.IGNORE_CASE)
private val ARCHIVE = Regex("""\.(zip|rar|7z|tar|gz|bz2|xz|iso|apk)$""", RegexOption.IGNORE_CASE)
private val DOC = Regex("""\.(docx?|odt|rtf|txt|md|xlsx?|ods|csv|pptx?|odp)$""", RegexOption.IGNORE_CASE)

fun iconOf(n: Node): Pair<ImageVector, Color> = when {
    n.isDir -> Icons.Default.Folder to FolderYellow
    n.isImage -> Icons.Default.Image to Color(0xFF2F9E6E)
    n.isVideo -> Icons.Default.Movie to Color(0xFF8A5CF6)
    n.isAudio -> Icons.Default.AudioFile to Color(0xFFE0568A)
    n.mime == "application/pdf" -> Icons.Default.PictureAsPdf to Color(0xFFD13438)
    ARCHIVE.containsMatchIn(n.name) -> Icons.Default.Archive to Color(0xFFC98A2C)
    DOC.containsMatchIn(n.name) -> Icons.Default.Description to Color(0xFF2B7CD3)
    CODE.containsMatchIn(n.name) -> Icons.Default.Code to Color(0xFF5B6B7B)
    else -> Icons.AutoMirrored.Filled.InsertDriveFile to Color(0xFF8A8F98)
}

/** A preview from the server for photos and videos, otherwise a type icon. */
@Composable
fun NodeVisual(node: Node, api: Api?, size: Dp, modifier: Modifier = Modifier, corner: Dp = 10.dp) {
    Box(modifier.size(size), contentAlignment = Alignment.Center) {
        val (icon, tint) = iconOf(node)
        if (node.thumb && api != null) {
            SubcomposeAsyncImage(
                model = api.thumbUrl(node),
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize().clip(RoundedCornerShape(corner)).background(MaterialTheme.colorScheme.surfaceVariant),
                error = { Icon(icon, null, Modifier.size(size * 0.7f), tint = tint) },
            )
            if (node.isVideo) {
                Icon(
                    Icons.Default.PlayArrow, null,
                    Modifier.size(size * 0.38f).clip(CircleShape).background(Color.Black.copy(alpha = 0.55f)).padding(2.dp),
                    tint = Color.White,
                )
            }
        } else {
            Icon(icon, null, Modifier.size(size * 0.8f), tint = tint)
        }
        val badge = when {
            node.pending -> Icons.Default.Sync to MaterialTheme.colorScheme.primary
            node.failed -> Icons.Default.ErrorOutline to MaterialTheme.colorScheme.error
            !node.isDir && !node.encrypted -> Icons.Default.LockOpen to Color(0xFFC98A2C)
            else -> null
        }
        if (badge != null) {
            Icon(
                badge.first, null,
                Modifier.align(Alignment.BottomEnd).size((size * 0.36f).coerceAtLeast(16.dp))
                    .clip(CircleShape).background(MaterialTheme.colorScheme.surface).padding(2.dp),
                tint = badge.second,
            )
        }
    }
}

/** A dialog with one text field, for names. */
@Composable
fun NameDialog(title: String, initial: String, confirm: String, onDismiss: () -> Unit, onConfirm: (String) -> Unit) {
    val dot = initial.lastIndexOf('.').takeIf { it > 0 } ?: initial.length
    var value by remember { mutableStateOf(TextFieldValue(initial, TextRange(0, dot))) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column {
                OutlinedTextField(value, { value = it }, Modifier.fillMaxWidth().focusRequester(focus), singleLine = true)
            }
        },
        confirmButton = { TextButton({ onConfirm(value.text) }, enabled = value.text.isNotBlank()) { Text(confirm) } },
        dismissButton = { TextButton(onDismiss) { Text("Abbrechen") } },
    )
}

@Composable
fun ConfirmDialog(title: String, text: String, confirm: String, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(text) },
        confirmButton = { TextButton({ onDismiss(); onConfirm() }) { Text(confirm, color = MaterialTheme.colorScheme.error) } },
        dismissButton = { TextButton(onDismiss) { Text("Abbrechen") } },
    )
}
