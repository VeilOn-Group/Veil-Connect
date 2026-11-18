package net.veilon.app.vpn

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.VpnService
import net.veilon.app.VeilOnApp

/**
 * Connects after boot or an app update when the user turned "connect at
 * start" on (ADR-0076). Only if the VPN permission was already granted
 * (VpnService.prepare returns null): a receiver cannot show the consent
 * dialog. Always-on VPN in system settings is the stronger option: the OS
 * starts the service itself and can block traffic without VPN.
 */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED && intent.action != Intent.ACTION_MY_PACKAGE_REPLACED) return
        val repo = (context.applicationContext as VeilOnApp).repo
        if (!repo.settings.autoConnect || !repo.loggedIn) return
        if (VpnService.prepare(context) != null) return
        if (VeilOnVpnService.stateFlow.value == VpnState.CONNECTED) return
        VeilOnVpnService.start(context)
    }
}
