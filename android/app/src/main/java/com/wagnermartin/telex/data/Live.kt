package com.wagnermartin.telex.data

import android.os.Handler
import android.os.Looper
import com.wagnermartin.telex.TelexApp
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import okhttp3.Request
import okhttp3.Response
import okhttp3.sse.EventSource
import okhttp3.sse.EventSourceListener
import okhttp3.sse.EventSources
import java.util.concurrent.TimeUnit

/**
 * The server's event stream (/api/events) while the app is in the foreground.
 * Screens listen to [files] to reload, the rest is kept as state.
 */
class Live(private val app: TelexApp) {
    private val main = Handler(Looper.getMainLooper())
    private var source: EventSource? = null
    private var wanted = false
    private var delay = 1000L

    private val client by lazy { app.http.newBuilder().readTimeout(60, TimeUnit.SECONDS).build() }

    private val _files = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

    /** Something changed in files, folders or shares. */
    val files: SharedFlow<Unit> = _files.asSharedFlow()

    private val _state = MutableStateFlow<LiveState?>(null)
    val state: StateFlow<LiveState?> = _state.asStateFlow()

    private val _jobs = MutableStateFlow<List<Job>>(emptyList())

    /** Files still on their way from the server to Telegram. */
    val jobs: StateFlow<List<Job>> = _jobs.asStateFlow()

    private val _stats = MutableStateFlow<Stats?>(null)
    val stats: StateFlow<Stats?> = _stats.asStateFlow()

    private val _connected = MutableStateFlow(false)
    val connected: StateFlow<Boolean> = _connected.asStateFlow()

    fun start() {
        wanted = true
        connect()
    }

    fun stop() {
        wanted = false
        main.removeCallbacksAndMessages(null)
        source?.cancel()
        source = null
        _connected.value = false
    }

    fun restart() {
        stop()
        start()
    }

    /** A local change, so screens reload at once without waiting for the stream. */
    fun poke() {
        _files.tryEmit(Unit)
    }

    private fun connect() {
        if (!wanted || source != null) return
        val api = app.api ?: return
        if (app.token.value == null) return
        val req = Request.Builder().url(api.url("/events")).header("Accept", "text/event-stream").build()
        source = EventSources.createFactory(client).newEventSource(req, object : EventSourceListener() {
            override fun onOpen(eventSource: EventSource, response: Response) {
                delay = 1000L
                _connected.value = true
                _files.tryEmit(Unit)
            }

            override fun onEvent(eventSource: EventSource, id: String?, type: String?, data: String) {
                runCatching {
                    when (type) {
                        "files" -> _files.tryEmit(Unit)
                        "state" -> _state.value = json.decodeFromString(LiveState.serializer(), data)
                        "jobs" -> _jobs.value = json.decodeFromString(kotlinx.serialization.builtins.ListSerializer(Job.serializer()), data)
                        "stats" -> _stats.value = json.decodeFromString(Stats.serializer(), data)
                        "logout" -> main.post { app.loggedOut() }
                    }
                }
            }

            override fun onClosed(eventSource: EventSource) = retry(eventSource)

            override fun onFailure(eventSource: EventSource, t: Throwable?, response: Response?) {
                if (response?.code == 401) main.post { app.loggedOut() }
                retry(eventSource)
            }
        })
    }

    private fun retry(es: EventSource) {
        main.post {
            if (source !== es) return@post
            source = null
            _connected.value = false
            if (!wanted) return@post
            main.postDelayed({ connect() }, delay)
            delay = (delay * 2).coerceAtMost(30_000L)
        }
    }
}
