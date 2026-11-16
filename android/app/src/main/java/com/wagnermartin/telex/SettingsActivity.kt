package com.wagnermartin.telex

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.webkit.WebView
import android.widget.Toast
import androidx.activity.enableEdgeToEdge
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.updatePadding
import androidx.preference.Preference
import androidx.preference.PreferenceFragmentCompat
import androidx.preference.SwitchPreferenceCompat
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import java.io.File

class SettingsActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_settings)
        val root = findViewById<View>(R.id.root)
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
            v.updatePadding(top = bars.top)
            insets
        }
        findViewById<com.google.android.material.appbar.MaterialToolbar>(R.id.toolbar).setNavigationOnClickListener { finish() }
        if (savedInstanceState == null) {
            supportFragmentManager.beginTransaction().replace(R.id.settings, SettingsFragment()).commit()
        }
    }

    class SettingsFragment : PreferenceFragmentCompat() {
        private lateinit var prefs: Prefs
        private lateinit var store: SecretStore

        override fun onCreatePreferences(savedInstanceState: Bundle?, rootKey: String?) {
            prefs = Prefs(requireContext())
            store = SecretStore(requireContext())
            setPreferencesFromResource(R.xml.preferences, rootKey)

            pref("server").setOnPreferenceClickListener {
                startActivity(Intent(requireContext(), ServerActivity::class.java))
                true
            }

            // Turning the lock off needs the same check as opening the app.
            findPreference<SwitchPreferenceCompat>(Prefs.KEY_APP_LOCK)!!.setOnPreferenceChangeListener { p, value ->
                if (value == true) return@setOnPreferenceChangeListener true
                val auth = Biometrics.appLockAuthenticators(requireActivity(), prefs)
                    ?: return@setOnPreferenceChangeListener true
                Biometrics.prompt(requireActivity(), getString(R.string.confirm_disable_lock), null, auth, onSuccess = {
                    (p as SwitchPreferenceCompat).isChecked = false
                })
                false
            }

            pref("bio_clear").setOnPreferenceClickListener {
                MaterialAlertDialogBuilder(requireContext())
                    .setTitle(R.string.pref_bio_clear)
                    .setPositiveButton(R.string.pref_bio_clear) { _, _ ->
                        store.clear()
                        updateStored()
                        Toast.makeText(requireContext(), R.string.pref_bio_cleared, Toast.LENGTH_SHORT).show()
                    }
                    .setNegativeButton(R.string.cancel, null)
                    .show()
                true
            }

            pref("clear_cache").setOnPreferenceClickListener {
                WebView(requireContext()).apply {
                    clearCache(true)
                    destroy()
                }
                File(requireContext().cacheDir, "open").deleteRecursively()
                Toast.makeText(requireContext(), R.string.pref_cache_cleared, Toast.LENGTH_SHORT).show()
                true
            }

            val info = requireContext().packageManager.getPackageInfo(requireContext().packageName, 0)
            pref("about").summary = info.versionName
        }

        override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
            super.onViewCreated(view, savedInstanceState)
            ViewCompat.setOnApplyWindowInsetsListener(listView) { v, insets ->
                val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars())
                v.updatePadding(left = bars.left, right = bars.right, bottom = bars.bottom)
                insets
            }
            listView.clipToPadding = false
        }

        override fun onResume() {
            super.onResume()
            pref("server").summary = prefs.serverUrl
            updateStored()
        }

        private fun updateStored() {
            val parts = buildList {
                if (store.hasLogin) add("Anmeldung als ${store.username}" + if (store.totp) " (2FA-Code nötig)" else "")
                if (store.hasPassphrase) add("Passphrase des Tresors")
            }
            pref("bio_stored").summary = if (parts.isEmpty()) getString(R.string.pref_bio_stored_none) else parts.joinToString("\n")
            pref("bio_clear").isEnabled = parts.isNotEmpty()
        }

        private fun pref(key: String) = findPreference<Preference>(key)!!
    }
}
