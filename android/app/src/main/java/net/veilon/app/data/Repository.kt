package net.veilon.app.data

import android.content.Context
import net.veilon.app.BuildConfig
import net.veilon.core.mobile.Mobile
import org.json.JSONArray
import org.json.JSONObject

/** User settings (local only). */
data class Settings(
    val nodeId: String = "",
    val allowLan: Boolean = true,
    val autoSwitch: Boolean = true,
    val disabled: Set<String> = emptySet(),
    val bypassPackages: Set<String> = emptySet(),
    // Automatic split tunnelling: .ru and .su sites outside the VPN (ADR-0064).
    val bypassRu: Boolean = true,
    // DoH resolver through the tunnel (ADR-0073): quad9 (default),
    // cloudflare, google, adguard, opendns or custom with dnsCustomUrl.
    val dnsProvider: String = "quad9",
    val dnsCustomUrl: String = "",
    // Connect after the device boots (ADR-0076): needs the VPN permission
    // granted once; Always-on VPN in system settings is the OS-level option.
    val autoConnect: Boolean = false,
    // Send sanitized crash reports automatically (ADR-0077); off by default.
    val crashReports: Boolean = false,
) {
    fun toJSON(): String = JSONObject()
        .put("node_id", nodeId).put("allow_lan", allowLan).put("auto_switch", autoSwitch)
        .put("disabled", JSONArray(disabled.toList())).put("bypass", JSONArray(bypassPackages.toList()))
        .put("bypass_ru", bypassRu)
        .put("dns_provider", dnsProvider).put("dns_custom_url", dnsCustomUrl)
        .put("auto_connect", autoConnect).put("crash_reports", crashReports)
        .toString()

    companion object {
        fun fromJSON(s: String?): Settings {
            if (s.isNullOrEmpty()) return Settings()
            val o = JSONObject(s)
            fun set(k: String) = o.optJSONArray(k)?.let { a -> (0 until a.length()).map { a.getString(it) }.toSet() } ?: emptySet()
            return Settings(o.optString("node_id"), o.optBoolean("allow_lan", true), o.optBoolean("auto_switch", true), set("disabled"), set("bypass"), o.optBoolean("bypass_ru", true),
                o.optString("dns_provider", "quad9").ifEmpty { "quad9" }, o.optString("dns_custom_url"),
                o.optBoolean("auto_connect", false), o.optBoolean("crash_reports", false))
        }
    }
}

data class Location(val id: String, val name: String, val region: String, val transports: List<String>)
data class ProfileView(val plan: String, val endsOn: String, val locations: List<Location>)

/**
 * Account and profile logic. Every security decision (signature checks,
 * validation, config generation) is delegated to the shared Go core through
 * gomobile (net.veilon.core.mobile.Mobile), the same code the desktop uses.
 */
class Repository(ctx: Context) {
    val store = SecureStore(ctx)
    private val roots = BuildConfig.ROOT_KEYS
    private val version = BuildConfig.VERSION_NAME
    private val api = ApiClient(
        BuildConfig.API_BASE_URLS.split(',').map { it.trim() }.filter { it.isNotEmpty() },
        BuildConfig.API_PINS.split(',').map { it.trim() }.filter { it.isNotEmpty() },
    )

    val crashes = CrashReports(ctx, api) { access() }

    val loggedIn get() = store.refreshToken != null
    var settings: Settings
        get() = Settings.fromJSON(store.settingsJSON)
        set(v) { store.settingsJSON = v.toJSON() }

    private fun now() = System.currentTimeMillis() / 1000

    private fun deviceSeed(): String = store.deviceSeed ?: Mobile.newDeviceKey().also { store.deviceSeed = it }

    private fun proof(): JSONObject {
        val nonce = JSONObject(api.call("POST", "/v1/auth/challenge", "{}")).getString("nonce")
        return JSONObject(Mobile.deviceProof(deviceSeed(), nonce))
    }

    private fun saveTokens(t: JSONObject) {
        store.accessToken = t.getString("access_token")
        store.refreshToken = t.getString("refresh_token")
        store.accessExp = now() + t.optLong("expires_in", 600)
    }

    /** Creates an anonymous account; returns the account number (shown once). */
    fun register(): String {
        val t = JSONObject(api.call("POST", "/v1/auth/register", JSONObject().put("device_proof", proof()).put("device_label", "Android").toString()))
        saveTokens(t)
        val number = t.getString("account_number")
        store.accountNumber = number
        refreshProfile()
        return number
    }

    fun login(number: String) {
        val body = JSONObject().put("account_number", number).put("device_proof", proof()).put("device_label", "Android")
        saveTokens(JSONObject(api.call("POST", "/v1/auth/login", body.toString())))
        store.accountNumber = number
        refreshProfile()
    }

