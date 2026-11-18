package com.wagnermartin.telex

import android.app.DownloadManager
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.provider.DocumentsContract
import android.webkit.CookieManager
import android.webkit.MimeTypeMap
import android.widget.Toast
import androidx.core.content.FileProvider
import java.io.File
import java.net.HttpURLConnection
import java.net.URL

/** Downloads, opening files in other apps, and reading picked folders. */
object Files {

    /** Hands a download to Android's download manager, with the session cookie. */
    fun download(context: Context, url: String, userAgent: String, contentDisposition: String?, mime: String?) {
        val name = fileName(url, contentDisposition, mime)
        try {
            val request = DownloadManager.Request(Uri.parse(url))
                .addRequestHeader("User-Agent", userAgent)
                .setTitle(name)
                .setMimeType(mime?.substringBefore(';')?.trim()?.ifEmpty { null } ?: guessMime(name))
                .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
            CookieManager.getInstance().getCookie(url)?.let { request.addRequestHeader("Cookie", it) }
            context.getSystemService(DownloadManager::class.java).enqueue(request)
            Toast.makeText(context, context.getString(R.string.download_started, name), Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
            Toast.makeText(context, context.getString(R.string.download_failed, e.localizedMessage ?: name), Toast.LENGTH_LONG).show()
        }
    }

    /**
     * Fetches a file into the cache so another app can open it. Used for PDFs, which
     * the WebView cannot show. Runs on a background thread.
     */
    fun fetchToCache(context: Context, url: String, userAgent: String): Pair<File, String> {
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.setRequestProperty("User-Agent", userAgent)
        CookieManager.getInstance().getCookie(url)?.let { conn.setRequestProperty("Cookie", it) }
        conn.connectTimeout = 15_000
        conn.readTimeout = 60_000
        try {
            if (conn.responseCode !in 200..299) throw IllegalStateException("HTTP ${conn.responseCode}")
            val name = fileName(url, conn.getHeaderField("Content-Disposition"), conn.contentType)
            val dir = File(context.cacheDir, "open").apply { mkdirs() }
            dir.listFiles()?.forEach { it.delete() }
            val file = File(dir, name)
            conn.inputStream.use { input -> file.outputStream().use { input.copyTo(it) } }
            val mime = conn.contentType?.substringBefore(';')?.trim()?.ifEmpty { null } ?: guessMime(name)
            return file to mime
        } finally {
            conn.disconnect()
        }
    }

    fun open(context: Context, file: File, mime: String) {
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.files", file)
        val intent = Intent(Intent.ACTION_VIEW)
            .setDataAndType(uri, mime)
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_DOCUMENT)
        try {
            context.startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            Toast.makeText(context, R.string.no_app_for_file, Toast.LENGTH_LONG).show()
        }
    }

    /**
     * Copies files shared from another app into the cache and returns FileProvider
     * URIs whose last segment is the original name. Earlier copies are removed.
     */
    fun copyShared(context: Context, uris: List<Uri>): List<Uri> {
        val root = File(context.cacheDir, "shared")
        root.deleteRecursively()
        val out = mutableListOf<Uri>()
        uris.forEachIndexed { i, uri ->
            try {
                val name = displayName(context, uri)
                val dir = File(root, i.toString()).apply { mkdirs() }
                val file = File(dir, name)
                context.contentResolver.openInputStream(uri)!!.use { input -> file.outputStream().use { input.copyTo(it) } }
                file.setLastModified(System.currentTimeMillis())
                out += FileProvider.getUriForFile(context, "${context.packageName}.files", file)
            } catch (e: Exception) {
                android.util.Log.w("TelexShare", "cannot read $uri", e)
            }
        }
        return out
    }

    private fun displayName(context: Context, uri: Uri): String {
        val name = runCatching {
            context.contentResolver.query(uri, arrayOf(android.provider.OpenableColumns.DISPLAY_NAME), null, null, null)
                ?.use { if (it.moveToFirst()) it.getString(0) else null }
        }.getOrNull() ?: uri.lastPathSegment ?: "datei"
        return fileName("", "attachment; filename=\"${name.replace("\"", "")}\"", context.contentResolver.getType(uri))
    }

    fun fileName(url: String, contentDisposition: String?, mime: String?): String {
        val cd = contentDisposition.orEmpty()
        val extended = Regex("""filename\*\s*=\s*([^']*)'[^']*'([^;]+)""", RegexOption.IGNORE_CASE).find(cd)
        val plain = Regex("""filename\s*=\s*(?:"((?:\\.|[^"\\])*)"|([^;\s]+))""", RegexOption.IGNORE_CASE).find(cd)
        val raw = when {
            extended != null -> Uri.decode(extended.groupValues[2].trim())
            plain != null -> plain.groupValues[1].ifEmpty { plain.groupValues[2] }.replace(Regex("""\\(.)"""), "$1")
            else -> Uri.parse(url).lastPathSegment ?: "download"
        }
        var name = raw.replace(Regex("""[/\\:*?"<>|\u0000-\u001f]"""), "_").trim().trimStart('.')
        if (name.isEmpty()) name = "download"
        if (!name.contains('.')) {
            val ext = mime?.substringBefore(';')?.trim()?.let { MimeTypeMap.getSingleton().getExtensionFromMimeType(it) }
            if (ext != null) name += ".$ext"
        }
        return name
    }

    private fun guessMime(name: String) =
        MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase())
            ?: "application/octet-stream"

    /**
     * Lists every file below a folder picked with ACTION_OPEN_DOCUMENT_TREE, with its
     * path relative to the folder's parent, like webkitRelativePath ("Fotos/2024/a.jpg").
     */
    fun listTree(context: Context, tree: Uri): List<Pair<Uri, String>> {
        val resolver = context.contentResolver
        val rootId = DocumentsContract.getTreeDocumentId(tree)
        val rootName = resolver.query(
            DocumentsContract.buildDocumentUriUsingTree(tree, rootId),
            arrayOf(DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null,
        )?.use { if (it.moveToFirst()) it.getString(0) else null } ?: "Ordner"
        val out = mutableListOf<Pair<Uri, String>>()
        fun walk(docId: String, prefix: String) {
            val children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, docId)
            val cols = arrayOf(
                DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                DocumentsContract.Document.COLUMN_MIME_TYPE,
            )
            resolver.query(children, cols, null, null, null)?.use { c ->
                while (c.moveToNext()) {
                    val id = c.getString(0)
                    val name = c.getString(1) ?: continue
                    if (c.getString(2) == DocumentsContract.Document.MIME_TYPE_DIR) {
                        walk(id, "$prefix/$name")
                    } else {
                        out += DocumentsContract.buildDocumentUriUsingTree(tree, id) to "$prefix/$name"
                    }
                }
            }
        }
        walk(rootId, rootName)
        return out
    }
}
