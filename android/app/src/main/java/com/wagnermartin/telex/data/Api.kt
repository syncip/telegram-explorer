package com.wagnermartin.telex.data

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.serializer
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.net.URLEncoder

/** An answer of the server other than 2xx, with its German message. */
class ApiException(val status: Int, message: String, val body: String = "") : IOException(message) {
    val unauthorized get() = status == 401
    val locked get() = status == 423
    val conflict get() = status == 409

    /** For a refused save: the id of the file that has the name now, if any. */
    val current: Long? get() = runCatching { json.parseToJsonElement(body).jsonObject["current"]?.jsonPrimitive?.contentOrNull?.toLong() }.getOrNull()
}

val json = Json {
    ignoreUnknownKeys = true
    explicitNulls = false
    encodeDefaults = true
}

private val JSON_TYPE = "application/json".toMediaType()
private val TEXT_TYPE = "text/plain; charset=utf-8".toMediaType()

/**
 * The JSON API of the server. [base] is the origin, for example
 * https://explorer.example.com; the bearer token is added by [client].
 */
class Api(val base: String, val client: OkHttpClient) {

    fun url(path: String) = "$base/api$path"
    fun contentUrl(id: Long, inline: Boolean = false) = url("/nodes/$id/content") + if (inline) "?inline=1" else ""
    fun thumbUrl(node: Node) = url("/nodes/${node.id}/thumb?v=${node.version}")
    fun zipUrl(ids: Collection<Long>) = url("/zip?ids=" + ids.joinToString(","))
    fun shareLink(token: String) = "$base/s/$token"

    suspend fun <T> request(path: String, method: String = "GET", body: RequestBody? = null, headers: Map<String, String> = emptyMap(), parse: (String) -> T): T =
        withContext(Dispatchers.IO) {
            val req = Request.Builder().url(url(path)).method(method, body ?: if (method == "GET" || method == "DELETE") null else ByteArray(0).toRequestBody())
            headers.forEach { (k, v) -> req.header(k, v) }
            client.newCall(req.build()).execute().use { res ->
                val text = res.body.string()
                if (!res.isSuccessful) {
                    val msg = runCatching { json.parseToJsonElement(text).jsonObject["error"]?.jsonPrimitive?.contentOrNull }.getOrNull()
                    throw ApiException(res.code, msg ?: "Fehler ${res.code}", text)
                }
                parse(text)
            }
        }

    private suspend inline fun <reified T> get(path: String): T = request(path) { json.decodeFromString(serializer<T>(), it) }

    private suspend inline fun <reified T> send(method: String, path: String, body: JsonElement): T =
        request(path, method, json.encodeToString(JsonElement.serializer(), body).toRequestBody(JSON_TYPE)) {
            if (T::class == Unit::class) Unit as T else json.decodeFromString(serializer<T>(), it)
        }

    private fun ids(ids: Collection<Long>) = buildJsonObject { putJsonArray("ids") { ids.forEach { add(kotlinx.serialization.json.JsonPrimitive(it)) } } }

    suspend fun state(): AppState = get("/state")

    suspend fun login(username: String, password: String, code: String, device: String): String {
        val body = buildJsonObject {
            put("username", username); put("password", password); put("code", code)
            put("app", true); put("device", device)
        }
        return send<LoginResult>("POST", "/login", body).token ?: throw ApiException(500, "Server ist zu alt für die App")
    }

    suspend fun logout() = send<Unit>("POST", "/logout", JsonObject(emptyMap()))
    suspend fun unlock(passphrase: String) = send<Unit>("POST", "/unlock", buildJsonObject { put("passphrase", passphrase) })

    suspend fun node(id: Long): NodeInfo = get("/nodes/$id")
    suspend fun children(id: Long): List<Node> = get("/nodes/$id/children")
    suspend fun search(q: String): List<Node> = get("/search?q=" + URLEncoder.encode(q, "UTF-8"))
    suspend fun mkdir(parent: Long, name: String): Node = send("POST", "/nodes/$parent/mkdir", buildJsonObject { put("name", name) })
    suspend fun rename(id: Long, name: String) = send<Unit>("PATCH", "/nodes/$id", buildJsonObject { put("name", name) })
    suspend fun move(ids: Collection<Long>, target: Long) =
        send<Unit>("POST", "/move", buildJsonObject { putJsonArray("ids") { ids.forEach { add(kotlinx.serialization.json.JsonPrimitive(it)) } }; put("target", target) })
    suspend fun delete(ids: Collection<Long>) = send<Unit>("POST", "/delete", ids(ids))

    suspend fun trash(): List<Node> = get("/trash")
    suspend fun restore(ids: Collection<Long>) = send<Unit>("POST", "/trash/restore", ids(ids))
    suspend fun purge(ids: Collection<Long>) = send<Unit>("POST", "/trash/purge", ids(ids))

    suspend fun shares(): List<Share> = get("/shares")
    suspend fun createShare(nodeId: Long): String = send<ShareCreated>("POST", "/shares", buildJsonObject { put("nodeId", nodeId) }).token
    suspend fun deleteShare(token: String) = send<Unit>("DELETE", "/shares/$token", JsonObject(emptyMap()))

    /** The raw bytes of a small file, for the text editor. */
    suspend fun bytes(id: Long): ByteArray = withContext(Dispatchers.IO) {
        client.newCall(Request.Builder().url(contentUrl(id)).build()).execute().use { res ->
            if (!res.isSuccessful) {
                val text = res.body.string()
                val msg = runCatching { json.parseToJsonElement(text).jsonObject["error"]?.jsonPrimitive?.contentOrNull }.getOrNull()
                throw ApiException(res.code, msg ?: "Fehler ${res.code}", text)
            }
            res.body.bytes()
        }
    }

    /**
     * Saves edited text as a new version of [id]. A 409 whose body names the
     * current file means it changed elsewhere; [force] replaces it anyway.
     */
    suspend fun saveText(id: Long, content: ByteArray, force: Boolean = false): Node =
        request("/nodes/$id/content" + if (force) "?force=1" else "", "PUT", content.toRequestBody(TEXT_TYPE)) {
            json.decodeFromString(Node.serializer(), it)
        }

    suspend fun newTextFile(parent: Long, name: String): Node = send("POST", "/nodes/$parent/textfile", buildJsonObject { put("name", name) })

    suspend fun jobs(): List<Job> = get("/jobs")
    suspend fun stats(): Stats = get("/stats")
    suspend fun sessions(): List<DeviceSession> = get("/sessions")

    suspend fun createUpload(parent: Long, name: String, size: Long, mtime: Long): UploadCreated =
        send("POST", "/uploads", buildJsonObject { put("parentId", parent); put("name", name); put("size", size); put("mtime", mtime) })
    suspend fun uploadStatus(id: String): UploadStatus = get("/uploads/$id")
    suspend fun cancelUpload(id: String) = send<Unit>("DELETE", "/uploads/$id", JsonObject(emptyMap()))

    companion object {
        /** Normalizes user input to an origin, or returns null. */
        fun origin(input: String): String? = runCatching {
            val text = input.trim().let { if (it.contains("://")) it else "https://$it" }
            val u = text.toHttpUrl()
            val port = if (u.port == okhttp3.HttpUrl.defaultPort(u.scheme)) "" else ":${u.port}"
            val host = if (u.host.contains(':')) "[${u.host}]" else u.host
            "${u.scheme}://$host$port"
        }.getOrNull()
    }
}