    private fun access(): String {
        val a = store.accessToken
        if (a != null && now() + 60 < store.accessExp) return a
        val r = store.refreshToken ?: throw IllegalStateException("not signed in")
        saveTokens(JSONObject(api.call("POST", "/v1/auth/refresh", JSONObject().put("refresh_token", r).put("device_proof", proof()).toString())))
        return store.accessToken!!
    }

    /** Downloads keyset + profile and stores them only after verification. */
    fun refreshProfile(): ProfileView {
        val ks = api.call("GET", "/v1/keyset")
        val v = Mobile.keysetVersion(roots, ks, now())
        if (v < store.keysetVersion) throw IllegalStateException("keyset downgrade")
        val env = api.call("GET", "/v1/profile", bearer = access())
        val view = summary(ks, env, v)
        store.keysetJSON = ks
        store.keysetVersion = v
        store.profileJSON = env
        return view
    }

    fun cachedProfile(): ProfileView? {
        val ks = store.keysetJSON ?: return null
        val env = store.profileJSON ?: return null
        return runCatching { summary(ks, env, store.keysetVersion) }.getOrNull()
    }

    private fun summary(ks: String, env: String, minVersion: Long): ProfileView {
        val o = JSONObject(Mobile.profileSummary(roots, ks, env, minVersion, now(), version))
        val sub = o.getJSONObject("subscription")
        val nodes = o.optJSONArray("nodes") ?: JSONArray()
        return ProfileView(
            sub.optString("plan"), sub.optString("ends_on"),
            (0 until nodes.length()).map { i ->
                val n = nodes.getJSONObject(i)
                val tr = n.optJSONArray("transports") ?: JSONArray()
                Location(n.getString("id"), n.getString("name"), n.getString("region"), (0 until tr.length()).map { tr.getString(it) })
            },
        )
    }

    /** Verified sing-box config for libbox and, for VLESS, the Xray config (ADR-0066). */
    data class Configs(val singBox: String, val xray: String)

    /**
     * Builds both configs. [xrayAvailable] is false on ABIs without a bundled
     * Xray binary: the VLESS transports are then switched off.
     */
    fun buildConfigs(clashListen: String, clashSecret: String, cacheFile: String, filesDir: String, xrayAvailable: Boolean): Configs {
        val s = settings
        val rules = JSONArray()
        if (s.bypassPackages.isNotEmpty()) rules.put(JSONObject().put("package_name", JSONArray(s.bypassPackages.toList())).put("action", "direct"))
        val disabled = s.disabled.toMutableSet()
        if (!xrayAvailable) disabled.addAll(listOf("xhttp", "vless"))
        val opts = JSONObject()
            .put("node_id", s.nodeId).put("allow_lan", s.allowLan)
            .put("disabled", JSONArray(disabled.toList()))
            .put("clash_api_listen", clashListen).put("clash_api_secret", clashSecret)
            .put("cache_file_path", cacheFile).put("split_rules", rules)
            .put("bypass_ru", s.bypassRu).put("files_dir", filesDir)
            .put("dns_provider", s.dnsProvider).put("dns_custom_url", s.dnsCustomUrl)
        val b = JSONObject(Mobile.buildConfigs(roots, store.keysetJSON, store.profileJSON, opts.toString(), store.keysetVersion, now(), version))
        return Configs(b.getString("singbox"), b.optString("xray"))
    }

    // ---- payments (ADR-0023): the provider page opens in the browser, the
    // order is polled; nothing about purchases is stored on the device ----

    fun offers(): List<Offer> {
        val a = JSONArray(api.call("GET", "/v1/billing/offers"))
        return (0 until a.length()).map { i ->
            val o = a.getJSONObject(i)
            val pr = o.getJSONArray("prices")
            Offer(
                o.getString("plan_id"), o.getString("name"), o.getInt("duration_days"), o.getInt("device_limit"),
                (0 until pr.length()).map { j -> pr.getJSONObject(j).let { Price(it.getString("provider"), it.getLong("amount_minor"), it.getString("currency")) } },
            )
        }
    }

    /** Starts a payment; returns the order id and the checked provider page. */
    fun checkout(planId: String, provider: String): Pair<String, String> {
        val r = JSONObject(api.call("POST", "/v1/billing/checkout", JSONObject().put("plan_id", planId).put("provider", provider).toString(), access()))
        val url = r.getString("confirmation_url")
        if (!paymentUrlAllowed(url)) throw IllegalStateException("unexpected payment page")
        return r.getString("order_id") to url
    }

    /** Order status; refreshes the profile once the order is fulfilled. */
    fun orderStatus(id: String): String {
        val st = JSONObject(api.call("GET", "/v1/billing/orders/$id", bearer = access())).getString("status")
        if (st == "fulfilled") runCatching { refreshProfile() }
        return st
    }

