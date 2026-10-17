package uk.co.dvhub.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

class CredentialsStore(context: Context) {
    private val prefs = context.getSharedPreferences("dvhub_secure", Context.MODE_PRIVATE)
    private val alias = "dvhub_gateway_credentials"

    private fun key(): SecretKey {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        return KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").run {
            init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .build())
            generateKey()
        }
    }

    fun save(value: GatewaySettings) {
        val clear = listOf(value.serverUrl, value.username, value.password, value.callsign, value.dmrId, value.essid, value.manualDmrId, value.accountType)
            .joinToString("\u001f").toByteArray(Charsets.UTF_8)
        val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply { init(Cipher.ENCRYPT_MODE, key()) }
        prefs.edit().putString("blob", Base64.encodeToString(cipher.iv + cipher.doFinal(clear), Base64.NO_WRAP)).apply()
    }

    fun load(): GatewaySettings {
        return runCatching {
            val blob = prefs.getString("blob", null) ?: return GatewaySettings()
            val bytes = Base64.decode(blob, Base64.NO_WRAP)
            require(bytes.size > 12) { "Invalid encrypted settings" }
            val cipher = Cipher.getInstance("AES/GCM/NoPadding").apply {
                init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, bytes.copyOfRange(0, 12)))
            }
            val parts = String(cipher.doFinal(bytes.copyOfRange(12, bytes.size)), Charsets.UTF_8).split("\u001f")
            require(parts.size >= 6) { "Unsupported settings format" }
            GatewaySettings(parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts.getOrElse(6) { "" }, parts.getOrElse(7) { "admin" })
        }.getOrElse {
            // Preferences and Android Keystore entries can become unreadable after an
            // app upgrade, device restore or lock-screen change. Discard only the
            // unusable local credentials so startup can recover to the setup screen.
            reset()
            GatewaySettings()
        }
    }

    fun reset() {
        prefs.edit().remove("blob").commit()
        runCatching {
            KeyStore.getInstance("AndroidKeyStore").apply {
                load(null)
                if (containsAlias(alias)) deleteEntry(alias)
            }
        }
    }
}
