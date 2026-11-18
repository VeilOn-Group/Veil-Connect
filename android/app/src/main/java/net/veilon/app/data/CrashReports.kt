package net.veilon.app.data

import android.content.Context
import android.os.Build
import android.util.Log
import net.veilon.app.BuildConfig
import net.veilon.core.mobile.Mobile
import org.json.JSONObject
import java.io.File

/** A queued, already sanitized report (what would be sent, verbatim). */
data class CrashItem(val id: String, val component: String, val day: String, val title: String, val text: String, val json: String)

/**
 * Crash reports on Android (ADR-0077).
 *
 * Capture: uncaught JVM exceptions are written raw to a private file by
 * [install]; Go fatal errors (this bridge, libbox) go to another private
 * file through Mobile.installCrashOutput. On the next start [collect] turns
 * both into sanitized reports (the same Go sanitizer as the desktop),
 * deletes the raw files and keeps at most 10 reports for 30 days.
 *
 * Sending: only with the "crash_reports" setting on, or when the user
 * presses Send. The session lets the server rate-limit; the stored report
 * has no account, device or IP.
 */
class CrashReports(ctx: Context, private val api: ApiClient, private val access: () -> String) {
    private val dir = File(ctx.noBackupFilesDir, "crash").apply { mkdirs() }
    private val queue = File(dir, "queue").apply { mkdirs() }
    private val jvmTrace = File(dir, "jvm.trace")
    private val goTrace = File(dir, "go.trace")

    /** Installs both handlers; call first thing in Application.onCreate. */
    fun install() {
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { t, e ->
            // Raw only until the next start, in the app's private storage.
            runCatching { jvmTrace.writeText(Log.getStackTraceString(e).take(MAX_RAW)) }
            previous?.uncaughtException(t, e)
        }
        val goPrev = runCatching { Mobile.installCrashOutput(goTrace.absolutePath) }.getOrDefault("")
        if (goPrev.isNotBlank()) add("panic", goPrev)
    }

    /** Converts a JVM crash left by the previous run into a report. */
    fun collect() {
        if (jvmTrace.exists()) {
            val raw = runCatching { jvmTrace.readText() }.getOrDefault("")
            jvmTrace.delete()
            if (raw.isNotBlank()) add("exception", raw)
        }
        prune()
    }

    private fun add(kind: String, raw: String) {
        val arch = when (Build.SUPPORTED_ABIS.firstOrNull()) {
            "arm64-v8a" -> "arm64"; "armeabi-v7a" -> "arm"; "x86_64" -> "amd64"; "x86" -> "386"; else -> return
        }
        val json = runCatching { Mobile.newCrashReport(kind, BuildConfig.VERSION_NAME, arch, raw, System.currentTimeMillis() / 1000) }.getOrNull() ?: return
        val id = JSONObject(json).getString("id")
        File(queue, "$id.json").writeText(json)
        prune()
    }

    fun list(): List<CrashItem> = queue.listFiles { f -> f.name.endsWith(".json") }.orEmpty()
        .sortedByDescending { it.lastModified() }
        .mapNotNull { f ->
            runCatching {
                val o = JSONObject(f.readText())
                CrashItem(o.getString("id"), o.getString("component"), o.getString("day"), Mobile.crashTitle(o.getString("text")), o.getString("text"), o.toString())
            }.getOrNull()
        }

    fun delete(id: String) {
        if (ID.matches(id)) File(queue, "$id.json").delete()
    }

    /** Sends one report; removed once the server has it (or refuses it for good). */
    fun send(item: CrashItem) {
        try {
            api.call("POST", "/v1/crash-reports", item.json, access())
            delete(item.id)
        } catch (e: ApiException) {
            if (e.status == 400) delete(item.id)
            throw e
        }
    }

    /** Sends everything if the user opted in; network errors keep the queue. */
    fun flush(enabled: Boolean) {
        if (!enabled) return
        for (item in list()) runCatching { send(item) }.onFailure { if (it !is ApiException || it.status != 400) return }
    }

    private fun prune() {
        val all = queue.listFiles { f -> f.name.endsWith(".json") }.orEmpty().sortedByDescending { it.lastModified() }
        val cutoff = System.currentTimeMillis() - MAX_AGE_MS
        all.forEachIndexed { i, f -> if (i >= MAX_QUEUED || f.lastModified() < cutoff) f.delete() }
    }

    companion object {
        private const val MAX_RAW = 128 * 1024
        private const val MAX_QUEUED = 10
        private const val MAX_AGE_MS = 30L * 24 * 3600 * 1000
        private val ID = Regex("^[0-9a-f]{32}$")
    }
}
