package net.veilon.app.vpn

import android.net.ConnectivityManager
import android.net.DnsResolver
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.VpnService
import android.os.Build
import android.os.CancellationSignal
import android.os.ParcelFileDescriptor
import android.system.OsConstants
import android.util.Base64
import net.veilon.core.libbox.ConnectionOwner
import net.veilon.core.libbox.ExchangeContext
import net.veilon.core.libbox.Func
import net.veilon.core.libbox.InterfaceUpdateListener
import net.veilon.core.libbox.Libbox
import net.veilon.core.libbox.LocalDNSTransport
import net.veilon.core.libbox.NetworkInterfaceIterator
import net.veilon.core.libbox.Notification
import net.veilon.core.libbox.PlatformInterface
import net.veilon.core.libbox.RoutePrefixIterator
import net.veilon.core.libbox.StringIterator
import net.veilon.core.libbox.TunOptions
import net.veilon.core.libbox.WIFIState
import java.net.Inet6Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.security.KeyStore
import java.util.concurrent.Executors
import net.veilon.core.libbox.NetworkInterface as BoxInterface

/**
 * libbox PlatformInterface for Android (sing-box 1.13.21 API).
 *
 * Privacy: the app keeps no logs (ADR-0011), reads no Wi-Fi names
 * ([readWIFIState] returns null) and sends no notifications from the core.
 */
internal class LibboxPlatform(
    private val vpn: VpnService,
    private val bypass: Set<String>,
) : PlatformInterface {
    private val cm = vpn.getSystemService(ConnectivityManager::class.java)
    private val monitor = DefaultNetworkMonitor(cm)
    private val dns = LocalResolver(monitor)

    @Volatile private var tun: ParcelFileDescriptor? = null

    fun startMonitor() = monitor.start()

    fun stopMonitor() = monitor.stop()

    fun closeTun() {
        LibboxEngine.close(tun)
        tun = null
    }

    override fun openTun(options: TunOptions): Int {
        if (VpnService.prepare(vpn) != null) error("android: VPN permission missing")
        val b = vpn.Builder().setSession("VeilOn").setMtu(options.getMTU())
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) b.setMetered(false)
        options.getInet4Address().forEach { b.addAddress(it.address(), it.prefix()) }
        options.getInet6Address().forEach { b.addAddress(it.address(), it.prefix()) }
        if (options.getAutoRoute()) {
            b.addDnsServer(options.getDNSServerAddress().value)
            // Everything is routed into the tunnel, IPv6 included: it is
            // tunnelled or rejected by the core, never leaked (ADR-0008).
            b.addRoute("0.0.0.0", 0)
            b.addRoute("::", 0)
        }
        // The app itself bypasses the tunnel (the core dials the node);
        // user-chosen apps too (split tunnelling, ADR-0054).
        b.addDisallowedApplication(vpn.packageName)
        bypass.forEach { runCatching { b.addDisallowedApplication(it) } }
        val pfd = b.establish() ?: error("android: VPN permission revoked")
        tun = pfd
        return pfd.fd
    }

    // Sockets the core opens to reach nodes must not loop into the tunnel.
    override fun usePlatformAutoDetectInterfaceControl(): Boolean = true

    override fun autoDetectInterfaceControl(fd: Int) {
        if (!vpn.protect(fd)) error("android: protect($fd) failed")
    }

    override fun useProcFS(): Boolean = Build.VERSION.SDK_INT < Build.VERSION_CODES.Q

    override fun findConnectionOwner(
        ipProtocol: Int,
        sourceAddress: String,
        sourcePort: Int,
        destinationAddress: String,
        destinationPort: Int,
    ): ConnectionOwner {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) error("android: owner lookup needs API 29")
        val uid = cm.getConnectionOwnerUid(
            ipProtocol,
            InetSocketAddress(sourceAddress, sourcePort),
            InetSocketAddress(destinationAddress, destinationPort),
        )
        if (uid == android.os.Process.INVALID_UID) error("android: connection owner not found")
        val pkgs = vpn.packageManager.getPackagesForUid(uid)?.toList().orEmpty()
        return ConnectionOwner().apply {
            userId = uid
            userName = pkgs.firstOrNull().orEmpty()
            setAndroidPackageNames(Strings(pkgs))
        }
    }

    override fun startDefaultInterfaceMonitor(listener: InterfaceUpdateListener) = monitor.setListener(listener)

    override fun closeDefaultInterfaceMonitor(listener: InterfaceUpdateListener) = monitor.setListener(null)

    override fun getInterfaces(): NetworkInterfaceIterator {
        val out = mutableListOf<BoxInterface>()
        for (network in cm.allNetworks) {
            val lp = cm.getLinkProperties(network) ?: continue
            val caps = cm.getNetworkCapabilities(network) ?: continue
            if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) continue
            val name = lp.interfaceName ?: continue
            val ni = runCatching { java.net.NetworkInterface.getByName(name) }.getOrNull() ?: continue
            out += BoxInterface().apply {
                setName(name)
                index = ni.index
                runCatching { setMTU(ni.mtu) }
                addresses = Strings(ni.interfaceAddresses.map { "${host(it.address)}/${it.networkPrefixLength}" })
                setDNSServer(Strings(lp.dnsServers.mapNotNull { it.hostAddress }))
                type = when {
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> Libbox.InterfaceTypeWIFI
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> Libbox.InterfaceTypeCellular
                    caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> Libbox.InterfaceTypeEthernet
                    else -> Libbox.InterfaceTypeOther
                }
                var f = 0
                if (caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)) f = OsConstants.IFF_UP or OsConstants.IFF_RUNNING
                if (ni.isLoopback) f = f or OsConstants.IFF_LOOPBACK
                if (ni.isPointToPoint) f = f or OsConstants.IFF_POINTOPOINT
                if (ni.supportsMulticast()) f = f or OsConstants.IFF_MULTICAST
                flags = f
                metered = !caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED)
            }
        }
        return Interfaces(out.iterator())
    }

    override fun underNetworkExtension(): Boolean = false

    override fun includeAllNetworks(): Boolean = false

    // Wi-Fi names are personal data and no rule uses them.
    override fun readWIFIState(): WIFIState? = null

    override fun localDNSTransport(): LocalDNSTransport = dns

    override fun systemCertificates(): StringIterator {
        val certs = mutableListOf<String>()
        runCatching {
            val ks = KeyStore.getInstance("AndroidCAStore").apply { load(null, null) }
            for (alias in ks.aliases()) {
                val der = ks.getCertificate(alias)?.encoded ?: continue
                certs += "-----BEGIN CERTIFICATE-----\n" +
                    Base64.encodeToString(der, Base64.NO_WRAP) + "\n-----END CERTIFICATE-----"
            }
        }
        return Strings(certs)
    }

    override fun clearDNSCache() {}

    override fun sendNotification(notification: Notification) {}

    private fun host(a: InetAddress): String =
        if (a is Inet6Address) InetAddress.getByAddress(a.address).hostAddress.orEmpty() else a.hostAddress.orEmpty()
}

