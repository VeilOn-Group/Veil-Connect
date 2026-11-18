package net.veilon.app.data

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey

/**
 * Everything the app keeps lives here, encrypted with a Keystore-backed key
 * and excluded from backups (data_extraction_rules.xml). There is no history:
 * no connection times, no visited hosts, no traffic log (ADR-0034).
 */
class SecureStore(ctx: Context) {
    private val prefs: SharedPreferences = EncryptedSharedPreferences.create(
        ctx,
        "veilon.secure",
        MasterKey.Builder(ctx).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
    )

    var deviceSeed: String? by str("device_seed")
    var accessToken: String? by str("access")
    var refreshToken: String? by str("refresh")
    var accessExp: Long
        get() = prefs.getLong("access_exp", 0)
        set(v) = prefs.edit().putLong("access_exp", v).apply()
    var accountNumber: String? by str("account_number")
    var keysetJSON: String? by str("keyset")
    var keysetVersion: Long
        get() = prefs.getLong("keyset_version", 0)
        set(v) = prefs.edit().putLong("keyset_version", v).apply()
    var profileJSON: String? by str("profile")
    var settingsJSON: String? by str("settings")

    /** Sign-out / account deletion: wipe the session, keep only settings. */
    fun clearSession() {
        val e = prefs.edit()
        listOf("device_seed", "access", "refresh", "access_exp", "account_number", "keyset", "keyset_version", "profile").forEach { e.remove(it) }
        e.apply()
    }

    private fun str(key: String) = object : kotlin.properties.ReadWriteProperty<Any?, String?> {
        override fun getValue(thisRef: Any?, property: kotlin.reflect.KProperty<*>): String? = prefs.getString(key, null)
        override fun setValue(thisRef: Any?, property: kotlin.reflect.KProperty<*>, value: String?) {
            val e = prefs.edit()
            if (value == null) e.remove(key) else e.putString(key, value)
            e.apply()
        }
    }
}
