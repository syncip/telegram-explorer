package com.wagnermartin.telex.data

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import androidx.core.content.edit
import com.wagnermartin.telex.Prefs
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Keeps the app's login token, encrypted with a key in the Android keystore.
 * The token alone opens the files once the vault is unlocked, so it never
 * leaves the device: backups are off and the key cannot be exported.
 */
class TokenStore(context: Context) {
    private val sp = Prefs(context).sp

    /** The token for [origin], or null when the app is not logged in there. */
    fun read(origin: String): String? {
        if (sp.getString(KEY_ORIGIN, null) != origin) return null
        val blob = sp.getString(KEY_TOKEN, null) ?: return null
        return runCatching {
            val raw = Base64.decode(blob, Base64.NO_WRAP)
            val cipher = Cipher.getInstance(TRANSFORMATION)
            cipher.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, raw, 0, IV_SIZE))
            String(cipher.doFinal(raw, IV_SIZE, raw.size - IV_SIZE), Charsets.UTF_8)
        }.getOrNull()
    }

    fun write(origin: String, token: String) {
        val cipher = Cipher.getInstance(TRANSFORMATION)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val out = cipher.iv + cipher.doFinal(token.toByteArray(Charsets.UTF_8))
        sp.edit {
            putString(KEY_ORIGIN, origin)
            putString(KEY_TOKEN, Base64.encodeToString(out, Base64.NO_WRAP))
        }
    }

    fun clear() = sp.edit { remove(KEY_TOKEN); remove(KEY_ORIGIN) }

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance(PROVIDER).apply { load(null) }
        (ks.getKey(ALIAS, null) as SecretKey?)?.let { return it }
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .build()
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER).run { init(spec); generateKey() }
    }

    private companion object {
        const val PROVIDER = "AndroidKeyStore"
        const val ALIAS = "telex_token"
        const val TRANSFORMATION = "AES/GCM/NoPadding"
        const val IV_SIZE = 12
        const val KEY_TOKEN = "app_token"
        const val KEY_ORIGIN = "app_token_origin"
    }
}
