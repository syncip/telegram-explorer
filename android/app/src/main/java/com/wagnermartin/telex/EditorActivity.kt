package com.wagnermartin.telex

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.view.View
import android.view.WindowManager
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.OnBackPressedCallback
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.updatePadding
import com.google.android.material.progressindicator.LinearProgressIndicator
import java.net.URLEncoder

/**
 * The live text editor of the web interface, full screen. Laptop and phone
 * edit the same text at the same time; the server stores it. It signs in with
 * the app's token as the session cookie, like [WebAdminActivity].
 */
class EditorActivity : AppCompatActivity() {
    private lateinit var web: WebView

    /** Called by the page when its close button was used. */
    private inner class Bridge {
        @JavascriptInterface
        fun close() = runOnUiThread { finish() }
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val app = application as TelexApp
        val origin = app.prefs.serverUrl
        val token = app.token.value
        val id = intent.getLongExtra(EXTRA_ID, 0)
        if (origin == null || token == null || id == 0L) {
            finish()
            return
        }
        if (app.prefs.secureScreen) window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        setContentView(R.layout.activity_web)
        val root = findViewById<View>(R.id.root)
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout() or WindowInsetsCompat.Type.ime())
            v.updatePadding(top = bars.top, bottom = bars.bottom, left = bars.left, right = bars.right)
            insets
        }
        // The page has its own header with the close button and the status.
        findViewById<View>(R.id.toolbar).visibility = View.GONE
        val progress = findViewById<LinearProgressIndicator>(R.id.progress)

        val secure = origin.startsWith("https://")
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setCookie(origin, "telex_session=$token; Path=/; SameSite=Strict" + if (secure) "; Secure" else "")
            flush()
        }

        web = findViewById(R.id.web)
        web.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            textZoom = app.prefs.textZoom
        }
        web.addJavascriptInterface(Bridge(), "TelexEditor")
        web.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                val url = request.url
                if (Prefs.origin(url) == origin) return false
                // Links in a note open in the browser.
                runCatching {
                    app.expectExternal()
                    startActivity(Intent(Intent.ACTION_VIEW, url))
                }
                return true
            }

            override fun onPageFinished(view: WebView, url: String?) {
                progress.visibility = View.GONE
            }
        }
        // Back closes the editor like its button, which waits for unsent changes.
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                web.evaluateJavascript("window.__telexBack ? window.__telexBack() : false") { handled ->
                    if (handled != "true") finish()
                }
            }
        })
        val device = URLEncoder.encode(deviceName(this), "UTF-8")
        if (savedInstanceState == null) web.loadUrl("$origin/?edit=$id&font=$FONT&device=$device")
        else web.restoreState(savedInstanceState)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::web.isInitialized) web.saveState(outState)
    }

    override fun onDestroy() {
        if (::web.isInitialized) {
            // Leaving the page tells the server this device is gone.
            web.loadUrl("about:blank")
            web.destroy()
        }
        super.onDestroy()
    }

    companion object {
        private const val EXTRA_ID = "id"
        private const val FONT = 18

        fun open(context: Context, id: Long) {
            context.startActivity(Intent(context, EditorActivity::class.java).putExtra(EXTRA_ID, id))
        }

        /** The name the user gave the phone, shown at its cursor on the other devices. */
        fun deviceName(context: Context): String =
            Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME)?.takeIf { it.isNotBlank() }
                ?: Build.MODEL
    }
}