private inline fun RoutePrefixIterator.forEach(f: (net.veilon.core.libbox.RoutePrefix) -> Unit) {
    while (hasNext()) f(next())
}

internal class Strings(private val items: List<String>) : StringIterator {
    private var i = 0
    override fun len(): Int = items.size
    override fun hasNext(): Boolean = i < items.size
    override fun next(): String = items[i++]
}

private class Interfaces(private val it: Iterator<BoxInterface>) : NetworkInterfaceIterator {
    override fun hasNext(): Boolean = it.hasNext()
    override fun next(): BoxInterface = it.next()
}

/**
 * Tracks the best non-VPN network and reports it to the core, which binds
 * its outgoing sockets to that interface.
 */
internal class DefaultNetworkMonitor(private val cm: ConnectivityManager) {
    @Volatile var network: Network? = null
        private set
    @Volatile private var listener: InterfaceUpdateListener? = null
    private var callback: ConnectivityManager.NetworkCallback? = null

    @Synchronized fun start() {
        if (callback != null) return
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(n: Network) = update(n)
            override fun onLinkPropertiesChanged(n: Network, lp: LinkProperties) = update(n)
            override fun onCapabilitiesChanged(n: Network, caps: NetworkCapabilities) = update(n)
            override fun onLost(n: Network) {
                if (network == n) update(null)
            }
        }
        val req = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
            .build()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            cm.registerBestMatchingNetworkCallback(req, cb, android.os.Handler(android.os.Looper.getMainLooper()))
        } else {
            cm.requestNetwork(req, cb)
        }
        callback = cb
    }

    @Synchronized fun stop() {
        callback?.let { runCatching { cm.unregisterNetworkCallback(it) } }
        callback = null
        network = null
    }

    fun setListener(l: InterfaceUpdateListener?) {
        listener = l
        report()
    }

    private fun update(n: Network?) {
        network = n
        report()
    }

    private fun report() {
        val l = listener ?: return
        val n = network
        val name = n?.let { cm.getLinkProperties(it)?.interfaceName }
        val index = name?.let { runCatching { java.net.NetworkInterface.getByName(it)?.index }.getOrNull() }
        if (name == null || index == null) {
            l.updateDefaultInterface("", -1, false, false)
            return
        }
        val caps = cm.getNetworkCapabilities(n)
        val expensive = caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED) == false
        l.updateDefaultInterface(name, index, expensive, false)
    }
}

/**
 * The core's "local" DNS server (bootstrap of node names and direct
 * domains): queries go to the underlying network's resolver, never through
 * the tunnel, and are never logged.
 */
internal class LocalResolver(private val monitor: DefaultNetworkMonitor) : LocalDNSTransport {
    private val executor = Executors.newCachedThreadPool()

    // API 29+: raw DNS messages through android.net.DnsResolver.
    override fun raw(): Boolean = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q

    override fun exchange(ctx: ExchangeContext, message: ByteArray) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) error("android: raw DNS needs API 29")
        val cancel = CancellationSignal()
        ctx.onCancel(object : Func { override fun invoke() = cancel.cancel() })
        val done = java.util.concurrent.CountDownLatch(1)
        DnsResolver.getInstance().rawQuery(
            monitor.network, message, DnsResolver.FLAG_NO_RETRY, executor, cancel,
            object : DnsResolver.Callback<ByteArray> {
                override fun onAnswer(answer: ByteArray, rcode: Int) {
                    ctx.rawSuccess(answer)
                    done.countDown()
                }
                override fun onError(e: DnsResolver.DnsException) {
                    ctx.errnoCode(e.code)
                    done.countDown()
                }
            },
        )
        done.await()
    }

    // API 26-28: address lookup on the underlying network.
    override fun lookup(ctx: ExchangeContext, network: String, domain: String) {
        val n = monitor.network
        val all = try {
            val name = domain.removeSuffix(".")
            (n?.getAllByName(name) ?: InetAddress.getAllByName(name)).toList()
        } catch (e: java.net.UnknownHostException) {
            ctx.errorCode(3) // NXDOMAIN
            return
        }
        val want = all.filter {
            when (network) {
                "ip4" -> it !is Inet6Address
                "ip6" -> it is Inet6Address
                else -> true
            }
        }
        ctx.success(want.mapNotNull { it.hostAddress }.joinToString("\n"))
    }
}
