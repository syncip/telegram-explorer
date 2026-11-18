package com.wagnermartin.telex.ui

import android.app.Application
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.wagnermartin.telex.TelexApp
import com.wagnermartin.telex.data.ApiException
import com.wagnermartin.telex.data.AppState
import com.wagnermartin.telex.data.TelegramStatus
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Which screen the app shows before the files. */
sealed interface Gate {
    data object Loading : Gate
    data object NoServer : Gate
    data object Login : Gate
    data object Unlock : Gate
    data object NeedsSetup : Gate
    data class Telegram(val status: TelegramStatus?) : Gate
    data class Offline(val message: String) : Gate
    data object Ready : Gate
}

class AppViewModel(application: Application) : AndroidViewModel(application) {
    val app = application as TelexApp

    private val _gate = MutableStateFlow<Gate>(Gate.Loading)
    val gate: StateFlow<Gate> = _gate.asStateFlow()

    var username: String? = null
        private set

    /** Files shared from another app, waiting for a target folder. */
    private val _shared = MutableStateFlow<List<Uri>>(emptyList())
    val shared: StateFlow<List<Uri>> = _shared.asStateFlow()

    private var seenReady = false
    private var polling: Job? = null

    init {
        viewModelScope.launch {
            app.token.collect { refresh() }
        }
        viewModelScope.launch {
            app.live.state.collect { s ->
                if (s == null || app.token.value == null) return@collect
                apply(AppState(loggedIn = true, locked = s.locked, telegram = s.telegram, username = username))
            }
        }
    }

    fun refresh() {
        viewModelScope.launch { load() }
    }

    private suspend fun load() {
        val api = app.api
        if (api == null) {
            _gate.value = Gate.NoServer
            return
        }
        if (app.token.value == null) {
            _gate.value = Gate.Login
            return
        }
        if (_gate.value !is Gate.Ready) _gate.value = Gate.Loading
        try {
            apply(api.state())
        } catch (e: ApiException) {
            _gate.value = Gate.Offline(e.message ?: "Fehler ${e.status}")
        } catch (e: Exception) {
            if (_gate.value !is Gate.Ready) _gate.value = Gate.Offline(e.message ?: e.javaClass.simpleName)
            else pollUntilBack()
        }
    }

    private fun apply(s: AppState) {
        if (s.username != null) username = s.username
        val step = s.telegram?.step
        if (s.locked || !s.loggedIn) seenReady = false
        if (step == "ready") seenReady = true
        _gate.value = when {
            s.needsSetup -> Gate.NeedsSetup
            !s.loggedIn -> {
                app.loggedOut()
                Gate.Login
            }
            s.locked -> Gate.Unlock
            step == "ready" || (seenReady && step == "connecting") -> Gate.Ready
            s.telegram == null -> _gate.value.takeIf { it is Gate.Ready } ?: Gate.Telegram(null)
            else -> Gate.Telegram(s.telegram)
        }
        if (_gate.value is Gate.Telegram) pollUntilBack()
    }

    /** While Telegram connects or the server is away, the state is fetched again. */
    private fun pollUntilBack() {
        if (polling?.isActive == true) return
        polling = viewModelScope.launch {
            while (true) {
                delay(3000)
                val api = app.api ?: return@launch
                try {
                    apply(api.state())
                    if (_gate.value is Gate.Ready || _gate.value is Gate.Login || _gate.value is Gate.Unlock) return@launch
                } catch (_: Exception) {
                }
            }
        }
    }

    suspend fun login(username: String, password: String, code: String) {
        val api = app.api ?: return
        val token = api.login(username.trim(), password, code.trim(), deviceName())
        this.username = username.trim()
        app.loggedIn(token)
    }

    suspend fun unlock(passphrase: String) {
        app.api?.unlock(passphrase)
        load()
    }

    fun logout() {
        viewModelScope.launch {
            runCatching { app.api?.logout() }
            app.loggedOut()
        }
    }

    fun setServer(origin: String) {
        if (origin != app.prefs.serverUrl) {
            app.prefs.serverUrl = origin
            app.serverChanged()
            app.secrets.clear()
        }
        refresh()
    }

    fun share(uris: List<Uri>) {
        _shared.value = uris
    }

    fun shareDone() {
        _shared.value = emptyList()
    }

    private fun deviceName(): String {
        val ctx = getApplication<Application>()
        val name = runCatching { Settings.Global.getString(ctx.contentResolver, Settings.Global.DEVICE_NAME) }.getOrNull()
        return name?.takeIf { it.isNotBlank() } ?: "${Build.MANUFACTURER.replaceFirstChar { it.uppercase() }} ${Build.MODEL}"
    }
}
