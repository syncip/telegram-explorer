package com.wagnermartin.telex.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.CloudOff
import androidx.compose.material.icons.filled.Fingerprint
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Visibility
import androidx.compose.material.icons.filled.VisibilityOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.fragment.app.FragmentActivity
import com.wagnermartin.telex.Biometrics
import com.wagnermartin.telex.R
import com.wagnermartin.telex.Secrets
import com.wagnermartin.telex.data.Api
import com.wagnermartin.telex.data.ApiException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.Request

/** A centered column for the screens before the files. */
@Composable
fun GatePage(icon: ImageVector, title: String, text: String? = null, content: @Composable () -> Unit = {}) {
    Box(Modifier.fillMaxSize().safeDrawingPadding().imePadding(), contentAlignment = Alignment.Center) {
        Column(
            Modifier.widthIn(max = 480.dp).fillMaxWidth().verticalScroll(rememberScrollState()).padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            Icon(icon, null, Modifier.size(56.dp), tint = MaterialTheme.colorScheme.primary)
            Text(title, style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
            if (text != null) Text(text, style = MaterialTheme.typography.bodyLarge, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
            content()
        }
    }
}

@Composable
fun SecretField(value: String, onChange: (String) -> Unit, label: String, imeAction: ImeAction = ImeAction.Done, onDone: () -> Unit = {}) {
    var visible by remember { mutableStateOf(false) }
    OutlinedTextField(
        value, onChange, Modifier.fillMaxWidth(),
        label = { Text(label) },
        singleLine = true,
        visualTransformation = if (visible) VisualTransformation.None else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = imeAction),
        keyboardActions = KeyboardActions(onDone = { onDone() }, onGo = { onDone() }),
        trailingIcon = {
            IconButton({ visible = !visible }) {
                Icon(if (visible) Icons.Default.VisibilityOff else Icons.Default.Visibility, if (visible) "Verbergen" else "Anzeigen")
            }
        },
    )
}

@Composable
fun BusyButton(text: String, busy: Boolean, enabled: Boolean = true, onClick: () -> Unit) {
    Button(onClick, Modifier.fillMaxWidth().height(56.dp), enabled = enabled && !busy) {
        if (busy) CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp) else Text(text, style = MaterialTheme.typography.titleMedium)
    }
}

@Composable
fun ServerScreen(vm: AppViewModel, onConnected: () -> Unit = {}) {
    val scope = rememberCoroutineScope()
    var input by rememberSaveable { mutableStateOf(vm.app.prefs.serverUrl ?: "") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var askHttp by remember { mutableStateOf<String?>(null) }

    fun probe(origin: String) {
        busy = true
        error = null
        scope.launch {
            val result = withContext(Dispatchers.IO) {
                runCatching {
                    vm.app.http.newCall(Request.Builder().url("$origin/api/state").build()).execute().use { res ->
                        val body = res.body.string()
                        if (!res.isSuccessful || !body.contains("needsSetup")) error("HTTP ${res.code}")
                    }
                }
            }
            busy = false
            result.onSuccess { vm.setServer(origin); onConnected() }
                .onFailure { error = if (it.message?.startsWith("HTTP") == true) "Unter dieser Adresse antwortet kein Telegram Explorer (${it.message})." else "Server nicht erreichbar: ${it.message}" }
        }
    }

    fun connect() {
        val origin = Api.origin(input)
        if (origin == null) {
            error = "Bitte eine Adresse wie https://explorer.example.com eingeben."
            return
        }
        input = origin
        if (origin.startsWith("http://")) askHttp = origin else probe(origin)
    }

    GatePage(Icons.Default.CloudOff, "Server verbinden", "Adresse deines Telegram Explorer, so wie du sie im Browser öffnest.") {
        OutlinedTextField(
            input, { input = it }, Modifier.fillMaxWidth(),
            label = { Text("Serveradresse") },
            placeholder = { Text("https://explorer.example.com") },
            singleLine = true,
            isError = error != null,
            supportingText = error?.let { { Text(it) } },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Go),
            keyboardActions = KeyboardActions(onGo = { connect() }),
        )
        BusyButton("Verbinden", busy, input.isNotBlank()) { connect() }
    }

    askHttp?.let { origin ->
        AlertDialog(
            onDismissRequest = { askHttp = null },
            title = { Text("Unverschlüsselte Verbindung") },
            text = { Text("Über http:// gehen Kennwort, Passphrase und Dateien unverschlüsselt durchs Netz. Das ist nur im eigenen Heimnetz oder über ein VPN vertretbar.") },
            confirmButton = { TextButton({ askHttp = null; probe(origin) }) { Text("Trotzdem verbinden") } },
            dismissButton = { TextButton({ askHttp = null }) { Text("Abbrechen") } },
        )
    }
}

