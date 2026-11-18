package net.veilon.app

import android.app.Application
import android.app.NotificationChannel
import android.app.NotificationManager
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import net.veilon.app.data.Repository
import net.veilon.app.vpn.LibboxEngine

class VeilOnApp : Application() {
    lateinit var repo: Repository
        private set

    override fun onCreate() {
        super.onCreate()
        repo = Repository(this)
        // Crash capture first: sanitized reports, sent only with consent.
        repo.crashes.install()
        CoroutineScope(SupervisorJob() + Dispatchers.IO).launch {
            repo.crashes.collect()
            if (repo.loggedIn) repo.crashes.flush(repo.settings.crashReports)
        }
        LibboxEngine.setup(this)
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL_VPN, getString(R.string.vpn_notification), NotificationManager.IMPORTANCE_LOW)
        )
    }

    companion object {
        const val CHANNEL_VPN = "vpn"
    }
}
