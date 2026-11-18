package net.veilon.app.vpn

import android.net.VpnService
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/** Quick Settings tile: one tap connect/disconnect (if VPN permission was granted). */
class QuickTile : TileService() {
    override fun onStartListening() {
        qsTile?.apply {
            state = if (VeilOnVpnService.stateFlow.value == VpnState.CONNECTED) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
            updateTile()
        }
    }

    override fun onClick() {
        if (VeilOnVpnService.stateFlow.value == VpnState.CONNECTED) {
            VeilOnVpnService.stop(this)
        } else if (VpnService.prepare(this) == null) {
            VeilOnVpnService.start(this)
        }
        onStartListening()
    }
}
