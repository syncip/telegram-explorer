package com.wagnermartin.telex

import android.annotation.SuppressLint
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.graphics.Bitmap
import android.net.Uri
import android.net.http.SslError
import android.os.Build
import android.os.Bundle
import android.os.Message
import android.os.SystemClock
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.view.inputmethod.InputMethodManager
import android.webkit.CookieManager
import android.webkit.RenderProcessGoneDetail
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.OnBackPressedCallback
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.core.view.isVisible
import androidx.core.view.updatePadding
import androidx.lifecycle.lifecycleScope
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebMessageCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import com.google.android.material.button.MaterialButton
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.progressindicator.LinearProgressIndicator
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

/**
 * Shows the web interface in a WebView and adds what a browser tab cannot do: the app
 * lock, logging in and unlocking the vault with the fingerprint, downloads through the
 * download manager, folder uploads and opening PDFs in another app.
 */
class MainActivity : AppCompatActivity() {
    private lateinit var prefs: Prefs
    private lateinit var store: SecretStore
    private lateinit var origin: String
    private lateinit var root: FrameLayout
    private lateinit var web: WebView
    private lateinit var progress: LinearProgressIndicator
    private lateinit var errorView: View
    private lateinit var lockView: View

    // App lock
    private var appLocked = false
    private var lockPromptShown = false
    private var stoppedAt = 0L
    private var ownUiActive = false
    private var stoppedForOwnUi = false
    private var settingsAfterUnlock = false

    // Fingerprint login and unlock
    private data class WebState(val needsSetup: Boolean, val loggedIn: Boolean, val locked: Boolean)
    private var webState: WebState? = null
    private var declined: String? = null
    private var bioBusy = false
    private var pendingLogin: Secrets? = null
    private var dialog: AlertDialog? = null
    private var replyProxy: JavaScriptReplyProxy? = null

    // WebView extras
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    // Files shared from other apps, waiting for the web interface to upload them.
    private var shared: List<Uri> = emptyList()
    private var customView: View? = null
    private var customCallback: WebChromeClient.CustomViewCallback? = null
    private var insets = WindowInsetsCompat.CONSUMED