@Composable
fun LoginScreen(vm: AppViewModel, onChangeServer: () -> Unit) {
    val scope = rememberCoroutineScope()
    val activity = LocalContext.current as FragmentActivity
    val secrets = vm.app.secrets
    var user by rememberSaveable { mutableStateOf(secrets.username ?: "") }
    var password by rememberSaveable { mutableStateOf("") }
    var code by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }

    fun submit(u: String = user, p: String = password) {
        busy = true
        error = null
        scope.launch {
            try {
                vm.login(u, p, code)
                rememberLogin(vm, u, p)
            } catch (e: ApiException) {
                error = if (e.unauthorized && secrets.totp && code.isBlank()) "Bitte noch den 2FA-Code eingeben." else e.message
            } catch (e: Exception) {
                error = e.message
            }
            busy = false
        }
    }

    fun withFingerprint() {
        Biometrics.withSecrets(activity, activity.getString(R.string.bio_login_title), { secrets.read() }, onDone = { s ->
            if (s.hasLogin) {
                user = s.username!!
                password = s.password!!
                if (secrets.totp && code.isBlank()) error = "Bitte noch den 2FA-Code eingeben." else submit(s.username, s.password)
            }
        })
    }

    LaunchedEffect(Unit) {
        if (secrets.hasLogin && vm.app.prefs.bioAuto && !secrets.totp && Biometrics.fingerprintReady(activity)) withFingerprint()
    }

    GatePage(Icons.Default.Lock, "Anmelden", vm.app.prefs.serverUrl) {
        OutlinedTextField(user, { user = it }, Modifier.fillMaxWidth(), label = { Text("Benutzername") }, singleLine = true,
            keyboardOptions = KeyboardOptions(imeAction = ImeAction.Next))
        SecretField(password, { password = it }, "Kennwort", ImeAction.Next)
        OutlinedTextField(code, { code = it.filter(Char::isDigit).take(6) }, Modifier.fillMaxWidth(), label = { Text("2FA-Code (falls aktiviert)") },
            singleLine = true, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword, imeAction = ImeAction.Done),
            keyboardActions = KeyboardActions(onDone = { submit() }))
        error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        BusyButton("Anmelden", busy, user.isNotBlank() && password.isNotEmpty()) { submit() }
        if (secrets.hasLogin) {
            OutlinedButton({ withFingerprint() }, Modifier.fillMaxWidth().height(56.dp)) {
                Icon(Icons.Default.Fingerprint, null)
                Spacer(Modifier.size(8.dp))
                Text("Mit Fingerabdruck")
            }
        }
        TextButton(onChangeServer) { Text("Server ändern") }
    }
}

/** Offers to keep the login for the fingerprint, once per new password. */
var pendingLogin: Secrets? = null

private fun rememberLogin(vm: AppViewModel, user: String, password: String) {
    val s = Secrets(user, password)
    if (!vm.app.secrets.knows(s)) pendingLogin = s
}

