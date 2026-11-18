package com.wagnermartin.telex

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.WindowManager
import android.widget.Toast
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.appcompat.app.AppCompatActivity
import androidx.compose.runtime.mutableFloatStateOf
import androidx.core.content.IntentCompat
import com.wagnermartin.telex.ui.AppViewModel
import com.wagnermartin.telex.ui.Root
import com.wagnermartin.telex.ui.TelexTheme

/** The only screen of the app; everything else is drawn with Compose inside it. */
class MainActivity : AppCompatActivity() {
    private val vm: AppViewModel by viewModels()
    private val app get() = application as TelexApp
    private val textScale = mutableFloatStateOf(1f)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) handle(intent)
        setContent {
            TelexTheme(textScale.floatValue) { Root(vm) }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    override fun onStart() {
        super.onStart()
        if (app.prefs.secureScreen) window.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        else window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
        textScale.floatValue = app.prefs.textZoom / 100f
        vm.refresh()
    }

    private fun handle(intent: Intent?) {
        when (intent?.action) {
            ACTION_LOCK -> app.lock()
            ACTION_SETTINGS -> startActivity(Intent(this, SettingsActivity::class.java))
            Intent.ACTION_SEND -> {
                val uri = IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
                if (uri != null) vm.share(listOf(uri)) else Toast.makeText(this, R.string.share_no_file, Toast.LENGTH_LONG).show()
            }
            Intent.ACTION_SEND_MULTIPLE -> {
                val uris = IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
                if (uris.isNotEmpty()) vm.share(uris) else Toast.makeText(this, R.string.share_no_file, Toast.LENGTH_LONG).show()
            }
        }
    }

    companion object {
        const val ACTION_LOCK = "com.wagnermartin.telex.LOCK"
        const val ACTION_SETTINGS = "com.wagnermartin.telex.SETTINGS"
    }
}
