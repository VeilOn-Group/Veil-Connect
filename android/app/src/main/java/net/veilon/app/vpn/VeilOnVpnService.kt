package net.veilon.app.vpn

import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.VpnService
import android.os.Build
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import net.veilon.app.R
import net.veilon.app.VeilOnApp
import net.veilon.app.ui.MainActivity
import java.security.SecureRandom

enum class VpnState { DISCONNECTED, CONNECTING, CONNECTED, FAILED }

/**
 * VpnService hosting sing-box (libbox). Supports Always-on VPN; together with
 * the system option "Block connections without VPN" this is the Android kill
 * switch (enforced by the OS even if this process dies).
 */
class VeilOnVpnService : VpnService() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var engine: CoreEngine? = null
    private var xray: XrayProcess? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopCore()
            stopSelf()
            return START_NOT_STICKY
        }
        // intent == null or ACTION_START, or the system starting us for Always-on.
        startForegroundCompat()
        scope.launch { startCore() }
        return START_STICKY
    }

    private fun startCore() {
        val repo = (application as VeilOnApp).repo
        state.value = VpnState.CONNECTING
        try {
            stopCoreQuietly()
            val secret = ByteArray(24).also { SecureRandom().nextBytes(it) }.joinToString("") { "%02x".format(it) }
            val x = XrayProcess(this)
            val cfg = repo.buildConfigs(
                "127.0.0.1:${freePort()}", secret, filesDir.resolve("cache.db").absolutePath,
                noBackupFilesDir.absolutePath, x.binary() != null,
            )
            // Xray first: sing-box selects XHTTP as soon as it starts.
            if (cfg.xray.isNotEmpty()) xray = x.also { it.start(cfg.xray) }
            engine = LibboxEngine(this, repo.settings.bypassPackages).also { it.start(cfg.singBox) }
            state.value = VpnState.CONNECTED
        } catch (e: Exception) {
            lastError.value = e.message
            state.value = VpnState.FAILED
        }
    }

    private fun stopCoreQuietly() {
        runCatching { engine?.stop() }
        engine = null
        runCatching { xray?.stop() }
        xray = null
    }

    private fun stopCore() {
        stopCoreQuietly()
        state.value = VpnState.DISCONNECTED
    }

    override fun onRevoke() {
        stopCore()
        stopSelf()
    }

    override fun onDestroy() {
        stopCore()
        scope.cancel()
        super.onDestroy()
    }

    private fun startForegroundCompat() {
        val open = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE)
        val n = Notification.Builder(this, VeilOnApp.CHANNEL_VPN)
            .setSmallIcon(R.drawable.ic_launcher_monochrome)
            .setContentTitle(getString(R.string.vpn_notification))
            .setContentIntent(open)
            .setOngoing(true)
            .build()
        if (Build.VERSION.SDK_INT >= 34) startForeground(1, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE) else startForeground(1, n)
    }

    private fun freePort(): Int = java.net.ServerSocket(0, 1, java.net.InetAddress.getByName("127.0.0.1")).use { it.localPort }

    companion object {
        const val ACTION_START = "net.veilon.START"
        const val ACTION_STOP = "net.veilon.STOP"
        val state = MutableStateFlow(VpnState.DISCONNECTED)
        val lastError = MutableStateFlow<String?>(null)
        val stateFlow: StateFlow<VpnState> get() = state

        fun start(ctx: Context) = ctx.startForegroundService(Intent(ctx, VeilOnVpnService::class.java).setAction(ACTION_START))
        fun stop(ctx: Context) = ctx.startService(Intent(ctx, VeilOnVpnService::class.java).setAction(ACTION_STOP))
    }
}
