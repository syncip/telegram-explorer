package com.wagnermartin.telex.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudUpload
import androidx.compose.material.icons.filled.ErrorOutline
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.work.WorkInfo
import androidx.work.WorkManager
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.upload.Uploads

/**
 * Shows what is on its way: uploads from the phone to the server, then from the
 * server to Telegram, and uploads that failed.
 */
@Composable
fun TransferBar(modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val app = context.applicationContext as TelexApp
    val wm = remember { WorkManager.getInstance(context) }
    val infos by wm.getWorkInfosByTagFlow(Uploads.TAG).collectAsState(emptyList())
    val jobs by app.live.jobs.collectAsState()

    val running = infos.firstOrNull { it.state == WorkInfo.State.RUNNING }
    val waiting = infos.count { it.state == WorkInfo.State.ENQUEUED || it.state == WorkInfo.State.BLOCKED }
    val failed = infos.filter { it.state == WorkInfo.State.FAILED }
    val active = jobs.filter { it.state == "pending" }

    AnimatedVisibility(running != null || waiting > 0 || failed.isNotEmpty() || active.isNotEmpty(), modifier) {
        Surface(tonalElevation = 2.dp, modifier = Modifier.fillMaxWidth()) {
            Column(Modifier.padding(horizontal = 16.dp, vertical = 10.dp)) {
                if (running != null || waiting > 0) {
                    val sent = running?.progress?.getLong(Uploads.PROGRESS_SENT, 0) ?: 0
                    val total = running?.progress?.getLong(Uploads.PROGRESS_TOTAL, 0) ?: 0
                    val name = running?.progress?.getString(Uploads.PROGRESS_NAME)
                    Line(Icons.Default.CloudUpload, buildString {
                        append(if (name != null) "Hochladen: $name" else "Wartet auf Netz …")
                        if (waiting > 0) append(" · $waiting weitere")
                    }, action = "Abbrechen") { Uploads.cancelAll(context) }
                    if (total > 0) {
                        LinearProgressIndicator({ sent.toFloat() / total }, Modifier.fillMaxWidth().padding(top = 6.dp))
                        Text("${formatSize(sent)} von ${formatSize(total)}", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    } else {
                        LinearProgressIndicator(Modifier.fillMaxWidth().padding(top = 6.dp))
                    }
                }
                if (active.isNotEmpty()) {
                    if (running != null || waiting > 0) Spacer(Modifier.height(8.dp))
                    val sent = active.sumOf { it.sent }
                    val total = active.sumOf { it.total }.coerceAtLeast(1)
                    Line(Icons.AutoMirrored.Filled.Send, if (active.size == 1) "Geht zu Telegram: ${active[0].name}" else "${active.size} Dateien gehen zu Telegram")
                    LinearProgressIndicator({ sent.toFloat() / total }, Modifier.fillMaxWidth().padding(top = 6.dp))
                }
                if (failed.isNotEmpty()) {
                    val first = failed.first().outputData
                    Line(
                        Icons.Default.ErrorOutline,
                        "${first.getString(Uploads.PROGRESS_NAME) ?: "Upload"}: ${first.getString(Uploads.RESULT_ERROR) ?: "fehlgeschlagen"}" +
                            if (failed.size > 1) " (und ${failed.size - 1} weitere)" else "",
                        error = true, action = "Ausblenden",
                    ) { Uploads.clearFinished(context) }
                }
            }
        }
    }
}

@Composable
private fun Line(icon: androidx.compose.ui.graphics.vector.ImageVector, text: String, error: Boolean = false, action: String? = null, onAction: () -> Unit = {}) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, Modifier.size(20.dp), tint = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary)
        Spacer(Modifier.size(10.dp))
        Text(text, Modifier.weight(1f), maxLines = 2, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium,
            color = if (error) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface)
        if (action != null) TextButton(onAction) { Text(action) }
    }
}
