package net.veilon.app.vpn

import android.content.Context
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import net.veilon.core.libbox.CommandServer
import net.veilon.core.libbox.CommandServerHandler
import net.veilon.core.libbox.Libbox
import net.veilon.core.libbox.OverrideOptions
import net.veilon.core.libbox.SetupOptions
import net.veilon.core.libbox.SystemProxyStatus

/**
 * The only entry point into libbox (sing-box 1.13.21, bound into
 * veilon-core.aar by client/android/scripts/build-aar.sh). Everything else
 * talks to [CoreEngine], so a libbox API change is contained in this file
 * and [LibboxPlatform].
 *
 * API checked against the Java sources gobind generates for the pinned tag
 * (net.veilon.core.libbox.*):
 * Libbox.setup(SetupOptions), Libbox.newCommandServer(handler, platform),
 * CommandServer.startOrReloadService(config, OverrideOptions),
 * closeService(), close().
 */
interface CoreEngine {
    fun start(config: String)
    fun stop()
}

class LibboxEngine(vpn: VpnService, bypass: Set<String>) : CoreEngine {
    private val platform = LibboxPlatform(vpn, bypass)
    private var server: CommandServer? = null

    override fun start(config: String) {
        platform.startMonitor()
        // CommandServer.start() is deliberately NOT called: it would open the
        // libbox gRPC command socket. Nothing else in the app needs it, so no
        // other app gets an IPC surface into the core (ADR-0060, ADR-0061).
        val s = Libbox.newCommandServer(Handler(), platform)
        try {
            s.startOrReloadService(config, OverrideOptions())
        } catch (e: Exception) {
            runCatching { s.close() }
            platform.stopMonitor()
            throw e
        }
        server = s
    }

    override fun stop() {
        val s = server
        server = null
        runCatching { s?.closeService() }
        runCatching { s?.close() }
        platform.closeTun()
        platform.stopMonitor()
    }

    /** Callbacks from the core. No logs are written anywhere (ADR-0011). */
    private class Handler : CommandServerHandler {
        override fun serviceStop() {}
        override fun serviceReload() {}
        override fun getSystemProxyStatus(): SystemProxyStatus =
            SystemProxyStatus().apply {
                available = false
                enabled = false
            }
        override fun setSystemProxyEnabled(enabled: Boolean) {}
        override fun writeDebugMessage(message: String?) {}
    }

    companion object {
        /** Called once per process (VeilOnApp.onCreate). */
        fun setup(ctx: Context) {
            Libbox.setup(
                SetupOptions().apply {
                    // cache_file (selected outbound only, no history) lives here.
                    basePath = ctx.filesDir.absolutePath
                    workingPath = ctx.filesDir.absolutePath
                    tempPath = ctx.cacheDir.absolutePath
                    // golang/go#68760, same condition as the upstream client.
                    // Drop when the pinned Go release fixes it.
                    fixAndroidStack = Build.VERSION.SDK_INT >= Build.VERSION_CODES.P
                    // Not used: the command server is never started.
                    commandServerListenPort = 0
                    commandServerSecret = ""
                    logMaxLines = 0L
                    debug = false
                },
            )
        }

        internal fun close(fd: ParcelFileDescriptor?) {
            runCatching { fd?.close() }
        }
    }
}
