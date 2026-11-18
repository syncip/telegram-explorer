package com.wagnermartin.telex

import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.webkit.MimeTypeMap
import android.widget.Toast
import androidx.core.content.FileProvider
import com.wagnermartin.telex.data.Node
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import okhttp3.Request
import java.io.File
import java.io.IOException
import kotlin.coroutines.coroutineContext

/** Getting files from the server onto the phone: downloads, opening and sending. */
object Files {

    /** Hands a download to Android's download manager, which shows it in its notification. */
    fun download(context: Context, nodes: List<Node>) {
        val app = context.applicationContext as TelexApp
        val api = app.api ?: return
        val token = app.token.value ?: return
        val (url, name) = if (nodes.size == 1 && !nodes[0].isDir) {
            api.contentUrl(nodes[0].id) to nodes[0].name
        } else {
            api.zipUrl(nodes.map { it.id }) to (if (nodes.size == 1) nodes[0].name else "Telegram Explorer") + ".zip"
        }
        val clean = safeName(name)
        try {
            val request = DownloadManager.Request(Uri.parse(url))
                .addRequestHeader("Authorization", "Bearer $token")
                .setTitle(clean)
                .setMimeType(if (clean.endsWith(".zip")) "application/zip" else mimeOf(nodes[0]))
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, clean)
            context.getSystemService(DownloadManager::class.java).enqueue(request)
            Toast.makeText(context, context.getString(R.string.download_started, clean), Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
            Toast.makeText(context, context.getString(R.string.download_failed, e.localizedMessage ?: clean), Toast.LENGTH_LONG).show()
        }
    }

    /**
     * Fetches a file into the app's cache, for opening it in or sending it to
     * another app. [progress] gets the share done, 0 to 1.
     */
    suspend fun fetch(context: Context, node: Node, progress: (Float) -> Unit = {}): File = withContext(Dispatchers.IO) {
        val app = context.applicationContext as TelexApp
        val api = app.api ?: throw IOException("Kein Server")
        val dir = File(context.cacheDir, "open/${node.id}").apply { mkdirs() }
        val file = File(dir, safeName(node.name))
        if (file.exists() && file.length() == node.size && file.lastModified() >= node.mtime * 1000) return@withContext file
        File(context.cacheDir, "open").listFiles()?.filter { it.name != node.id.toString() }?.forEach { it.deleteRecursively() }
        val tmp = File(dir, file.name + ".part")
        app.http.newCall(Request.Builder().url(api.contentUrl(node.id)).build()).execute().use { res ->
            if (!res.isSuccessful) throw IOException("HTTP ${res.code}")
            res.body.byteStream().use { input ->
                tmp.outputStream().use { out ->
                    val buf = ByteArray(256 * 1024)
                    var done = 0L
                    while (true) {
                        coroutineContext.ensureActive()
                        val n = input.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        done += n
                        if (node.size > 0) progress(done.toFloat() / node.size)
                    }
                }
            }
        }
        tmp.renameTo(file)
        file.setLastModified(node.mtime * 1000)
        file
    }

    fun uriOf(context: Context, file: File): Uri = FileProvider.getUriForFile(context, "${context.packageName}.files", file)

    /** Opens a fetched file in the app the user picks for its type. */
    fun open(context: Context, file: File, node: Node) {
        val uri = uriOf(context, file)
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, mimeOf(node))
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            (context.applicationContext as TelexApp).expectExternal()
            context.startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            Toast.makeText(context, R.string.no_app_for_file, Toast.LENGTH_LONG).show()
        }
    }

    /** Sends fetched files to another app, for example a messenger. */
    fun send(context: Context, files: List<Pair<File, Node>>) {
        if (files.isEmpty()) return
        val uris = files.map { uriOf(context, it.first) }
        val type = files.map { mimeOf(it.second) }.distinct().singleOrNull() ?: "*/*"
        val intent = if (uris.size == 1) {
            Intent(Intent.ACTION_SEND).putExtra(Intent.EXTRA_STREAM, uris[0])
        } else {
            Intent(Intent.ACTION_SEND_MULTIPLE).putParcelableArrayListExtra(Intent.EXTRA_STREAM, ArrayList(uris))
        }
        intent.setType(type).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        intent.clipData = ClipData.newRawUri(null, uris[0]).apply { uris.drop(1).forEach { addItem(ClipData.Item(it)) } }
        (context.applicationContext as TelexApp).expectExternal()
        context.startActivity(Intent.createChooser(intent, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    fun mimeOf(node: Node): String =
        node.mime.substringBefore(';').trim().ifEmpty { null }
            ?: MimeTypeMap.getSingleton().getMimeTypeFromExtension(node.name.substringAfterLast('.', "").lowercase())
            ?: "application/octet-stream"

    fun safeName(name: String): String {
        val clean = name.replace(Regex("""[/\\:*?"<>|\u0000-\u001f]"""), "_").trim().trimStart('.')
        return clean.ifEmpty { "download" }
    }
}
