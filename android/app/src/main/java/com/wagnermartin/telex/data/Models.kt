package com.wagnermartin.telex.data

import kotlinx.serialization.Serializable

@Serializable
data class Node(
    val id: Long,
    val parentId: Long = 0,
    val name: String,
    val isDir: Boolean = false,
    val size: Long = 0,
    val mime: String = "",
    val encrypted: Boolean = false,
    val policy: Int = 0,
    val state: String = "ready",
    val error: String? = null,
    val sha256: String? = null,
    val md5: String? = null,
    val verifyStatus: String? = null,
    val mtime: Long = 0,
    val ctime: Long = 0,
    val trashedAt: Long = 0,
    val shared: Boolean = false,
    val segments: Long = 0,
    val fileCount: Long = 0,
    val thumb: Boolean = false,
) {
    val isImage get() = mime.startsWith("image/")
    val isVideo get() = mime.startsWith("video/")
    val isAudio get() = mime.startsWith("audio/")
    val pending get() = state == "pending"
    val failed get() = state == "failed"

    /** Changes whenever the content does, so cached previews are not reused. */
    val version get() = "$size-$mtime"
}

@Serializable
data class NodeInfo(val node: Node, val path: List<Node>, val encryptDefault: Boolean = true)

@Serializable
data class TelegramStatus(val step: String = "", val user: String? = null, val error: String? = null)

@Serializable
data class AppState(
    val needsSetup: Boolean = false,
    val loggedIn: Boolean = false,
    val locked: Boolean = false,
    val username: String? = null,
    val telegram: TelegramStatus? = null,
)

@Serializable
data class LiveState(val locked: Boolean = false, val telegram: TelegramStatus? = null)

@Serializable
data class LoginResult(val ok: Boolean = false, val token: String? = null)

@Serializable
data class Share(
    val token: String,
    val nodeId: Long,
    val name: String,
    val isDir: Boolean = false,
    val hasPassword: Boolean = false,
    val trashed: Boolean = false,
    val expiresAt: Long = 0,
    val maxDownloads: Int = 0,
    val downloads: Int = 0,
    val createdAt: Long = 0,
)

@Serializable
data class Job(
    val id: Long,
    val name: String,
    val parentId: Long = 0,
    val state: String = "",
    val error: String? = null,
    val sent: Long = 0,
    val total: Long = 0,
)

@Serializable
data class Speed(val up: Long = 0, val down: Long = 0)

@Serializable
data class Stats(
    val files: Long = 0,
    val folders: Long = 0,
    val size: Long = 0,
    val shares: Long = 0,
    val trashFiles: Long = 0,
    val trashSize: Long = 0,
    val speed: Speed = Speed(),
)

@Serializable
data class DeviceSession(
    val id: String,
    val kind: String,
    val name: String = "",
    val ip: String = "",
    val createdAt: Long = 0,
    val lastSeen: Long = 0,
    val current: Boolean = false,
)

@Serializable
data class UploadCreated(val id: String, val received: Long = 0)

@Serializable
data class UploadStatus(val id: String, val received: Long = 0, val size: Long = 0)

@Serializable
data class ShareCreated(val token: String)
