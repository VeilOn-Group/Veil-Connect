package net.veilon.app.vpn

import android.content.Context
import java.io.File
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * Runs the bundled Xray-core (lib/<abi>/libxray.so, official release pinned
 * in build/xray.lock) for the VLESS transports, XHTTP and Vision.
 *
 * sing-box (libbox, in process) hands VLESS traffic to Xray through a local
 * SOCKS5 endpoint with a per-start password. Xray runs under the app's own
 * UID, which the VpnService excludes from the tunnel, so its connections to
 * the node do not loop back into the TUN.
 *
 * Privacy: the config disables every Xray log; stdout and stderr are drained
 * and dropped. The config file is private to the app (0600) and
 * deleted on stop.
 */
class XrayProcess(private val ctx: Context) {
    @Volatile private var proc: Process? = null
    @Volatile private var stopped = false
    private val config = File(ctx.noBackupFilesDir, "xray.json")

    /** The bundled binary, or null on ABIs without an official build. */
    fun binary(): File? = File(ctx.applicationInfo.nativeLibraryDir, "libxray.so").takeIf { it.canExecute() }

    fun start(configJson: String) {
        val bin = binary() ?: throw IllegalStateException("xray binary not bundled for this device")
        stopped = false
        config.delete()
        config.createNewFile()
        config.setReadable(false, false); config.setReadable(true, true)
        config.setWritable(false, false); config.setWritable(true, true)
        config.writeText(configJson)
        launch(bin)
    }

    private fun launch(bin: File) {
        val p = ProcessBuilder(bin.absolutePath, "run", "-c", config.absolutePath)
            .redirectErrorStream(true)
            .apply { environment().clear() }
            .start()
        proc = p
        thread(name = "xray-drain", isDaemon = true) {
            runCatching { p.inputStream.use { s -> val buf = ByteArray(4096); while (s.read(buf) >= 0) { /* dropped */ } } }
            // Restart with a short pause if it died while it should run.
            if (!stopped) {
                Thread.sleep(1000)
                if (!stopped) runCatching { launch(bin) }
            }
        }
    }

    fun stop() {
        stopped = true
        proc?.let { p ->
            p.destroy()
            if (!p.waitFor(3, TimeUnit.SECONDS)) p.destroyForcibly()
        }
        proc = null
        config.delete()
    }
}