    // ---- support (ADR-0028…0032): relayed as is, nothing stored on the device ----

    fun supportTickets(): List<SupportTicket> {
        val a = JSONArray(api.call("GET", "/v1/support/tickets", bearer = access()))
        return (0 until a.length()).map { SupportTicket.from(a.getJSONObject(it)) }
    }

    fun supportThread(id: String): SupportThread = SupportThread.from(JSONObject(api.call("GET", "/v1/support/tickets/$id", bearer = access())))

    fun supportOpen(subject: String, body: String, lang: String): SupportThread =
        SupportThread.from(JSONObject(api.call("POST", "/v1/support/tickets", JSONObject().put("subject", subject).put("body", body).put("lang", lang).toString(), access())))

    fun supportPost(id: String, body: String): SupportThread =
        SupportThread.from(JSONObject(api.call("POST", "/v1/support/tickets/$id/messages", JSONObject().put("body", body).toString(), access())))

    fun supportHuman(id: String): SupportThread = SupportThread.from(JSONObject(api.call("POST", "/v1/support/tickets/$id/human", bearer = access())))

    fun supportClose(id: String) { api.call("POST", "/v1/support/tickets/$id/close", bearer = access()) }

    /**
     * Diagnostics without log lines (the Android core log is not kept):
     * versions, settings summary and the profile state. Nothing here is an
     * address or an identifier; the server redacts again anyway.
     */
    fun diagnostics(vpnState: String): String = JSONObject()
        .put("format", "veilon-diagnostics/1")
        .put("client", JSONObject().put("version", version).put("os", "android " + android.os.Build.VERSION.RELEASE).put("sdk", android.os.Build.VERSION.SDK_INT))
        .put("state", JSONObject().put("state", vpnState).put("has_profile", store.profileJSON != null))
        .put("settings", JSONObject().put("bypass_ru", settings.bypassRu).put("allow_lan", settings.allowLan)
            .put("disabled_transports", settings.disabled.size).put("bypass_apps", settings.bypassPackages.size))
        .toString()

    fun attachDiagnostics(id: String, report: String) {
        api.call("POST", "/v1/support/tickets/$id/attachments", report, access())
    }

    /** DoH presets from the shared core: id to display name. */
    fun dnsPresets(): List<Pair<String, String>> {
        val a = JSONArray(Mobile.listDNSPresets())
        return (0 until a.length()).map { a.getJSONObject(it).let { o -> o.getString("id") to o.getString("name") } }
    }

    /** "" when the choice is valid, otherwise the reason (shared validation). */
    fun validateDns(provider: String, customUrl: String): String = Mobile.validateDNS(provider, customUrl)

    fun exportAccount(): String = api.call("GET", "/v1/account/export", bearer = access())

    fun deleteAccount() {
        api.call("DELETE", "/v1/account", JSONObject().put("confirm", "DELETE").toString(), access())
        store.clearSession()
    }

    fun signOut() {
        runCatching { api.call("POST", "/v1/auth/logout", JSONObject().put("refresh_token", store.refreshToken).toString(), access()) }
        store.clearSession()
    }
}

data class Price(val provider: String, val amountMinor: Long, val currency: String)
data class Offer(val planId: String, val name: String, val days: Int, val devices: Int, val prices: List<Price>)

/** Same rule as veilond (core.PaymentURLAllowed): https on a provider host. */
fun paymentUrlAllowed(u: String): Boolean {
    val uri = runCatching { java.net.URI(u) }.getOrNull() ?: return false
    if (uri.scheme != "https" || uri.userInfo != null || uri.port != -1) return false
    val h = uri.host?.lowercase() ?: return false
    return listOf("yoomoney.ru", "yookassa.ru", "pay.heleket.com").any { h == it || h.endsWith(".$it") }
}

data class SupportTicket(val id: String, val subject: String, val status: String, val updated: String) {
    companion object {
        fun from(o: JSONObject) = SupportTicket(o.getString("id"), o.getString("subject"), o.getString("status"), o.getString("updated_at").take(10))
    }
}

data class SupportMessage(val id: Long, val author: String, val body: String)

data class SupportThread(val ticket: SupportTicket, val messages: List<SupportMessage>) {
    companion object {
        fun from(o: JSONObject): SupportThread {
            val ms = o.getJSONArray("messages")
            return SupportThread(
                SupportTicket.from(o.getJSONObject("ticket")),
                (0 until ms.length()).map { i ->
                    ms.getJSONObject(i).let { SupportMessage(it.getLong("id"), it.getString("author"), it.getString("body").replace(Regex("\\s*\\[kb:[a-z0-9-]+\\]"), "")) }
                },
            )
        }
    }
}