@Composable
fun UnlockScreen(vm: AppViewModel) {
    val scope = rememberCoroutineScope()
    val activity = LocalContext.current as FragmentActivity
    val app = vm.app
    var passphrase by rememberSaveable { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var offer by remember { mutableStateOf<Secrets?>(null) }

    fun submit(p: String, typed: Boolean) {
        busy = true
        error = null
        scope.launch {
            try {
                vm.unlock(p)
                val wanted = Secrets(pendingLogin?.username, pendingLogin?.password, if (typed) p else null)
                pendingLogin = null
                if (!wanted.isEmpty && app.prefs.bioOffer && Biometrics.fingerprintReady(activity) && !app.secrets.knows(wanted)) offer = wanted
            } catch (e: ApiException) {
                error = if (typed) e.message else activity.getString(R.string.bio_unlock_failed)
            } catch (e: Exception) {
                error = e.message
            }
            busy = false
        }
    }

    fun withFingerprint() {
        Biometrics.withSecrets(activity, activity.getString(R.string.bio_unlock_title), { app.secrets.read() }, onDone = { s ->
            s.passphrase?.let { submit(it, false) }
        })
    }

    LaunchedEffect(Unit) {
        if (app.secrets.hasPassphrase && app.prefs.bioAuto && Biometrics.fingerprintReady(activity)) withFingerprint()
    }

    GatePage(Icons.Default.Lock, "Tresor entsperren", "Der Server wurde neu gestartet. Die Passphrase entschlüsselt deine Dateien wieder.") {
        SecretField(passphrase, { passphrase = it }, "Passphrase") { if (passphrase.isNotEmpty()) submit(passphrase, true) }
        error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
        BusyButton("Entsperren", busy, passphrase.isNotEmpty()) { submit(passphrase, true) }
        if (app.secrets.hasPassphrase) {
            OutlinedButton({ withFingerprint() }, Modifier.fillMaxWidth().height(56.dp)) {
                Icon(Icons.Default.Fingerprint, null)
                Spacer(Modifier.size(8.dp))
                Text("Mit Fingerabdruck")
            }
        }
        TextButton({ vm.logout() }) { Text("Abmelden") }
    }

    offer?.let { SaveSecretsDialog(vm, it) { offer = null } }
}

/** Asks whether to keep login and passphrase behind the fingerprint. */
@Composable
fun SaveSecretsDialog(vm: AppViewModel, wanted: Secrets, onDone: () -> Unit) {
    val activity = LocalContext.current as FragmentActivity
    val app = vm.app
    val text = when {
        wanted.hasLogin && wanted.hasPassphrase -> R.string.bio_save_both
        wanted.hasPassphrase -> R.string.bio_save_passphrase
        else -> R.string.bio_save_login
    }
    AlertDialog(
        onDismissRequest = onDone,
        icon = { Icon(Icons.Default.Fingerprint, null) },
        title = { Text(activity.getString(R.string.bio_save_title)) },
        text = {
            androidx.compose.foundation.layout.Column(verticalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(8.dp)) {
                Text(activity.getString(text))
                androidx.compose.foundation.layout.Spacer(Modifier.height(8.dp))
                androidx.compose.material3.Button({
                    onDone()
                    Biometrics.withSecrets(activity, activity.getString(R.string.bio_confirm_save), {
                        val old = runCatching { app.secrets.read() }.getOrDefault(Secrets())
                        app.secrets.write(
                            Secrets(
                                wanted.username ?: old.username, wanted.password ?: old.password,
                                wanted.passphrase ?: old.passphrase,
                            ),
                        )
                    }, onDone = { android.widget.Toast.makeText(activity, R.string.bio_saved, android.widget.Toast.LENGTH_SHORT).show() })
                }, Modifier.fillMaxWidth().heightIn(min = 52.dp)) { Text(activity.getString(R.string.bio_save_yes)) }
                androidx.compose.material3.OutlinedButton(onDone, Modifier.fillMaxWidth().heightIn(min = 52.dp)) {
                    Text(activity.getString(R.string.bio_save_no))
                }
                TextButton({ app.prefs.bioOffer = false; onDone() }, Modifier.fillMaxWidth()) {
                    Text(activity.getString(R.string.bio_save_never))
                }
            }
        },
        confirmButton = {},
    )
}

@Composable
fun InfoScreen(icon: ImageVector, title: String, text: String, actions: @Composable () -> Unit) {
    GatePage(icon, title, text) { actions() }
}

@Composable
fun LoadingScreen() {
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
}
