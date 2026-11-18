package com.wagnermartin.telex

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import coil3.ImageLoader
import coil3.PlatformContext
import coil3.SingletonImageLoader
import coil3.disk.DiskCache
import coil3.disk.directory
import coil3.memory.MemoryCache
import coil3.network.okhttp.OkHttpNetworkFetcherFactory
import com.wagnermartin.telex.data.Api
import com.wagnermartin.telex.data.Live
import com.wagnermartin.telex.data.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import okhttp3.OkHttpClient
import java.util.concurrent.TimeUnit

class TelexApp : Application(), SingletonImageLoader.Factory {
    lateinit var prefs: Prefs
        private set
    lateinit var tokens: TokenStore
        private set
    lateinit var secrets: SecretStore
        private set
    lateinit var http: OkHttpClient
        private set
    lateinit var live: Live
        private set

    private val _token = MutableStateFlow<String?>(null)

    /** The login token for the current server, null when logged out. */
    val token: StateFlow<String?> = _token.asStateFlow()

    private val _locked = MutableStateFlow(false)

    /** True while the app lock covers the screen. */
    val locked: StateFlow<Boolean> = _locked.asStateFlow()
    private var backgroundAt = 0L
    private var graceUntil = 0L

    private var cachedApi: Api? = null

    /** The API of the configured server, or null before one is set. */
    val api: Api?
        get() {
            val origin = prefs.serverUrl ?: return null
            cachedApi?.takeIf { it.base == origin }?.let { return it }
            return Api(origin, http).also { cachedApi = it }
        }

    override fun onCreate() {
        super.onCreate()
        prefs = Prefs(this)
        tokens = TokenStore(this)
        secrets = SecretStore(this)
        http = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(2, TimeUnit.MINUTES)
            .writeTimeout(2, TimeUnit.MINUTES)
            .addInterceptor { chain ->
                val req = chain.request()
                val origin = prefs.serverUrl
                val tok = _token.value
                // The token only ever goes to the configured server.
                val sameServer = origin != null && req.url.toString().startsWith("$origin/")
                val out = if (sameServer && tok != null) req.newBuilder().header("Authorization", "Bearer $tok").build() else req
                val res = chain.proceed(out)
                if (sameServer && tok != null && res.code == 401 && _token.value == tok) loggedOut()
                res
            }
            .build()
        prefs.serverUrl?.let { _token.value = tokens.read(it) }
        live = Live(this)

        // The app lock: locked at start and after a pause longer than the timeout.
        _locked.value = prefs.appLock
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStop(owner: LifecycleOwner) {
                backgroundAt = System.currentTimeMillis()
                live.stop()
            }

            override fun onStart(owner: LifecycleOwner) {
                val now = System.currentTimeMillis()
                val away = now - backgroundAt
                if (backgroundAt != 0L && prefs.appLock && now > graceUntil && away >= prefs.lockTimeoutMillis) _locked.value = true
                graceUntil = 0
                live.start()
            }
        })

        if (Build.VERSION.SDK_INT >= 26) {
            val nm = getSystemService(NotificationManager::class.java)
            nm.createNotificationChannel(NotificationChannel(CHANNEL_UPLOADS, getString(R.string.channel_uploads), NotificationManager.IMPORTANCE_LOW))
        }
    }

    fun loggedIn(token: String) {
        val origin = prefs.serverUrl ?: return
        tokens.write(origin, token)
        _token.value = token
        live.restart()
    }

    fun loggedOut() {
        tokens.clear()
        _token.value = null
        live.stop()
    }

    /** Called when the server changes: the old token belongs to the old server. */
    fun serverChanged() {
        loggedOut()
        cachedApi = null
    }

    fun lock() {
        if (prefs.appLock) _locked.value = true
    }

    fun unlockApp() {
        _locked.value = false
    }

    /**
     * Our own trip to another app (file picker, camera, viewer) must not lock
     * the app on the way back.
     */
    fun expectExternal() {
        graceUntil = System.currentTimeMillis() + 5 * 60_000
    }

    override fun newImageLoader(context: PlatformContext): ImageLoader =
        ImageLoader.Builder(context)
            .components { add(OkHttpNetworkFetcherFactory(callFactory = { http })) }
            .memoryCache { MemoryCache.Builder().maxSizePercent(context, 0.25).build() }
            .diskCache { DiskCache.Builder().directory(cacheDir.resolve("images")).maxSizeBytes(256L shl 20).build() }
            .build()

    companion object {
        const val CHANNEL_UPLOADS = "uploads"
    }
}