    private val pickFiles = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
        ownUiActive = false
        val data = result.data
        val uris = if (result.resultCode != Activity.RESULT_OK || data == null) null else {
            val clip = data.clipData
            if (clip != null) Array(clip.itemCount) { clip.getItemAt(it).uri } else data.data?.let { arrayOf(it) }
        }
        fileCallback?.onReceiveValue(uris)
        fileCallback = null
    }

    private val pickFolder = registerForActivityResult(ActivityResultContracts.OpenDocumentTree()) { tree ->
        ownUiActive = false
        val callback = fileCallback ?: return@registerForActivityResult
        fileCallback = null
        if (tree == null) {
            callback.onReceiveValue(null)
            return@registerForActivityResult
        }
        progress.isVisible = true
        lifecycleScope.launch {
            val files = runCatching { withContext(Dispatchers.IO) { Files.listTree(this@MainActivity, tree) } }.getOrElse { emptyList() }
            progress.isVisible = false
            if (files.isEmpty()) {
                callback.onReceiveValue(null)
                return@launch
            }
            // The web interface rebuilds the folders from these paths, the WebView alone
            // would hand over the files without them.
            val paths = JSONArray(files.map { it.second })
            web.evaluateJavascript("window.__telex && window.__telex.setFolderPaths($paths)") {
                callback.onReceiveValue(files.map { it.first }.toTypedArray())
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        store = SecretStore(this)
        val server = prefs.serverUrl
        val serverOrigin = prefs.serverOrigin
        if (server == null || serverOrigin == null) {
            startActivity(Intent(this, ServerActivity::class.java))
            finish()
            return
        }
        origin = serverOrigin
        setContentView(R.layout.activity_main)
        root = findViewById(R.id.root)
        web = findViewById(R.id.web)
        progress = findViewById(R.id.progress)
        errorView = findViewById(R.id.error)
        lockView = findViewById(R.id.lock)

        ViewCompat.setOnApplyWindowInsetsListener(root) { _, i ->
            insets = i
            applyInsets()
            WindowInsetsCompat.CONSUMED
        }
        findViewById<MaterialButton>(R.id.lock_unlock).setOnClickListener { promptAppUnlock() }
        findViewById<MaterialButton>(R.id.error_retry).setOnClickListener {
            errorView.isVisible = false
            if (web.url.isNullOrEmpty() || web.url == "about:blank") web.loadUrl(server) else web.reload()
        }
        findViewById<MaterialButton>(R.id.error_server).setOnClickListener {
            startOwn(Intent(this, ServerActivity::class.java))
        }
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() = onBack()
        })

        applyWindowFlags()
        setupWebView()

        if (prefs.appLock && Biometrics.appLockAuthenticators(this, prefs) != null) lock()
        handleIntent(intent)
        if (savedInstanceState != null) web.restoreState(savedInstanceState)
        if (web.url == null) web.loadUrl(server)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handleIntent(intent)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::web.isInitialized) web.saveState(outState)
    }

    override fun onStart() {
        super.onStart()
        if (!::web.isInitialized) return
        // The server address may have changed in the settings.
        if (prefs.serverOrigin != origin) {
            recreate()
            return
        }
        val away = SystemClock.elapsedRealtime() - stoppedAt
        val ownUi = stoppedForOwnUi && away < OWN_UI_GRACE_MILLIS
        stoppedForOwnUi = false
        if (stoppedAt > 0 && !appLocked && !ownUi && prefs.appLock && away >= prefs.lockTimeoutMillis &&
            Biometrics.appLockAuthenticators(this, prefs) != null
        ) {
            lock()
        }
        stoppedAt = 0
    }

    override fun onResume() {
        super.onResume()
        if (!::web.isInitialized) return
        applyWindowFlags()
        web.settings.textZoom = prefs.textZoom
        web.onResume()
        if (appLocked && !lockPromptShown) promptAppUnlock()
    }

    override fun onPause() {
        if (::web.isInitialized) web.onPause()
        super.onPause()
    }

    override fun onStop() {
        super.onStop()
        if (!::web.isInitialized) return
        CookieManager.getInstance().flush()
        if (!isChangingConfigurations) {
            stoppedAt = SystemClock.elapsedRealtime()
            stoppedForOwnUi = ownUiActive
            lockPromptShown = false
        }
    }

    override fun onDestroy() {
        dialog?.dismiss()
        if (::web.isInitialized) {
            fileCallback?.onReceiveValue(null)
            (web.parent as? ViewGroup)?.removeView(web)
            web.destroy()
        }
        super.onDestroy()
    }

    private fun handleIntent(intent: Intent?) {
        when (intent?.action) {
            ACTION_LOCK -> {
                if (Biometrics.appLockAuthenticators(this, prefs) != null) lock()
            }
            ACTION_SETTINGS -> if (appLocked) settingsAfterUnlock = true else openSettings()
            Intent.ACTION_SEND, Intent.ACTION_SEND_MULTIPLE -> {
                val uris = sharedUris(intent)
                if (uris.isEmpty()) return
                // Copied right away: the read grant ends with the sharing app's intent,
                // and the WebView needs a real file name and size.
                progress.isVisible = true
                lifecycleScope.launch {
                    val copies = withContext(Dispatchers.IO) { Files.copyShared(this@MainActivity, uris) }
                    progress.isVisible = false
                    if (copies.size < uris.size) {
                        Toast.makeText(this@MainActivity, R.string.share_read_failed, Toast.LENGTH_LONG).show()
                    }
                    if (copies.isNotEmpty()) {
                        shared = copies
                        sendShared()
                    }
                }
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun sharedUris(intent: Intent): List<Uri> {
        val out = mutableListOf<Uri>()
        // Some apps send a single Uri with SEND_MULTIPLE or a list with SEND.
        runCatching { intent.getParcelableArrayListExtra<Uri>(Intent.EXTRA_STREAM) }.getOrNull()?.let { out += it }
        if (out.isEmpty()) runCatching { intent.getParcelableExtra<Uri>(Intent.EXTRA_STREAM) }.getOrNull()?.let { out += it }
        intent.clipData?.let { clip -> for (i in 0 until clip.itemCount) clip.getItemAt(i).uri?.let { if (it !in out) out += it } }
        return out
    }

    /** Tells the explorer how many shared files wait, so it can offer to upload them. */
    private fun sendShared() {
        val proxy = replyProxy ?: return
        runCatching { proxy.postMessage(JSONObject().put("type", "shared").put("count", shared.size).toString()) }
    }

    // ---- Window --------------------------------------------------------------------

    private fun applyWindowFlags() {
        if (prefs.secureScreen) {
            window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        } else {
            window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
        }
        if (Build.VERSION.SDK_INT >= 33) setRecentsScreenshotEnabled(!prefs.secureScreen && !prefs.appLock)
    }

    private fun applyInsets() {
        if (customView != null) {
            root.updatePadding(0, 0, 0, 0)
            return
        }
        val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
        val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
        root.updatePadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
    }

    /** Starts one of the app's own screens, which must not lock the app on return. */
    private fun startOwn(intent: Intent) {
        ownUiActive = true
        try {
            startActivity(intent)
        } catch (e: ActivityNotFoundException) {
            ownUiActive = false
        }
    }

    private fun openSettings() = startOwn(Intent(this, SettingsActivity::class.java))

    // ---- App lock ------------------------------------------------------------------

    private fun lock() {
        appLocked = true
        dialog?.dismiss()
        currentFocus?.let { getSystemService(InputMethodManager::class.java).hideSoftInputFromWindow(it.windowToken, 0) }
        lockView.isVisible = true
        lockView.bringToFront()
        if (lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED)) promptAppUnlock()
    }

    private fun promptAppUnlock() {
        val auth = Biometrics.appLockAuthenticators(this, prefs)
        if (auth == null) {
            unlockApp()
            return
        }
        lockPromptShown = true
        ownUiActive = true
        Biometrics.prompt(
            this, getString(R.string.lock_prompt_title), getString(R.string.lock_prompt_subtitle), auth,
            onSuccess = {
                ownUiActive = false
                unlockApp()
            },
            onCancel = { ownUiActive = false },
        )
    }

    private fun unlockApp() {
        appLocked = false
        lockView.isVisible = false
        if (settingsAfterUnlock) {
            settingsAfterUnlock = false
            openSettings()
            return
        }
        webState?.let { onWebState(it) }
    }

    // ---- WebView -------------------------------------------------------------------

    @SuppressLint("SetJavaScriptEnabled")
    private fun setupWebView() {
        if (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0) WebView.setWebContentsDebuggingEnabled(true)
        CookieManager.getInstance().setAcceptCookie(true)
        val version = packageManager.getPackageInfo(packageName, 0).versionName
        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            mediaPlaybackRequiresUserGesture = false
            allowFileAccess = false
            allowContentAccess = false
            setSupportMultipleWindows(true)
            javaScriptCanOpenWindowsAutomatically = true
            builtInZoomControls = false
            userAgentString = "$userAgentString TelexAndroid/$version"
        }

        val origins = setOf(origin)
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(web, "TelexBridge", origins) { _, message, sourceOrigin, isMainFrame, reply ->
                if (isMainFrame && Prefs.origin(sourceOrigin) == origin) {
                    replyProxy = reply
                    onBridgeMessage(message)
                }
            }
        }
        if (WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
            val script = assets.open("bridge.js").bufferedReader().use { it.readText() }
            WebViewCompat.addDocumentStartJavaScript(web, script, origins)
        }

        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                if (isOwn(request.url)) return false
                openExternal(request.url)
                return true
            }

            override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
                progress.isVisible = true
                bioBusy = false
            }

            override fun onPageFinished(view: WebView, url: String?) {
                progress.isVisible = false
            }

            override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
                if (request.isForMainFrame) showError(error.description.toString())
            }

            @SuppressLint("WebViewClientOnReceivedSslError")
            override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
                handler.cancel()
                showError(getString(R.string.ssl_error, sslReason(error)))
            }

            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                recreate()
                return true
            }
        }

        web.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
                fileCallback?.onReceiveValue(null)
                fileCallback = null
                // The explorer asked for the shared files: hand them over without a picker.
                val accept = params.acceptTypes.orEmpty().toSet()
                if (ACCEPT_SHARED in accept) {
                    val uris = shared.toTypedArray()
                    shared = emptyList()
                    callback.onReceiveValue(uris)
                    sendShared()
                    return true
                }
                fileCallback = callback
                ownUiActive = true
                try {
                    // webkitdirectory: some WebView versions report mode 2 (hidden in the SDK),
                    // the web interface also marks its folder input.
                    if (params.mode == MODE_OPEN_FOLDER || ACCEPT_FOLDER in accept) {
                        pickFolder.launch(null)
                    } else {
                        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT)
                            .addCategory(Intent.CATEGORY_OPENABLE)
                            .setType("*/*")
                            .putExtra(Intent.EXTRA_ALLOW_MULTIPLE, params.mode == FileChooserParams.MODE_OPEN_MULTIPLE)
                        pickFiles.launch(intent)
                    }
                } catch (e: ActivityNotFoundException) {
                    ownUiActive = false
                    fileCallback = null
                    Toast.makeText(this@MainActivity, R.string.file_chooser_failed, Toast.LENGTH_LONG).show()
                    return false
                }
                return true
            }

            // window.open() for PDFs: catch the address and decide where it goes.
            override fun onCreateWindow(view: WebView, isDialog: Boolean, isUserGesture: Boolean, resultMsg: Message): Boolean {
                val popup = WebView(this@MainActivity)
                var handled = false
                fun take(uri: Uri?) {
                    if (handled || uri == null || uri.toString() == "about:blank") return
                    handled = true
                    openFromPopup(uri)
                    popup.post { popup.destroy() }
                }
                popup.webViewClient = object : WebViewClient() {
                    override fun shouldOverrideUrlLoading(v: WebView, request: WebResourceRequest): Boolean {
                        take(request.url)
                        return true
                    }

                    override fun onPageStarted(v: WebView, url: String?, favicon: Bitmap?) {
                        take(url?.let(Uri::parse))
                        v.stopLoading()
                    }
                }
                (resultMsg.obj as WebView.WebViewTransport).webView = popup
                resultMsg.sendToTarget()
                return true
            }

            override fun onShowCustomView(view: View, callback: CustomViewCallback) {
                customView?.let { onHideCustomView() }
                customView = view
                customCallback = callback
                root.addView(view, FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
                if (appLocked) lockView.bringToFront()
                WindowCompat.getInsetsController(window, root).apply {
                    systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                    hide(WindowInsetsCompat.Type.systemBars())
                }
                applyInsets()
            }

            override fun onHideCustomView() {
                val view = customView ?: return
                root.removeView(view)
                customView = null
                customCallback?.onCustomViewHidden()
                customCallback = null
                WindowCompat.getInsetsController(window, root).show(WindowInsetsCompat.Type.systemBars())
                applyInsets()
            }
        }

        web.setDownloadListener { url, userAgent, contentDisposition, mimetype, _ ->
            Files.download(this, url, userAgent, contentDisposition, mimetype)
        }
    }

    private fun isOwn(uri: Uri) = Prefs.origin(uri) == origin

    private fun openExternal(uri: Uri) {
        try {
            startActivity(Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE))
        } catch (e: ActivityNotFoundException) {
            Toast.makeText(this, R.string.no_app_for_file, Toast.LENGTH_LONG).show()
        }
    }

    /** The WebView cannot show PDFs, so they are fetched with the session and handed on. */
    private fun openFromPopup(uri: Uri) {
        if (!isOwn(uri)) {
            openExternal(uri)
            return
        }
        val url = uri.toString()
        progress.isVisible = true
        Toast.makeText(this, getString(R.string.opening, uri.lastPathSegment ?: ""), Toast.LENGTH_SHORT).show()
        val agent = web.settings.userAgentString
        lifecycleScope.launch {
            val result = runCatching { withContext(Dispatchers.IO) { Files.fetchToCache(this@MainActivity, url, agent) } }
            progress.isVisible = false
            result.onSuccess { (file, mime) ->
                ownUiActive = true
                Files.open(this@MainActivity, file, mime)
            }.onFailure {
                Toast.makeText(this@MainActivity, getString(R.string.download_failed, it.localizedMessage ?: ""), Toast.LENGTH_LONG).show()
            }
        }
    }

    private fun showError(message: String) {
        progress.isVisible = false
        findViewById<TextView>(R.id.error_message).text = message
        errorView.isVisible = true
        if (appLocked) lockView.bringToFront()
    }

    private fun sslReason(error: SslError) = when (error.primaryError) {
        SslError.SSL_UNTRUSTED -> "nicht vertrauenswürdig"
        SslError.SSL_EXPIRED -> "abgelaufen"
        SslError.SSL_IDMISMATCH -> "falscher Name"
        SslError.SSL_NOTYETVALID -> "noch nicht gültig"
        SslError.SSL_DATE_INVALID -> "ungültiges Datum"
        else -> "ungültig"
    }

    private fun onBack() {
        if (customView != null) {
            web.webChromeClient?.onHideCustomView()
            return
        }
        if (appLocked || errorView.isVisible) {
            moveTaskToBack(true)
            return
        }
        // The web interface closes dialogs and menus or goes back a folder first.
        web.evaluateJavascript("(function(){try{return !!(window.__telexBack && window.__telexBack())}catch(e){return false}})()") { handled ->
            if (handled == "true") return@evaluateJavascript
            if (web.canGoBack()) web.goBack() else moveTaskToBack(true)
        }
    }

    // ---- Bridge --------------------------------------------------------------------

    private fun onBridgeMessage(message: WebMessageCompat) {
        val m = runCatching { JSONObject(message.data ?: return) }.getOrNull() ?: return
        fun str(name: String) = m.optString(name).ifEmpty { null }
        when (m.optString("type")) {
            "hello" -> sendStored()
            "state" -> onWebState(WebState(m.optBoolean("needsSetup"), m.optBoolean("loggedIn"), m.optBoolean("locked")))
            "setup" -> offerSave(Secrets(str("username"), str("password"), str("passphrase")), R.string.bio_save_both)
            "login" -> {
                store.totp = m.optBoolean("totp")
                pendingLogin = Secrets(str("username"), str("password"))
                declined = null
            }
            "unlock" -> {
                val login = pendingLogin
                pendingLogin = null
                val s = Secrets(login?.username, login?.password, str("passphrase"))
                offerSave(s, if (login != null) R.string.bio_save_both else R.string.bio_save_passphrase)
            }
            "changed" -> offerUpdate(m.optString("field"), m.optString("value"))
            "totp" -> store.totp = m.optBoolean("on")
            "logout" -> declined = "login"
            "auto" -> onAutoResult(m.optString("step"), m.optInt("status"))
            "bio" -> runBio(m.optString("step"), manual = true)
            "lock" -> if (Biometrics.appLockAuthenticators(this, prefs) != null) lock()
            "appSettings" -> openSettings()
            "explorer" -> sendShared()
            "sharedDrop" -> {
                shared = emptyList()
                sendShared()
            }
        }
    }

    private fun sendStored() {
        val proxy = replyProxy ?: return
        val msg = JSONObject()
            .put("type", "stored")
            .put("login", store.hasLogin)
            .put("passphrase", store.hasPassphrase)
            .put("fingerprint", Biometrics.fingerprintReady(this))
        runCatching { proxy.postMessage(msg.toString()) }
    }

    private fun onWebState(s: WebState) {
        webState = s
        // A login with the vault already open is complete now; with a locked vault
        // the offer waits for the passphrase so both are saved in one go.
        pendingLogin?.let {
            if (s.loggedIn && !s.locked) {
                pendingLogin = null
                offerSave(it, R.string.bio_save_login)
            }
        }
        val need = when {
            s.needsSetup -> null
            !s.loggedIn && store.hasLogin -> STEP_LOGIN
            s.loggedIn && s.locked && store.hasPassphrase -> STEP_UNLOCK
            else -> null
        }
        if (need != declined) declined = null
        if (need == null || need == declined || !prefs.bioAuto) return
        runBio(need, manual = false)
    }

    /** Logs in or unlocks the vault with the stored values after a fingerprint check. */
    private fun runBio(step: String, manual: Boolean) {
        if (step != STEP_LOGIN && step != STEP_UNLOCK) return
        if (appLocked || bioBusy || dialog?.isShowing == true || !lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED)) return
        if (manual && !Biometrics.fingerprintReady(this)) {
            Toast.makeText(this, R.string.bio_not_available, Toast.LENGTH_LONG).show()
            return
        }
        val title = when {
            step == STEP_UNLOCK -> R.string.bio_unlock_title
            webState?.locked == true && store.hasPassphrase && !store.totp -> R.string.bio_login_unlock_title
            else -> R.string.bio_login_title
        }
        bioBusy = true
        Biometrics.withSecrets(
            this, getString(title),
            op = { store.read() },
            onDone = { s ->
                when {
                    step == STEP_UNLOCK && s.hasPassphrase ->
                        js("window.__telex.unlock(${q(s.passphrase)})")
                    step == STEP_LOGIN && s.hasLogin && store.totp -> {
                        js("window.__telex.fillLogin(${q(s.username)}, ${q(s.password)})")
                        Toast.makeText(this, R.string.bio_code_needed, Toast.LENGTH_LONG).show()
                        bioBusy = false
                        declined = STEP_LOGIN
                    }
                    step == STEP_LOGIN && s.hasLogin ->
                        js("window.__telex.login(${q(s.username)}, ${q(s.password)}, ${if (s.hasPassphrase) q(s.passphrase) else "null"})")
                    else -> {
                        bioBusy = false
                        sendStored()
                    }
                }
            },
            onCancel = {
                bioBusy = false
                declined = step
                sendStored()
            },
        )
    }

    private fun onAutoResult(step: String, status: Int) {
        bioBusy = false
        when {
            step == "done" -> Unit
            status == 429 -> Toast.makeText(this, "Zu viele Fehlversuche, bitte später erneut versuchen.", Toast.LENGTH_LONG).show()
            step == STEP_LOGIN -> {
                declined = STEP_LOGIN
                Toast.makeText(this, R.string.bio_login_failed, Toast.LENGTH_LONG).show()
            }
            step == STEP_UNLOCK -> {
                declined = STEP_UNLOCK
                Toast.makeText(this, R.string.bio_unlock_failed, Toast.LENGTH_LONG).show()
            }
        }
    }

    private fun offerSave(s: Secrets, message: Int) {
        if (s.isEmpty || !prefs.bioOffer || !Biometrics.fingerprintReady(this) || store.knows(s)) return
        dialog?.dismiss()
        dialog = MaterialAlertDialogBuilder(this)
            .setTitle(R.string.bio_save_title)
            .setIcon(R.drawable.ic_fingerprint)
            .setMessage(message)
            .setPositiveButton(R.string.bio_save_yes) { _, _ -> save { old -> Secrets(s.username ?: old.username, s.password ?: old.password, s.passphrase ?: old.passphrase) } }
            .setNegativeButton(R.string.bio_save_no, null)
            .setNeutralButton(R.string.bio_save_never) { _, _ -> prefs.bioOffer = false }
            .show()
    }

    private fun offerUpdate(field: String, value: String) {
        if (value.isEmpty()) return
        val stored = if (field == "password") store.hasLogin else store.hasPassphrase
        if (!stored) return
        val what = getString(if (field == "password") R.string.the_password else R.string.the_passphrase)
        dialog?.dismiss()
        dialog = MaterialAlertDialogBuilder(this)
            .setTitle(R.string.bio_update_title)
            .setIcon(R.drawable.ic_fingerprint)
            .setMessage(getString(R.string.bio_update_message, what))
            .setPositiveButton(R.string.bio_save_yes) { _, _ ->
                save { old -> if (field == "password") old.copy(password = value) else old.copy(passphrase = value) }
            }
            .setNegativeButton(R.string.bio_save_no, null)
            .show()
    }

    /** Merges new values into the stored ones; one fingerprint covers read and write. */
    private fun save(merge: (Secrets) -> Secrets) {
        Biometrics.withSecrets(
            this, getString(R.string.bio_confirm_save),
            op = {
                val old = if (store.isEmpty) Secrets() else try {
                    store.read()
                } catch (e: SecretsInvalidatedException) {
                    Secrets()
                }
                store.write(merge(old))
            },
            onDone = {
                Toast.makeText(this, R.string.bio_saved, Toast.LENGTH_SHORT).show()
                sendStored()
            },
        )
    }

    private fun js(code: String) {
        if (web.url?.let { isOwn(Uri.parse(it)) } != true) {
            bioBusy = false
            return
        }
        web.evaluateJavascript("window.__telex && $code", null)
    }

    /** A JavaScript string literal. */
    private fun q(s: String?) = JSONObject.quote(s ?: "")

    companion object {
        const val ACTION_LOCK = "com.wagnermartin.telex.LOCK"
        const val ACTION_SETTINGS = "com.wagnermartin.telex.SETTINGS"
        private const val STEP_LOGIN = "login"
        private const val STEP_UNLOCK = "unlock"
        private const val MODE_OPEN_FOLDER = 2
        private const val ACCEPT_SHARED = "telex/shared"
        private const val ACCEPT_FOLDER = "telex/folder"

        /** Coming back from a file picker or the settings within this time keeps the app open. */
        private const val OWN_UI_GRACE_MILLIS = 5 * 60 * 1000L
    }
}
