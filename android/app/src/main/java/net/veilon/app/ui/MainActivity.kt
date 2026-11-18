package net.veilon.app.ui

import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import android.provider.Settings as AndroidSettings
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import net.veilon.app.R
import net.veilon.app.VeilOnApp
import net.veilon.app.data.Location
import net.veilon.app.data.Offer
import net.veilon.app.data.Price
import net.veilon.app.data.SupportThread
import net.veilon.app.data.SupportTicket
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import net.veilon.app.data.ProfileView
import net.veilon.app.vpn.VeilOnVpnService
import net.veilon.app.vpn.VpnState

class MainActivity : ComponentActivity() {
    private val repo get() = (application as VeilOnApp).repo
    private val vpnPermission = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) {
        if (VpnService.prepare(this) == null) VeilOnVpnService.start(this)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent { VeilOnTheme { App() } }
    }

    private fun connect() {
        val prep = VpnService.prepare(this)
        if (prep != null) vpnPermission.launch(prep) else VeilOnVpnService.start(this)
    }

    private fun io(block: suspend () -> Unit) = lifecycleScope.launch { block() }

    @Composable
    private fun App() {
        var loggedIn by remember { mutableStateOf(repo.loggedIn) }
        var profile by remember { mutableStateOf(repo.cachedProfile()) }
        var tab by remember { mutableStateOf("home") }
        var error by remember { mutableStateOf<String?>(null) }
        val vpn by VeilOnVpnService.stateFlow.collectAsState()

        Surface(color = Tokens.bgBase, modifier = Modifier.fillMaxSize()) {
            Column(Modifier.fillMaxSize().systemBarsPadding()) {
                Row(Modifier.fillMaxWidth().padding(20.dp), verticalAlignment = Alignment.CenterVertically) {
                    Wordmark()
                }
                Box(Modifier.weight(1f).padding(horizontal = 20.dp)) {
                    if (!loggedIn) {
                        SignIn(onDone = { loggedIn = true; profile = repo.cachedProfile() }, onError = { error = it })
                    } else when (tab) {
                        "home" -> Home(vpn, profile, onLocations = { tab = "locations" })
                        "locations" -> Locations(profile) { id ->
                            repo.settings = repo.settings.copy(nodeId = id)
                            if (vpn == VpnState.CONNECTED) VeilOnVpnService.start(this@MainActivity) // restart on new node
                            tab = "home"
                        }
                        "settings" -> SettingsScreen()
                        "privacy" -> Privacy(onDeleted = { loggedIn = false })
                        "plan" -> Plan(profile, onProfile = { profile = it })
                        "support" -> SupportScreen(vpn)
                    }
                    error?.let { Text(it, color = Tokens.danger, fontSize = 12.sp, modifier = Modifier.align(Alignment.BottomCenter)) }
                }
                if (loggedIn) BottomBar(tab) { tab = it }
            }
        }
    }

    @Composable
    private fun Wordmark() {
        // The brand mark replaces the letter V (ADR-0051).
        Row(verticalAlignment = Alignment.Bottom) {
            Image(painterResource(R.drawable.ic_launcher_foreground), null, Modifier.size(34.dp).offset(y = 6.dp))
            Text("eil", color = Tokens.textPrimary, fontSize = 22.sp, fontWeight = FontWeight.SemiBold)
            Text("On", color = Tokens.accent, fontSize = 22.sp, fontWeight = FontWeight.SemiBold)
        }
    }

    @Composable
    private fun Home(vpn: VpnState, profile: ProfileView?, onLocations: () -> Unit) {
        val node = profile?.locations?.firstOrNull { it.id == repo.settings.nodeId } ?: profile?.locations?.firstOrNull()
        Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(20.dp)) {
            Spacer(Modifier.height(24.dp))
            val on = vpn == VpnState.CONNECTED
            Box(
                Modifier.size(180.dp).clip(CircleShape)
                    .background(if (on) Tokens.primarySolid else Tokens.bgElevated)
                    .border(2.dp, if (on) Tokens.accent else Tokens.border, CircleShape)
                    .clickable { if (on || vpn == VpnState.CONNECTING) VeilOnVpnService.stop(this@MainActivity) else connect() },
                contentAlignment = Alignment.Center,
            ) { Icon(Icons.Filled.PowerSettingsNew, contentDescription = stringResource(if (on) R.string.disconnect else R.string.connect), tint = Tokens.textPrimary, modifier = Modifier.size(64.dp)) }
            Text(
                stringResource(
                    when (vpn) {
                        VpnState.CONNECTED -> R.string.state_connected
                        VpnState.CONNECTING -> R.string.state_connecting
                        VpnState.FAILED -> R.string.state_failed
                        VpnState.DISCONNECTED -> R.string.state_disconnected
                    }
                ),
                color = Tokens.textPrimary, fontSize = 18.sp, fontWeight = FontWeight.SemiBold,
            )
            if (node != null) LocationRow(node, current = false, onClick = onLocations)
            Text(stringResource(R.string.zero_log), color = Tokens.accent, fontSize = 12.sp)
        }
    }

    @Composable
    private fun LocationRow(l: Location, current: Boolean, onClick: () -> Unit) {
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(Tokens.bgSurface)
                .border(1.dp, if (current) Tokens.primarySolid else Tokens.border, RoundedCornerShape(10.dp))
                .clickable(onClick = onClick).padding(14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(flag(l.region), fontSize = 24.sp)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(l.name.substringBefore(" · "), color = Tokens.textPrimary, fontWeight = FontWeight.Medium)
                Text(l.transports.joinToString(", "), color = Tokens.textMuted, fontSize = 12.sp)
            }
            if (current) Text(stringResource(R.string.current), color = Tokens.accent, fontSize = 12.sp)
        }
    }

    @Composable
    private fun Locations(profile: ProfileView?, onPick: (String) -> Unit) {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(stringResource(R.string.locations), color = Tokens.textPrimary, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
            Text(stringResource(R.string.locations_hint), color = Tokens.textMuted, fontSize = 12.sp)
            LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                items(profile?.locations ?: emptyList()) { l -> LocationRow(l, current = l.id == repo.settings.nodeId) { onPick(l.id) } }
            }
        }
    }

    @Composable
    private fun SettingsScreen() {
        var s by remember { mutableStateOf(repo.settings) }
        fun save(n: net.veilon.app.data.Settings) { s = n; repo.settings = n }
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(stringResource(R.string.settings), color = Tokens.textPrimary, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
            Card(colors = CardDefaults.cardColors(containerColor = Tokens.bgSurface)) {
                Column(Modifier.padding(14.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(stringResource(R.string.kill_switch), color = Tokens.textPrimary)
                    Text(stringResource(R.string.kill_switch_hint), color = Tokens.textMuted, fontSize = 12.sp)
                    OutlinedButton(onClick = { startActivity(Intent(AndroidSettings.ACTION_VPN_SETTINGS)) }) { Text(stringResource(R.string.open_vpn_settings)) }
                }
            }
            ToggleRow(stringResource(R.string.auto_switch), s.autoSwitch) { save(s.copy(autoSwitch = it)) }
            ToggleRow(stringResource(R.string.allow_lan), s.allowLan) { save(s.copy(allowLan = it)) }
            ToggleRow(stringResource(R.string.bypass_ru), s.bypassRu) { save(s.copy(bypassRu = it)) }
            Text(stringResource(R.string.bypass_ru_hint), color = Tokens.textMuted, fontSize = 12.sp)
            DnsChooser(s) { save(it) }
            ToggleRow(stringResource(R.string.auto_connect), s.autoConnect) { save(s.copy(autoConnect = it)) }
            Text(stringResource(R.string.auto_connect_hint), color = Tokens.textMuted, fontSize = 12.sp)
            ToggleRow(stringResource(R.string.crash_reports), s.crashReports) {
                save(s.copy(crashReports = it))
                if (it) io { withContext(Dispatchers.IO) { repo.crashes.flush(true) } }
            }
            Text(stringResource(R.string.crash_reports_hint), color = Tokens.textMuted, fontSize = 12.sp)
            CrashList()
            Text(stringResource(R.string.split_apps), color = Tokens.textPrimary)
            Text(stringResource(R.string.split_apps_hint), color = Tokens.textMuted, fontSize = 12.sp)
            val apps = remember {
                packageManager.getInstalledApplications(0)
                    .filter { packageManager.getLaunchIntentForPackage(it.packageName) != null && it.packageName != packageName }
                    .sortedBy { it.loadLabel(packageManager).toString().lowercase() }
            }
            LazyColumn(Modifier.heightIn(max = 320.dp)) {
                items(apps) { a ->
                    val checked = a.packageName in s.bypassPackages
                    Row(Modifier.fillMaxWidth().clickable {
                        save(s.copy(bypassPackages = if (checked) s.bypassPackages - a.packageName else s.bypassPackages + a.packageName))
                    }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                        Checkbox(checked = checked, onCheckedChange = null)
                        Text(a.loadLabel(packageManager).toString(), color = Tokens.textPrimary, fontSize = 14.sp)
                    }
                }
            }
        }
    }

    /** DNS over HTTPS through the tunnel (ADR-0073); Quad9 by default. */
    @Composable
    private fun DnsChooser(s: net.veilon.app.data.Settings, onSave: (net.veilon.app.data.Settings) -> Unit) {
        val presets = remember { runCatching { repo.dnsPresets() }.getOrElse { listOf("quad9" to "Quad9") } }
        var custom by remember { mutableStateOf(s.dnsCustomUrl) }
        var editing by remember { mutableStateOf(s.dnsProvider == "custom") }
        var err by remember { mutableStateOf("") }
        Text(stringResource(R.string.dns), color = Tokens.textPrimary)
        Text(stringResource(R.string.dns_hint), color = Tokens.textMuted, fontSize = 12.sp)
        (presets + ("custom" to stringResource(R.string.dns_custom))).forEach { (id, name) ->
            val sel = if (editing) id == "custom" else id == s.dnsProvider
            Row(Modifier.fillMaxWidth().clickable {
                if (id == "custom") editing = true else { editing = false; err = ""; onSave(s.copy(dnsProvider = id, dnsCustomUrl = "")) }
            }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                RadioButton(selected = sel, onClick = null)
                Text(if (id == "quad9") "$name (${stringResource(R.string.dns_default)})" else name, color = Tokens.textPrimary, fontSize = 14.sp)
            }
        }
        if (editing) {
            OutlinedTextField(custom, { custom = it.trim().take(255) }, label = { Text("https://…/dns-query") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            Text(stringResource(R.string.dns_custom_hint), color = Tokens.textMuted, fontSize = 12.sp)
            Button(onClick = {
                err = repo.validateDns("custom", custom)
                if (err.isEmpty()) onSave(s.copy(dnsProvider = "custom", dnsCustomUrl = custom))
            }) { Text(stringResource(R.string.dns_apply)) }
            if (err.isNotEmpty()) Text(err, color = Tokens.danger, fontSize = 12.sp)
        }
    }

    /** Reports waiting on this device, with the exact text that would be sent (ADR-0077). */
    @Composable
    private fun CrashList() {
        var items by remember { mutableStateOf(repo.crashes.list()) }
        var open by remember { mutableStateOf<String?>(null) }
        var err by remember { mutableStateOf("") }
        Text(stringResource(R.string.crash_title), color = Tokens.textPrimary)
        if (items.isEmpty()) Text(stringResource(R.string.crash_none), color = Tokens.textMuted, fontSize = 12.sp)
        items.forEach { c ->
            Card(colors = CardDefaults.cardColors(containerColor = Tokens.bgSurface)) {
                Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    Text(c.title, color = Tokens.textPrimary, fontSize = 12.sp, maxLines = 2, modifier = Modifier.clickable { open = if (open == c.id) null else c.id })
                    Text("${c.day} · ${c.component}", color = Tokens.textMuted, fontSize = 11.sp)
                    if (open == c.id) {
                        Text(stringResource(R.string.crash_preview_hint), color = Tokens.textMuted, fontSize = 11.sp)
                        Text(c.text, color = Tokens.textPrimary, fontSize = 11.sp, fontFamily = androidx.compose.ui.text.font.FontFamily.Monospace,
                            modifier = Modifier.fillMaxWidth().background(Tokens.bgElevated, RoundedCornerShape(8.dp)).padding(8.dp))
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = {
                            io {
                                err = withContext(Dispatchers.IO) { runCatching { repo.crashes.send(c) }.exceptionOrNull()?.message.orEmpty() }
                                items = repo.crashes.list()
                            }
                        }) { Text(stringResource(R.string.crash_send)) }
                        TextButton(onClick = { repo.crashes.delete(c.id); items = repo.crashes.list() }) { Text(stringResource(R.string.crash_delete)) }
                    }
                }
            }
        }
        if (err.isNotEmpty()) Text(err, color = Tokens.danger, fontSize = 12.sp)
    }

    @Composable
    private fun ToggleRow(label: String, value: Boolean, onChange: (Boolean) -> Unit) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(label, color = Tokens.textPrimary, modifier = Modifier.weight(1f))
            Switch(checked = value, onCheckedChange = onChange)
        }
    }

    @Composable
    private fun Privacy(onDeleted: () -> Unit) {
        var confirm by remember { mutableStateOf(false) }
        var exported by remember { mutableStateOf<String?>(null) }
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(stringResource(R.string.privacy), color = Tokens.textPrimary, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
            OutlinedButton(onClick = { io { exported = withContext(Dispatchers.IO) { runCatching { repo.exportAccount() }.getOrElse { it.message } } } }) {
                Text(stringResource(R.string.export_data))
            }
            exported?.let { Text(it, color = Tokens.textMuted, fontSize = 11.sp, maxLines = 12) }
            if (!confirm) {
                OutlinedButton(onClick = { confirm = true }, colors = ButtonDefaults.outlinedButtonColors(contentColor = Tokens.danger)) { Text(stringResource(R.string.delete_account)) }
            } else {
                Text(stringResource(R.string.delete_confirm), color = Tokens.danger, fontSize = 13.sp)
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = { confirm = false }) { Text(stringResource(R.string.cancel)) }
                    Button(onClick = {
                        io {
                            VeilOnVpnService.stop(this@MainActivity)
                            withContext(Dispatchers.IO) { runCatching { repo.deleteAccount() } }
                            onDeleted()
                        }
                    }, colors = ButtonDefaults.buttonColors(containerColor = Tokens.danger)) { Text(stringResource(R.string.delete_account)) }
                }
            }
        }
    }

    @Composable
    private fun Plan(profile: ProfileView?, onProfile: (ProfileView?) -> Unit) {
        var offers by remember { mutableStateOf<List<Offer>?>(null) }
        var plan by remember { mutableStateOf("") }
        var phase by remember { mutableStateOf("idle") } // idle | waiting | done | expired
        var err by remember { mutableStateOf<String?>(null) }
        var order by remember { mutableStateOf<String?>(null) }
        LaunchedEffect(Unit) {
            offers = withContext(Dispatchers.IO) { runCatching { repo.offers() }.getOrElse { emptyList() } }
            offers?.firstOrNull()?.let { plan = it.planId }
        }
        // Poll the order while waiting (every 3 s, the page may stay open in the browser).
        LaunchedEffect(order) {
            val id = order ?: return@LaunchedEffect
            var fails = 0
            while (phase == "waiting") {
                kotlinx.coroutines.delay(3000)
                val st = withContext(Dispatchers.IO) { runCatching { repo.orderStatus(id) }.getOrNull() }
                when (st) {
                    "fulfilled" -> { phase = "done"; onProfile(repo.cachedProfile()) }
                    "expired", "canceled" -> phase = "expired"
                    null -> if (++fails > 5) phase = "idle"
                }
            }
        }
        fun money(p: Price) = "%.2f %s".format(p.amountMinor / 100.0, if (p.currency == "RUB") "₽" else p.currency)
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text(stringResource(R.string.payments), color = Tokens.textPrimary, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
            profile?.let { Text("${it.plan} · ${it.endsOn}", color = Tokens.textMuted, fontSize = 13.sp) }
            Text(stringResource(R.string.payments_hint), color = Tokens.textMuted, fontSize = 12.sp)
            when (phase) {
                "done" -> Text(stringResource(R.string.pay_done), color = Tokens.success)
                "expired" -> Text(stringResource(R.string.pay_expired), color = Tokens.warning)
                "waiting" -> Text(stringResource(R.string.pay_waiting), color = Tokens.accent)
            }
            if (phase != "waiting") {
                offers?.forEach { o ->
                    val sel = o.planId == plan
                    Row(
                        Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp))
                            .border(1.dp, if (sel) Tokens.accent else Tokens.border, RoundedCornerShape(8.dp))
                            .clickable { plan = o.planId }.padding(12.dp),
                    ) {
                        Column(Modifier.weight(1f)) {
                            Text(o.name, color = Tokens.textPrimary)
                            Text("${o.days} d · ≤ ${o.devices}", color = Tokens.textMuted, fontSize = 12.sp)
                        }
                        Column(horizontalAlignment = Alignment.End) { o.prices.forEach { Text(money(it), color = Tokens.textMuted, fontSize = 12.sp) } }
                    }
                }
                if (offers?.isEmpty() == true) Text(stringResource(R.string.pay_none), color = Tokens.textMuted, fontSize = 12.sp)
                offers?.firstOrNull { it.planId == plan }?.prices?.forEach { p ->
                    Button(onClick = {
                        io {
                            withContext(Dispatchers.IO) { runCatching { repo.checkout(plan, p.provider) } }
                                .onSuccess { (id, url) ->
                                    err = null
                                    phase = "waiting"
                                    order = id
                                    startActivity(Intent(Intent.ACTION_VIEW, android.net.Uri.parse(url)))
                                }
                                .onFailure { err = it.message }
                        }
                    }, modifier = Modifier.fillMaxWidth()) {
                        Text("${stringResource(if (p.provider == "heleket") R.string.pay_crypto else R.string.pay_card)} · ${money(p)}")
                    }
                }
            }
            err?.let { Text(it, color = Tokens.danger, fontSize = 12.sp) }
        }
    }

    /** Support: tickets relayed to the API; nothing is stored on the device. */
    @Composable
    private fun SupportScreen(vpn: VpnState) {
        var tickets by remember { mutableStateOf<List<SupportTicket>?>(null) }
        var thread by remember { mutableStateOf<SupportThread?>(null) }
        var creating by remember { mutableStateOf(false) }
        var err by remember { mutableStateOf<String?>(null) }
        var text by remember { mutableStateOf("") }
        var subject by remember { mutableStateOf("") }
        var diag by remember { mutableStateOf<String?>(null) }
        val lang = java.util.Locale.getDefault().language.let { if (it == "ru" || it == "sr") it else "en" }
        fun bg(block: () -> Unit) = io { withContext(Dispatchers.IO) { runCatching(block) }.onFailure { err = it.message } }
        LaunchedEffect(thread?.ticket?.id, creating) {
            if (thread == null && !creating) tickets = withContext(Dispatchers.IO) { runCatching { repo.supportTickets() }.getOrNull() }
        }
        // Poll an open conversation every 10 s for staff answers.
        LaunchedEffect(thread?.ticket?.id) {
            val id = thread?.ticket?.id ?: return@LaunchedEffect
            while (thread?.ticket?.id == id && thread?.ticket?.status != "closed") {
                kotlinx.coroutines.delay(10_000)
                withContext(Dispatchers.IO) { runCatching { repo.supportThread(id) }.getOrNull() }?.let { thread = it }
            }
        }
        Column(Modifier.fillMaxSize().verticalScrollCompat(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text(stringResource(R.string.support), color = Tokens.textPrimary, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
            val th = thread
            when {
                creating -> {
                    Text(stringResource(R.string.sup_privacy), color = Tokens.textMuted, fontSize = 12.sp)
                    OutlinedTextField(subject, { subject = it.take(200) }, label = { Text(stringResource(R.string.sup_subject)) }, singleLine = true, modifier = Modifier.fillMaxWidth())
                    OutlinedTextField(text, { text = it.take(8000) }, label = { Text(stringResource(R.string.sup_message)) }, minLines = 4, modifier = Modifier.fillMaxWidth())
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        OutlinedButton(onClick = { creating = false }) { Text(stringResource(R.string.cancel)) }
                        Button(enabled = subject.isNotBlank() && text.isNotBlank(), onClick = {
                            bg { val t = repo.supportOpen(subject.trim(), text.trim(), lang); thread = t; creating = false; subject = ""; text = "" }
                        }) { Text(stringResource(R.string.sup_send)) }
                    }
                }
                th != null -> {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        TextButton(onClick = { thread = null }) { Text("‹") }
                        Text(th.ticket.subject, color = Tokens.textPrimary, modifier = Modifier.weight(1f), maxLines = 1)
                        if (th.ticket.status != "waiting_human" && th.ticket.status != "closed") {
                            OutlinedButton(onClick = { bg { thread = repo.supportHuman(th.ticket.id) } }) { Text(stringResource(R.string.sup_human)) }
                        }
                    }
                    th.messages.forEach { m ->
                        if (m.author == "system") {
                            Text(m.body, color = Tokens.textMuted, fontSize = 11.sp, modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp))
                        } else {
                            val mine = m.author == "user"
                            Column(
                                Modifier.fillMaxWidth(0.88f).align(if (mine) Alignment.End else Alignment.Start)
                                    .clip(RoundedCornerShape(10.dp)).background(if (mine) Tokens.bgElevated else Tokens.bgSurface).padding(10.dp),
                            ) {
                                Text(stringResource(when (m.author) { "ai" -> R.string.sup_ai; "staff" -> R.string.sup_staff; else -> R.string.sup_you }),
                                    color = Tokens.accent, fontSize = 10.sp, fontWeight = FontWeight.SemiBold)
                                Text(m.body, color = Tokens.textPrimary, fontSize = 14.sp)
                            }
                        }
                    }
                    if (th.ticket.status != "closed") {
                        OutlinedTextField(text, { text = it.take(8000) }, label = { Text(stringResource(R.string.sup_reply)) }, minLines = 2, modifier = Modifier.fillMaxWidth())
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Button(enabled = text.isNotBlank(), onClick = { bg { thread = repo.supportPost(th.ticket.id, text.trim()); text = "" } }) { Text(stringResource(R.string.sup_send)) }
                            OutlinedButton(onClick = { diag = repo.diagnostics(vpn.name.lowercase()) }) { Text(stringResource(R.string.sup_diag)) }
                            TextButton(onClick = { bg { repo.supportClose(th.ticket.id); thread = repo.supportThread(th.ticket.id) } }) { Text(stringResource(R.string.sup_close)) }
                        }
                    }
                    diag?.let { report ->
                        AlertDialog(
                            onDismissRequest = { diag = null },
                            title = { Text(stringResource(R.string.sup_diag)) },
                            text = { Column { Text(stringResource(R.string.sup_diag_hint), fontSize = 12.sp); Text(report, fontSize = 10.sp, color = Tokens.textMuted) } },
                            confirmButton = { Button(onClick = { bg { repo.attachDiagnostics(th.ticket.id, report); diag = null } }) { Text(stringResource(R.string.sup_send)) } },
                            dismissButton = { OutlinedButton(onClick = { diag = null }) { Text(stringResource(R.string.cancel)) } },
                        )
                    }
                }
                else -> {
                    Text(stringResource(R.string.sup_intro), color = Tokens.textMuted, fontSize = 12.sp)
                    Button(onClick = { creating = true }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.sup_new)) }
                    tickets?.forEach { t ->
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).border(1.dp, Tokens.border, RoundedCornerShape(8.dp))
                                .clickable { bg { thread = repo.supportThread(t.id) } }.padding(12.dp),
                        ) {
                            Text(t.subject, color = Tokens.textPrimary, modifier = Modifier.weight(1f), maxLines = 1)
                            Text(t.updated, color = Tokens.textMuted, fontSize = 12.sp)
                        }
                    }
                }
            }
            err?.let { Text(it, color = Tokens.danger, fontSize = 12.sp) }
        }
    }

    @Composable
    private fun Modifier.verticalScrollCompat(): Modifier = this.verticalScroll(rememberScrollState())

    @Composable
    private fun SignIn(onDone: () -> Unit, onError: (String) -> Unit) {
        var number by remember { mutableStateOf("") }
        var created by remember { mutableStateOf<String?>(null) }
        Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
            val shown = created
            if (shown != null) {
                Text(stringResource(R.string.save_number), color = Tokens.warning)
                Text(shown, color = Tokens.textPrimary, fontSize = 22.sp, fontWeight = FontWeight.SemiBold)
                Button(onClick = onDone) { Text(stringResource(R.string.ok)) }
                return@Column
            }
            Button(onClick = {
                io { withContext(Dispatchers.IO) { runCatching { repo.register() } }.onSuccess { created = it }.onFailure { onError(it.message ?: "error") } }
            }, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.create_account)) }
            Text(stringResource(R.string.create_account_hint), color = Tokens.textMuted, fontSize = 12.sp)
            OutlinedTextField(number, { number = it.filter(Char::isDigit).take(16) }, label = { Text(stringResource(R.string.account_number)) }, singleLine = true, modifier = Modifier.fillMaxWidth())
            OutlinedButton(onClick = {
                io { withContext(Dispatchers.IO) { runCatching { repo.login(number) } }.onSuccess { onDone() }.onFailure { onError(it.message ?: "error") } }
            }, enabled = number.length == 16, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.sign_in)) }
        }
    }

    @Composable
    private fun BottomBar(tab: String, onTab: (String) -> Unit) {
        NavigationBar(containerColor = Tokens.bgSurface) {
            listOf(
                Triple("home", Icons.Filled.Home, R.string.connect),
                Triple("locations", Icons.Filled.Public, R.string.locations),
                Triple("plan", Icons.Filled.ShoppingCart, R.string.payments),
                Triple("support", Icons.Filled.Email, R.string.support),
                Triple("privacy", Icons.Filled.Lock, R.string.privacy),
                Triple("settings", Icons.Filled.Settings, R.string.settings),
            ).forEach { (id, icon, label) ->
                NavigationBarItem(selected = tab == id, onClick = { onTab(id) }, icon = { Icon(icon, null) }, label = { Text(stringResource(label), fontSize = 10.sp) })
            }
        }
    }

    private fun flag(cc: String): String =
        if (cc.length == 2 && cc.all { it in 'A'..'Z' }) cc.map { String(Character.toChars(0x1F1A5 + it.code)) }.joinToString("") else "🌐"
}
