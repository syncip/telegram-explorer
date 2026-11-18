package com.wagnermartin.telex

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.view.inputmethod.EditorInfo
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.updatePadding
import androidx.lifecycle.lifecycleScope
import com.google.android.material.button.MaterialButton
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.progressindicator.LinearProgressIndicator
import com.google.android.material.textfield.TextInputEditText
import com.google.android.material.textfield.TextInputLayout
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/** Asks for the server address and checks that a Telegram Explorer answers there. */
class ServerActivity : AppCompatActivity() {
    private lateinit var prefs: Prefs
    private lateinit var input: TextInputEditText
    private lateinit var layout: TextInputLayout
    private lateinit var button: MaterialButton
    private lateinit var progress: LinearProgressIndicator

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_server)
        prefs = Prefs(this)
        val root = findViewById<View>(R.id.root)
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime())
            v.updatePadding(top = bars.top, bottom = bars.bottom, left = bars.left, right = bars.right)
            insets
        }
        input = findViewById(R.id.url)
        layout = findViewById(R.id.url_layout)
        button = findViewById(R.id.connect)
        progress = findViewById(R.id.progress)
        input.setText(prefs.serverUrl ?: "")
        button.setOnClickListener { submit() }
        input.setOnEditorActionListener { _, id, _ ->
            if (id == EditorInfo.IME_ACTION_GO) submit()
            true
        }
    }

    private fun submit() {
        layout.error = null
        val url = normalize(input.text?.toString().orEmpty())
        if (url == null) {
            layout.error = getString(R.string.server_invalid)
            return
        }
        input.setText(url)
        if (url.startsWith("http://")) {
            MaterialAlertDialogBuilder(this)
                .setTitle(R.string.server_http_title)
                .setMessage(R.string.server_http_message)
                .setPositiveButton(R.string.server_http_continue) { _, _ -> check(url) }
                .setNegativeButton(R.string.cancel, null)
                .show()
        } else {
            check(url)
        }
    }

    private fun check(url: String) {
        button.isEnabled = false
        progress.visibility = View.VISIBLE
        lifecycleScope.launch {
            val problem = withContext(Dispatchers.IO) { probe(url) }
            button.isEnabled = true
            progress.visibility = View.INVISIBLE
            if (problem != null) {
                layout.error = problem
                return@launch
            }
            val changed = prefs.serverUrl != url
            prefs.serverUrl = url
            if (changed) SecretStore(this@ServerActivity).clear()
            startActivity(Intent(this@ServerActivity, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP))
            finish()
        }
    }

    /** Returns an error text, or null when /api/state looks like Telegram Explorer. */
    private fun probe(url: String): String? = try {
        val conn = URL("$url/api/state").openConnection() as HttpURLConnection
        conn.connectTimeout = 10_000
        conn.readTimeout = 10_000
        conn.instanceFollowRedirects = false
        try {
            val code = conn.responseCode
            if (code != 200) {
                getString(R.string.server_not_telex, "HTTP $code")
            } else {
                val json = JSONObject(conn.inputStream.bufferedReader().readText())
                if (json.has("needsSetup") && json.has("locked")) null else getString(R.string.server_not_telex, "unbekannte Antwort")
            }
        } finally {
            conn.disconnect()
        }
    } catch (e: Exception) {
        getString(R.string.server_unreachable, e.localizedMessage ?: e.javaClass.simpleName)
    }

    companion object {
        /** Keeps scheme, host and port. The web interface always lives at the root. */
        fun normalize(raw: String): String? {
            var s = raw.trim()
            if (s.isEmpty()) return null
            if (!s.contains("://")) s = "https://$s"
            val uri = Uri.parse(s)
            if (uri.scheme !in setOf("http", "https") || uri.host.isNullOrEmpty()) return null
            return Prefs.origin(uri)
        }
    }
}
