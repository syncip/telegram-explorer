package com.wagnermartin.telex

import android.content.Context
import android.content.SharedPreferences
import android.net.Uri
import androidx.core.content.edit
import androidx.preference.PreferenceManager

/** App settings. The keys match res/xml/preferences.xml. */
class Prefs(context: Context) {
    val sp: SharedPreferences = PreferenceManager.getDefaultSharedPreferences(context.applicationContext)

    init {
        PreferenceManager.setDefaultValues(context.applicationContext, R.xml.preferences, false)
    }

    /** Base address without a trailing slash, for example https://explorer.example.com. */
    var serverUrl: String?
        get() = sp.getString(KEY_SERVER, null)
        set(value) = sp.edit { putString(KEY_SERVER, value) }

    /** scheme://host[:port] of the server, the only origin the bridge talks to. */
    val serverOrigin: String?
        get() = serverUrl?.let { origin(Uri.parse(it)) }

    val appLock: Boolean get() = sp.getBoolean(KEY_APP_LOCK, true)
    val lockTimeoutMillis: Long get() = (sp.getString(KEY_LOCK_TIMEOUT, "0")?.toLongOrNull() ?: 0L) * 1000
    val allowDeviceCredential: Boolean get() = sp.getBoolean(KEY_DEVICE_CREDENTIAL, false)
    val secureScreen: Boolean get() = sp.getBoolean(KEY_SECURE, true)
    var bioOffer: Boolean
        get() = sp.getBoolean(KEY_BIO_OFFER, true)
        set(value) = sp.edit { putBoolean(KEY_BIO_OFFER, value) }
    val bioAuto: Boolean get() = sp.getBoolean(KEY_BIO_AUTO, true)
    val textZoom: Int get() = sp.getString(KEY_TEXT_ZOOM, "100")?.toIntOrNull() ?: 100

    companion object {
        const val KEY_SERVER = "server_url"
        const val KEY_APP_LOCK = "app_lock"
        const val KEY_LOCK_TIMEOUT = "lock_timeout"
        const val KEY_DEVICE_CREDENTIAL = "device_credential"
        const val KEY_SECURE = "secure_screen"
        const val KEY_BIO_OFFER = "bio_offer"
        const val KEY_BIO_AUTO = "bio_auto"
        const val KEY_TEXT_ZOOM = "text_zoom"

        fun origin(uri: Uri): String? {
            val scheme = uri.scheme?.lowercase() ?: return null
            val host = uri.host?.lowercase() ?: return null
            val port = if (uri.port == -1) "" else ":${uri.port}"
            val h = if (host.contains(':')) "[$host]" else host
            return "$scheme://$h$port"
        }
    }
}
