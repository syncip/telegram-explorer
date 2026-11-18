package com.wagnermartin.telex

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import android.security.keystore.UserNotAuthenticatedException
import android.util.Base64
import androidx.core.content.edit
import org.json.JSONObject
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.Mac
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** What the fingerprint can fill in. Null fields are not stored. */
data class Secrets(
    val username: String? = null,
    val password: String? = null,
    val passphrase: String? = null,
) {
    val hasLogin get() = !username.isNullOrEmpty() && !password.isNullOrEmpty()
    val hasPassphrase get() = !passphrase.isNullOrEmpty()
    val isEmpty get() = !hasLogin && !hasPassphrase
}

/** The fingerprint changed since the key was made, the stored data is gone. */
class SecretsInvalidatedException : Exception()

/**
 * Keeps the login and the vault passphrase encrypted with an Android Keystore key that
 * only works for [AUTH_SECONDS] after a strong biometric check, and that Android deletes
 * when a fingerprint is added or removed.
 *
 * Reading or writing throws [UserNotAuthenticatedException] when no fingerprint was
 * checked recently; the caller then shows the prompt and tries again.
 */
class SecretStore(context: Context) {
    private val sp = Prefs(context).sp

    /** Facts about the stored data that need no fingerprint, so the UI can show them. */
    val hasLogin get() = sp.getBoolean(META_LOGIN, false) && sp.contains(BLOB)
    val hasPassphrase get() = sp.getBoolean(META_PASSPHRASE, false) && sp.contains(BLOB)
    val username: String? get() = sp.getString(META_USER, null)
    val isEmpty get() = !sp.contains(BLOB)

    /** Whether the account asks for a 2FA code, which the fingerprint cannot provide. */
    var totp: Boolean
        get() = sp.getBoolean(META_TOTP, false)
        set(value) = sp.edit { putBoolean(META_TOTP, value) }

    /**
     * Whether [s] matches what is stored, compared through an HMAC with a second
     * Keystore key that needs no fingerprint. Keeps the app from asking again after
     * every login without keeping a plain hash of the password around.
     */
    fun knows(s: Secrets): Boolean {
        if (isEmpty) return false
        if (s.hasLogin && sp.getString(TAG_LOGIN, null) != tag("${s.username}\u0000${s.password}")) return false
        if (s.hasPassphrase && sp.getString(TAG_PASSPHRASE, null) != tag(s.passphrase!!)) return false
        return true
    }

    fun read(): Secrets {
        val blob = sp.getString(BLOB, null) ?: return Secrets()
        val raw = Base64.decode(blob, Base64.NO_WRAP)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        try {
            val key = key() ?: run { clear(); throw SecretsInvalidatedException() }
            cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, raw, 0, IV_SIZE))
        } catch (e: KeyPermanentlyInvalidatedException) {
            clear()
            throw SecretsInvalidatedException()
        }
        val json = JSONObject(String(cipher.doFinal(raw, IV_SIZE, raw.size - IV_SIZE), Charsets.UTF_8))
        fun opt(name: String) = if (json.has(name)) json.getString(name) else null
        return Secrets(opt("username"), opt("password"), opt("passphrase"))
    }

    fun write(s: Secrets) {
        if (s.isEmpty) {
            clear()
            return
        }
        val json = JSONObject()
        if (s.hasLogin) json.put("username", s.username).put("password", s.password)
        if (s.hasPassphrase) json.put("passphrase", s.passphrase)
        val cipher = Cipher.getInstance(TRANSFORMATION)
        try {
            cipher.init(Cipher.ENCRYPT_MODE, key() ?: createKey())
        } catch (e: KeyPermanentlyInvalidatedException) {
            clear()
            cipher.init(Cipher.ENCRYPT_MODE, createKey())
        }
        val ct = cipher.doFinal(json.toString().toByteArray(Charsets.UTF_8))
        val out = cipher.iv + ct
        sp.edit {
            putString(BLOB, Base64.encodeToString(out, Base64.NO_WRAP))
            putBoolean(META_LOGIN, s.hasLogin)
            putBoolean(META_PASSPHRASE, s.hasPassphrase)
            if (s.hasLogin) putString(META_USER, s.username) else remove(META_USER)
            if (s.hasLogin) putString(TAG_LOGIN, tag("${s.username}\u0000${s.password}")) else remove(TAG_LOGIN)
            if (s.hasPassphrase) putString(TAG_PASSPHRASE, tag(s.passphrase!!)) else remove(TAG_PASSPHRASE)
        }
    }

    fun clear() {
        sp.edit {
            remove(BLOB)
            remove(META_LOGIN)
            remove(META_PASSPHRASE)
            remove(META_USER)
            remove(TAG_LOGIN)
            remove(TAG_PASSPHRASE)
        }
        runCatching { keyStore().deleteEntry(ALIAS) }
    }

    private fun keyStore() = KeyStore.getInstance(PROVIDER).apply { load(null) }

    private fun key(): SecretKey? = keyStore().getKey(ALIAS, null) as SecretKey?

    private fun tag(value: String): String {
        val ks = keyStore()
        val key = ks.getKey(TAG_ALIAS, null) as SecretKey? ?: KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_HMAC_SHA256, PROVIDER).run {
            init(KeyGenParameterSpec.Builder(TAG_ALIAS, KeyProperties.PURPOSE_SIGN).build())
            generateKey()
        }
        val mac = Mac.getInstance("HmacSHA256").apply { init(key) }
        return Base64.encodeToString(mac.doFinal(value.toByteArray(Charsets.UTF_8)), Base64.NO_WRAP)
    }

    private fun createKey(): SecretKey {
        val spec = KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .setUserAuthenticationRequired(true)
            .setUserAuthenticationParameters(AUTH_SECONDS, KeyProperties.AUTH_BIOMETRIC_STRONG)
            .setInvalidatedByBiometricEnrollment(true)
            .build()
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, PROVIDER).run {
            init(spec)
            generateKey()
        }
    }

    companion object {
        /** One fingerprint covers logging in and unlocking the vault right after. */
        const val AUTH_SECONDS = 30
        private const val PROVIDER = "AndroidKeyStore"
        private const val ALIAS = "telex_secrets"
        private const val TAG_ALIAS = "telex_tags"
        private const val TAG_LOGIN = "secret_tag_login"
        private const val TAG_PASSPHRASE = "secret_tag_passphrase"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val IV_SIZE = 12
        private const val BLOB = "secret_blob"
        private const val META_LOGIN = "secret_has_login"
        private const val META_PASSPHRASE = "secret_has_passphrase"
        private const val META_USER = "secret_user"
        private const val META_TOTP = "secret_totp"

        fun isAuthError(e: Throwable) = e is UserNotAuthenticatedException ||
            (e is java.security.InvalidKeyException && e.cause is UserNotAuthenticatedException)
    }
}
