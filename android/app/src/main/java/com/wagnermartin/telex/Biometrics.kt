package com.wagnermartin.telex

import android.util.Log
import android.widget.Toast
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricManager.Authenticators.BIOMETRIC_STRONG
import androidx.biometric.BiometricManager.Authenticators.DEVICE_CREDENTIAL
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity

object Biometrics {
    private const val TAG = "TelexBiometrics"

    fun fingerprintReady(activity: FragmentActivity) =
        BiometricManager.from(activity).canAuthenticate(BIOMETRIC_STRONG) == BiometricManager.BIOMETRIC_SUCCESS

    /**
     * What the app lock asks for, or null when the device has neither a fingerprint nor a
     * screen lock and the app therefore cannot be locked.
     */
    fun appLockAuthenticators(activity: FragmentActivity, prefs: Prefs): Int? {
        val bm = BiometricManager.from(activity)
        val wanted = if (prefs.allowDeviceCredential) BIOMETRIC_STRONG or DEVICE_CREDENTIAL else BIOMETRIC_STRONG
        if (bm.canAuthenticate(wanted) == BiometricManager.BIOMETRIC_SUCCESS) return wanted
        // No fingerprint enrolled: the device screen lock is the next best thing.
        if (bm.canAuthenticate(DEVICE_CREDENTIAL) == BiometricManager.BIOMETRIC_SUCCESS) return DEVICE_CREDENTIAL
        return null
    }

    fun prompt(
        activity: FragmentActivity,
        title: String,
        subtitle: String?,
        authenticators: Int,
        onSuccess: () -> Unit,
        onCancel: () -> Unit = {},
    ) {
        val callback = object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) = onSuccess()

            override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                val quiet = errorCode == BiometricPrompt.ERROR_USER_CANCELED ||
                    errorCode == BiometricPrompt.ERROR_NEGATIVE_BUTTON ||
                    errorCode == BiometricPrompt.ERROR_CANCELED
                if (!quiet) Toast.makeText(activity, errString, Toast.LENGTH_LONG).show()
                onCancel()
            }
        }
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle(title)
            .apply { if (subtitle != null) setSubtitle(subtitle) }
            .setAllowedAuthenticators(authenticators)
            .apply { if (authenticators and DEVICE_CREDENTIAL == 0) setNegativeButtonText(activity.getString(R.string.cancel)) }
            .setConfirmationRequired(false)
            .build()
        BiometricPrompt(activity, ContextCompat.getMainExecutor(activity), callback).authenticate(info)
    }

    /**
     * Runs [op] on the stored secrets and asks for the fingerprint first when the key
     * needs it. [onDone] gets the result, [onCancel] runs when there is none.
     */
    fun <T> withSecrets(
        activity: FragmentActivity,
        title: String,
        op: () -> T,
        onDone: (T) -> Unit,
        onCancel: () -> Unit = {},
    ) {
        fun attempt(afterPrompt: Boolean) {
            try {
                onDone(op())
            } catch (e: SecretsInvalidatedException) {
                Toast.makeText(activity, R.string.bio_invalidated, Toast.LENGTH_LONG).show()
                onCancel()
            } catch (e: Exception) {
                if (!afterPrompt && SecretStore.isAuthError(e)) {
                    if (!fingerprintReady(activity)) {
                        Toast.makeText(activity, R.string.bio_not_available, Toast.LENGTH_LONG).show()
                        onCancel()
                        return
                    }
                    prompt(activity, title, null, BIOMETRIC_STRONG, { attempt(true) }, onCancel)
                } else {
                    Log.w(TAG, "secret store", e)
                    onCancel()
                }
            }
        }
        attempt(false)
    }
}
