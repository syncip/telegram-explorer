package com.wagnermartin.telex.upload

import android.app.Notification
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.Uri
import android.os.Build
import android.provider.OpenableColumns
import androidx.core.app.NotificationCompat
import androidx.core.content.edit
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import androidx.work.workDataOf
import com.wagnermartin.telex.MainActivity
import com.wagnermartin.telex.R
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.data.ApiException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.File
import java.io.IOException
import java.io.InputStream

/** Background uploads with WorkManager: they go on when the app is closed. */
object Uploads {
    const val TAG = "upload"
    private const val QUEUE = "uploads"
    const val KEY_URI = "uri"
    const val KEY_NAME = "name"
    const val KEY_PARENT = "parent"
    const val KEY_SIZE = "size"
    const val KEY_MTIME = "mtime"
    const val KEY_TEMP = "temp"
    const val KEY_FOLDER = "folder"
    const val PROGRESS_SENT = "sent"
    const val PROGRESS_TOTAL = "total"
    const val PROGRESS_NAME = "name"
    const val RESULT_ERROR = "error"

    data class Item(val uri: Uri, val name: String, val size: Long, val mtime: Long = 0, val temp: Boolean = false)

    /** Reads name and size of a content URI. */
    fun describe(context: Context, uri: Uri): Item? = runCatching {
        var name: String? = null
        var size = -1L
        context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                name = c.getString(0)
                if (!c.isNull(1)) size = c.getLong(1)
            }
        }
        if (uri.scheme == "file") {
            val f = File(uri.path!!)
            name = name ?: f.name
            size = f.length()
        }
        if (size < 0) size = context.contentResolver.openFileDescriptor(uri, "r")?.use { it.statSize } ?: -1
        val clean = (name ?: uri.lastPathSegment ?: "datei").replace(Regex("""[/\\\u0000-\u001f]"""), "_").trim().ifEmpty { "datei" }
        Item(uri, clean, size)
    }.getOrNull()

    /**
     * Keeps access to a picked file for the worker. Picker URIs can be kept with a
     * persistable grant; anything else (shared from another app) is copied first.
     */
    fun keep(context: Context, item: Item): Item {
        try {
            context.contentResolver.takePersistableUriPermission(item.uri, Intent.FLAG_GRANT_READ_URI_PERMISSION)
            return item
        } catch (_: SecurityException) {
        }
        val dir = File(context.filesDir, "outbox").apply { mkdirs() }
        val file = File.createTempFile("up", ".bin", dir)
        context.contentResolver.openInputStream(item.uri)!!.use { input -> file.outputStream().use { input.copyTo(it) } }
        return item.copy(uri = Uri.fromFile(file), size = file.length(), temp = true)
    }

    fun enqueue(context: Context, parent: Long, folderName: String, items: List<Item>) {
        val wm = WorkManager.getInstance(context)
        val constraints = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
        for (item in items) {
            val req = OneTimeWorkRequestBuilder<UploadWorker>()
                .setConstraints(constraints)
                .addTag(TAG)
                .setInputData(
                    workDataOf(
                        KEY_URI to item.uri.toString(), KEY_NAME to item.name, KEY_PARENT to parent,
                        KEY_SIZE to item.size, KEY_MTIME to item.mtime, KEY_TEMP to item.temp, KEY_FOLDER to folderName,
                    ),
                )
                .build()
            // One after the other, so the first files are complete soonest.
            wm.enqueueUniqueWork(QUEUE, ExistingWorkPolicy.APPEND_OR_REPLACE, req)
        }
    }

    fun cancelAll(context: Context) {
        WorkManager.getInstance(context).cancelAllWorkByTag(TAG)
    }

    fun clearFinished(context: Context) {
        WorkManager.getInstance(context).pruneWork()
    }
}

class UploadWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    private val app = context.applicationContext as TelexApp
    private val state = context.getSharedPreferences("uploads", Context.MODE_PRIVATE)

    private val uri get() = Uri.parse(inputData.getString(Uploads.KEY_URI))
    private val name get() = inputData.getString(Uploads.KEY_NAME) ?: "datei"
    private val parent get() = inputData.getLong(Uploads.KEY_PARENT, 1)
    private val size get() = inputData.getLong(Uploads.KEY_SIZE, -1)

    override suspend fun getForegroundInfo() = foreground(0, size)

    override suspend fun doWork(): Result {
        val api = app.api ?: return fail(applicationContext.getString(R.string.upload_no_server))
        if (app.token.value == null) return fail(applicationContext.getString(R.string.upload_logged_out))
        if (size < 0) return fail(applicationContext.getString(R.string.upload_unknown_size))
        runCatching { setForeground(foreground(0, size)) }
        setProgress(workDataOf(Uploads.PROGRESS_SENT to 0L, Uploads.PROGRESS_TOTAL to size, Uploads.PROGRESS_NAME to name))
        val key = id.toString()
        return try {
            var uploadId = state.getString(key, null)
            var offset = 0L
            if (uploadId != null) {
                offset = try {
                    api.uploadStatus(uploadId).received
                } catch (e: ApiException) {
                    if (e.status != 404) throw e
                    uploadId = null
                    0L
                }
            }
            if (uploadId == null) {
                uploadId = create(api, name)
                state.edit { putString(key, uploadId) }
            }
            send(api, uploadId, offset)
            state.edit { remove(key) }
            cleanup()
            app.live.poke()
            Result.success()
        } catch (e: ApiException) {
            when {
                e.status == 423 || e.status == 503 || e.status >= 500 -> retryOrFail(e.message)
                else -> {
                    state.edit { remove(key) }
                    fail(e.message ?: "Fehler ${e.status}")
                }
            }
        } catch (e: IOException) {
            retryOrFail(e.message)
        } catch (e: SecurityException) {
            fail(applicationContext.getString(R.string.upload_no_access))
        }
    }

    /** Creates the upload, adding " (2)", " (3)" … when the name is taken. */
    private suspend fun create(api: com.wagnermartin.telex.data.Api, wanted: String): String {
        val dot = wanted.lastIndexOf('.').takeIf { it > 0 } ?: wanted.length
        val stem = wanted.substring(0, dot)
        val ext = wanted.substring(dot)
        for (n in 1..50) {
            val candidate = if (n == 1) wanted else "$stem ($n)$ext"
            try {
                return api.createUpload(parent, candidate, size, inputData.getLong(Uploads.KEY_MTIME, 0)).id
            } catch (e: ApiException) {
                if (!e.conflict) throw e
            }
        }
        throw ApiException(409, applicationContext.getString(R.string.upload_name_taken))
    }

    private suspend fun send(api: com.wagnermartin.telex.data.Api, uploadId: String, start: Long) = withContext(Dispatchers.IO) {
        var offset = start
        open().use { input ->
            skipFully(input, offset)
            val buf = ByteArray(CHUNK)
            while (offset < size || size == 0L) {
                val want = minOf(CHUNK.toLong(), size - offset).toInt()
                val n = readFully(input, buf, want)
                if (n < want) throw IOException(applicationContext.getString(R.string.upload_changed))
                val req = Request.Builder().url(api.url("/uploads/$uploadId"))
                    .patch(buf.toRequestBody(OCTET, 0, n))
                    .header("X-Upload-Offset", offset.toString())
                    .build()
                app.http.newCall(req).execute().use { res ->
                    if (!res.isSuccessful) {
                        val msg = runCatching {
                            com.wagnermartin.telex.data.json.parseToJsonElement(res.body.string())
                                .let { (it as kotlinx.serialization.json.JsonObject)["error"].toString().trim('"') }
                        }.getOrNull()
                        throw ApiException(res.code, msg ?: "Fehler ${res.code}")
                    }
                }
                offset += n
                setProgress(workDataOf(Uploads.PROGRESS_SENT to offset, Uploads.PROGRESS_TOTAL to size, Uploads.PROGRESS_NAME to name))
                runCatching { setForeground(foreground(offset, size)) }
                if (size == 0L) break
            }
        }
    }

    private fun open(): InputStream =
        if (uri.scheme == "file") File(uri.path!!).inputStream()
        else applicationContext.contentResolver.openInputStream(uri) ?: throw IOException("Datei nicht lesbar")

    private fun skipFully(input: InputStream, n: Long) {
        var left = n
        val scratch = ByteArray(64 * 1024)
        while (left > 0) {
            val skipped = input.skip(left)
            if (skipped > 0) {
                left -= skipped
                continue
            }
            val r = input.read(scratch, 0, minOf(scratch.size.toLong(), left).toInt())
            if (r < 0) throw IOException(applicationContext.getString(R.string.upload_changed))
            left -= r
        }
    }

    private fun readFully(input: InputStream, buf: ByteArray, want: Int): Int {
        var total = 0
        while (total < want) {
            val r = input.read(buf, total, want - total)
            if (r < 0) break
            total += r
        }
        return total
    }

    private fun retryOrFail(msg: String?): Result =
        if (runAttemptCount < 8) Result.retry() else fail(msg ?: applicationContext.getString(R.string.upload_failed))

    private fun fail(msg: String): Result {
        cleanup()
        return Result.failure(workDataOf(Uploads.RESULT_ERROR to msg, Uploads.PROGRESS_NAME to name))
    }

    private fun cleanup() {
        if (inputData.getBoolean(Uploads.KEY_TEMP, false)) uri.path?.let { File(it).delete() }
        else runCatching { applicationContext.contentResolver.releasePersistableUriPermission(uri, Intent.FLAG_GRANT_READ_URI_PERMISSION) }
    }

    private fun foreground(sent: Long, total: Long): ForegroundInfo {
        val ctx = applicationContext
        val open = android.app.PendingIntent.getActivity(
            ctx, 0, Intent(ctx, MainActivity::class.java), android.app.PendingIntent.FLAG_IMMUTABLE,
        )
        val percent = if (total > 0) (sent * 100 / total).toInt() else 0
        val n: Notification = NotificationCompat.Builder(ctx, TelexApp.CHANNEL_UPLOADS)
            .setSmallIcon(R.drawable.ic_upload)
            .setContentTitle(ctx.getString(R.string.upload_notification, name))
            .setContentText(ctx.getString(R.string.upload_notification_to, inputData.getString(Uploads.KEY_FOLDER) ?: ""))
            .setProgress(100, percent, total <= 0)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setContentIntent(open)
            .addAction(0, ctx.getString(R.string.cancel), WorkManager.getInstance(ctx).createCancelPendingIntent(id))
            .build()
        return if (Build.VERSION.SDK_INT >= 29) ForegroundInfo(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        else ForegroundInfo(NOTIFICATION_ID, n)
    }

    private companion object {
        const val CHUNK = 8 shl 20
        const val NOTIFICATION_ID = 4711
        val OCTET = "application/octet-stream".toMediaType()
    }
}
