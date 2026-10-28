/*
 * Android client, shared by the phone and TV builds: the Android VpnService
 * that runs the whole VPN session in-process. It drives the Go engine, owns
 * the TUN interface, keeps engine sockets off the VPN, follows network
 * switches, and publishes state into Live for the screens and notification.
 * Routes come from RoutePlan (which reads SplitStore and Prefs); the profile
 * YAML comes from YamlBuilder.
 */
package app.loft.android;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ServiceInfo;
import android.net.ConnectivityManager;
import android.net.IpPrefix;
import android.net.Network;
import android.net.NetworkCapabilities;
import android.net.NetworkRequest;
import android.net.VpnService;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.os.ParcelFileDescriptor;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.net.InetAddress;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

import app.loft.engine.mobile.Engine;
import app.loft.engine.mobile.PacketSink;
import app.loft.engine.mobile.Snapshot;
import app.loft.engine.mobile.SocketProtector;

/**
 * Overlay as a first-class Android network: VpnService TUN, protect on every
 * front socket, NetworkCallback → NotifyPathChange, dual-stack from Snapshot.
 *
 * <p>This service is the whole VPN session; there is no separate helper
 * process. It runs the Go engine (clients/mobile, Java package
 * app.loft.engine) in-process, creates the TUN interface (the virtual
 * network card apps send traffic into) and moves packets both ways: TUN
 * reads go to Engine.injectPacket, and the engine hands packets back through
 * onPacket. A "front" is one of the HTTPS transports the protocol rides on
 * (GET, WebSocket, HTTP/2, QUIC, ...). The engine opens its own sockets to
 * the node's fronts; protect() keeps those sockets off the VPN so the
 * tunnel's traffic does not loop back into itself. Dual-stack means IPv4
 * and IPv6 overlay addresses, both taken from the engine's Snapshot.
 *
 * <p>Threads, in short: Android calls the lifecycle methods (onStartCommand,
 * onRevoke, onDestroy) on the main thread. Dialing runs on the {@code go}
 * pool because Engine.start blocks. The once-a-second poll (tickOnce) runs
 * on the main thread through {@code main}. One "loft-tun" thread reads the
 * TUN; the engine calls onPacket and protect from its own threads. Network
 * callbacks arrive on a ConnectivityManager thread.
 */
public final class LoftVpnService extends VpnService implements PacketSink, SocketProtector {
    /** Intent action that ends the session (sent by requestStop and the notification button). */
    static final String ACTION_STOP = "app.loft.android.STOP";
    /** Intent action that starts a session (sent by requestStart). Any non-stop intent starts one. */
    static final String ACTION_START = "app.loft.android.START";
    /** Notification channel id; the channel is low-importance so it never makes a sound. */
    private static final String CH = "loft.vpn";
    /** Id of the one ongoing notification; reusing it means each update replaces the old one. */
    private static final int NOTE = 1;

    /** Worker threads for blocking work: dialing, stopping the engine, rebuilding the TUN. */
    private final ExecutorService go = Executors.newCachedThreadPool();
    /** Main-thread handler for the once-a-second poll and reconnect delays. */
    private final Handler main = new Handler(Looper.getMainLooper());
    /** Guards the TUN descriptor and its output stream (tun, out, plan, lastAddr). */
    private final Object io = new Object();
    /** Serialises TUN rebuilds so two applyTun calls never establish() at once. */
    private final Object tunLock = new Object();
    /** Set once the user or system asked to stop; blocks retries and new sessions. */
    private final AtomicBoolean stopping = new AtomicBoolean(false);

    /**
     * The running Go engine, or null between sessions. Read from several
     * threads; each reader copies it into a local first so a concurrent
     * stopEngine (which sets it to null) cannot cause a NullPointerException.
     */
    private Engine engine;
    /** The TUN descriptor returned by Builder.establish(); null while there is none. Guarded by io. */
    private ParcelFileDescriptor tun;
    /** Write side of the TUN: onPacket writes packets for apps here. Guarded by io. */
    private FileOutputStream out;
    /** Route plan the current TUN was built from; the reader uses it to drop blocked packets. */
    private RoutePlan plan;
    /** Callback registered by watchPath to follow the physical network; null when not watching. */
    private ConnectivityManager.NetworkCallback pathCb;
    /** The physical network (Wi-Fi, LTE, Ethernet) the VPN currently rides on. */
    private Network underlying;
    /** False until the first onAvailable, which is the start network and not a switch. */
    private boolean seenPath;
    /**
     * Key of everything the current TUN was built from (addresses, DNS, MTU,
     * routes, app filter). applyTun skips the rebuild when it is unchanged.
     */
    private String lastAddr = "";
    /** Thread that reads packets from the TUN (see pumpReads). */
    private Thread reader;
    /** Reconnect attempts since the last successful start. */
    private int retries;
    /** The poll as one Runnable object, so tick() can cancel a pending run before posting the next. */
    private final Runnable ticker = this::tickOnce;

    /**
     * True while a session is running, so screens (home, TV, quick-settings
     * tile) can show the right button state even before Live.state turns
     * "connecting". Static because screens have no reference to the service.
     */
    static boolean running;

    /**
     * Handles start and stop intents. Start goes foreground at once (Android
     * kills a service started with startForegroundService otherwise) and then
     * dials on a worker thread, because Engine.start blocks on the network.
     * On API 34+ a VPN must declare the "systemExempted" foreground type.
     * START_STICKY asks Android to restart the service if it kills the
     * process for memory. Note that the stop path runs stopSession, and so
     * Engine.stop, right here on the main thread.
     */
    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            stopping.set(true);
            stopSession();
            stopForeground(true);
            stopSelf();
            return START_NOT_STICKY;
        }
        stopping.set(false);
        Notification n = notice(getString(R.string.connecting));
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTE, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED);
        } else {
            startForeground(NOTE, n);
        }
        go.execute(this::startSession);
        return START_STICKY;
    }

    /**
     * The user turned the VPN off in system settings or another VPN took over.
     * Called on the main thread; the session is torn down at once.
     */
    @Override
    public void onRevoke() {
        stopping.set(true);
        stopSession();
        super.onRevoke();
    }

    /**
     * Tears everything down. A fatal error (bad key, revoked, ...) is kept in
     * Live after the reset so the main screen can still explain why the
     * connection stopped. shutdownNow interrupts any dial or rebuild still
     * running on the worker pool.
     */
    @Override
    public void onDestroy() {
        String err = Live.err;
        boolean keep = "error".equals(Live.state) || fatalErr(err);
        stopping.set(true);
        unwatchPath();
        stopSession();
        go.shutdownNow();
        running = false;
        Live.reset();
        if (keep && err != null && !err.isEmpty()) {
            Live.state = "error";
            Live.err = err;
        }
        Bus.post();
        super.onDestroy();
    }

    /**
     * Connects the active profile: builds the engine, hands it the socket
     * protector, the packet sink and the instance file, starts it with the
     * profile's YAML, and then builds the TUN from the first Snapshot (the
     * engine's current state: addresses, routes, DNS, counters).
     * The instance file keeps the instance_id, a persistent id of this install;
     * the node uses it to recognise the same client after a reconnect.
     * Runs on a {@code go} worker thread (never the main thread) because
     * Engine.start blocks until the handshake is done.
     */
    private void startSession() {
        if (stopping.get()) {
            return;
        }
        Profile p = LoftApp.of(this).profiles().active();
        // 1. Pick the active profile and its engine YAML; nothing to do without them.
        if (p == null) {
            Journal.add("no connection");
            stopSelf();
            return;
        }
        String yaml = p.sessionYaml();
        if (yaml == null || yaml.trim().isEmpty()) {
            Journal.add("empty yaml");
            stopSelf();
            return;
        }
        // 2. Show "connecting" and log which front and host are dialed.
        Live.name = p.name;
        Live.kind = p.kind;
        Live.state = "connecting";
        Live.err = "";
        running = true;
        Bus.post();
        String path = p.path == null ? "" : p.path.trim();
        if (p.isLoft() && "get".equalsIgnoreCase(p.kind) && path.isEmpty()) {
            path = "(default)";
        }
        Journal.add("front " + p.side() + " " + p.host() + (path.isEmpty() ? "" : " " + path));
        // 3. Follow network switches and route settings for the whole session.
        watchPath();
        watchRoutePrefs();
        // 4. Start the engine (blocks until the handshake ends), then build
        //    the TUN, start polling and fetch due route lists.
        try {
            // A fresh Engine per attempt: a failed engine is never reused.
            Engine eng = new Engine();
            eng.setProtector(this);
            eng.setSink(this);
            eng.setInstanceFile(new File(getFilesDir(), "instance").getAbsolutePath());
            eng.start(yaml);
            engine = eng;
            Snapshot snap = eng.snapshot();
            if (!applyTun(snap)) {
                eng.stop();
                fail("no overlay address");
                return;
            }
            tick();
            retries = 0;
            Live.since = System.currentTimeMillis();
            Journal.add("up " + Live.ip + (Live.ip6.isEmpty() ? "" : " " + Live.ip6));
            go.execute(this::pullRoutes);
        } catch (Exception e) {
            fail(e.getMessage());
        }
    }

    /**
     * Records an error and, when auto-reconnect is on and the error is not
     * fatal, retries with exponential backoff (400 ms doubling up to 8 s, at most
     * 8 tries). Otherwise stops the service. The delay is posted on the main
     * handler only as a timer; the retry itself runs on the worker pool.
     */
    private void fail(String msg) {
        Live.state = "error";
        Live.err = msg == null ? "" : msg;
        Journal.add("error " + Live.err);
        Bus.post();
        updateNote();
        if (!stopping.get() && Prefs.reconnect(this) && retries < 8 && !fatalErr(Live.err)) {
            retries++;
            main.postDelayed(() -> {
                if (!stopping.get()) {
                    go.execute(this::startSession);
                }
            }, Math.min(8000, 200 * (1 << retries)));
            return;
        }
        stopSelf();
    }

    /**
     * Creates or rebuilds the TUN from a Snapshot. Returns false when the node
     * gave no overlay address (the address inside the members' virtual network).
     * The TUN is rebuilt when the address, DNS, front IPs, MTU or the route
     * plan change (split lists, default route, LAN bypass). Routes come from
     * RoutePlan; front IPs and split bypass prefixes are excluded so they stay
     * on the real network. The old reader thread is stopped and a new one
     * started on the new descriptor.
     *
     * <p>Called from three places: startSession (worker thread), tickOnce
     * (main thread, every second) and rebuildRoutes (worker thread). The
     * route plan and key are computed on every call, even when nothing
     * changed; only the establish() step is skipped then.
     */
    private boolean applyTun(Snapshot snap) throws Exception {
        if (snap == null) {
            return false;
        }
        String ip = nz(snap.getIP());
        String ip6 = nz(snap.getIP6());
        if (ip.isEmpty() && ip6.isEmpty()) {
            return false;
        }
        // 1. Build the route plan and a key of everything the TUN depends on.
        RoutePlan rp = RoutePlan.of(this, snap);
        String key = ip + "|" + ip6 + "|" + snap.getDNS() + "|" + snap.getFrontIPs()
                + "|" + snap.getMTU() + "|" + rp.key()
                + "|" + Prefs.appMode(this) + Prefs.appSet(this, Prefs.appMode(this)).hashCode()
                + "|" + Prefs.dnsMode(this) + "|" + Prefs.dnsAddr(this);
        synchronized (tunLock) {
            // 2. Nothing changed: keep the current TUN, just refresh the UI.
            if (key.equals(lastAddr) && tun != null) {
                paint(snap);
                return true;
            }
            // 3. Describe the new interface: session name, MTU, overlay addresses, DNS.
            // setBlocking(true): the reader thread sleeps inside read() until a
            // packet arrives instead of spinning.
            Builder b = new Builder()
                    .setSession(Live.name.isEmpty() ? "loft" : Live.name)
                    .setMtu(snap.getMTU() > 0 ? (int) snap.getMTU() : 1280)
                    .setBlocking(true);
            // API 29+: by default Android treats a VPN as metered; say it is not,
            // so apps do not hold back downloads on Wi-Fi.
            if (Build.VERSION.SDK_INT >= 29) {
                b.setMetered(false);
            }
            if (rp.overlay4 != null) {
                b.addAddress(rp.overlay4.address(), rp.overlay4.bits);
                // allowFamily: keep Android from blocking this address family when
                // no route of that family ends up in the plan.
                if (Build.VERSION.SDK_INT >= 33) {
                    b.allowFamily(android.system.OsConstants.AF_INET);
                }
            }
            if (rp.overlay6 != null) {
                b.addAddress(rp.overlay6.address(), rp.overlay6.bits);
                if (Build.VERSION.SDK_INT >= 33) {
                    b.allowFamily(android.system.OsConstants.AF_INET6);
                }
            }
            addDns(b, snap);
            // 4. Add tunnel routes, then (API 33+) carve out excluded prefixes.
            //    Bad prefixes are logged (first 8) and skipped, not fatal.
            int fail = 0;
            for (Cidr c : rp.include) {
                try {
                    Cidr n = c.canonical();
                    b.addRoute(n.address(), n.bits);
                } catch (Exception e) {
                    fail++;
                    if (fail <= 8) {
                        Journal.add("addRoute " + c.display() + " " + e.getMessage());
                    }
                }
            }
            // excludeRoute only exists on API 33+. On older versions RoutePlan
            // has already cut the excluded prefixes out of the include list.
            if (Build.VERSION.SDK_INT >= 33) {
                for (Cidr c : rp.exclude) {
                    try {
                        Cidr n = c.canonical();
                        b.excludeRoute(new IpPrefix(InetAddress.getByAddress(n.addr), n.bits));
                    } catch (Exception e) {
                        fail++;
                        if (fail <= 8) {
                            Journal.add("excludeRoute " + c.display() + " " + e.getMessage());
                        }
                    }
                }
            }
            if (fail > 8) {
                Journal.add("routes " + fail + " failed");
            }
            // 5. Per-app filter and the physical network, then create the TUN.
            addApps(b);
            if (underlying != null && Build.VERSION.SDK_INT >= 22) {
                b.setUnderlyingNetworks(new Network[]{underlying});
            }
            // establish() is a call into the system VPN service; it can take a
            // while. It returns null when VPN permission was revoked.
            ParcelFileDescriptor pfd = b.establish();
            if (pfd == null) {
                return false;
            }
            // 6. Stop the old reader and swap in the new descriptor.
            Thread old = reader;
            reader = null;
            if (old != null) {
                old.interrupt();
            }
            synchronized (io) {
                closeTunLocked();
                tun = pfd;
                out = new FileOutputStream(pfd.getFileDescriptor());
                plan = rp;
                lastAddr = key;
            }
        }
        // 7. Start reading from the new TUN and show the new state.
        pumpReads();
        paint(snap);
        return true;
    }

    /**
     * Copies a Snapshot into Live for the UI and the notification. Once the
     * session is on, it also saves what the node taught during the handshake:
     * the mask public key (mask is a mode where the node shows a certificate
     * shaped like its cover website's; the next start passes the key to the
     * engine as mask_pub) and the list of fronts the node offers, so the next
     * start can use them.
     *
     * <p>Runs on whichever thread called it: the main thread from tickOnce
     * (every second), a worker thread from startSession / rebuildRoutes. It
     * ends with a notification update and a Bus event.
     */
    private void paint(Snapshot snap) {
        // 1. Copy state, addresses and counters. Empty addresses keep the old value.
        Live.state = nz(snap.getState()).isEmpty() ? "on" : snap.getState();
        Live.err = nz(snap.getErr());
        String ip = nz(snap.getIP());
        if (!ip.isEmpty()) {
            Live.ip = ip;
        }
        String ip6 = nz(snap.getIP6());
        if (!ip6.isEmpty()) {
            Live.ip6 = ip6;
        }
        String gw = nz(snap.getGateway());
        if (!gw.isEmpty()) {
            Live.gateway = gw;
        }
        Live.kind = nz(snap.getKind());
        Live.dns = nz(snap.getDNS());
        Live.instance = nz(snap.getInstanceID());
        Live.mtu = snap.getMTU() > 0 ? (int) snap.getMTU() : 1280;
        Live.rates(snap.getBytesUp(), snap.getBytesDown());
        // 2. When up: save what the node taught (mask key, fronts, and the
        //    operator's profile JSON, which ProfileStore.applyOperator takes in).
        if ("on".equals(Live.state)) {
            Live.holding = false;
            Profiles store = LoftApp.of(this).profiles();
            String id = store.activeId();
            String pbk = nz(snap.getMaskPub());
            if (!pbk.isEmpty()) {
                store.applyLearnedMask(id, pbk);
            }
            String fronts = nz(snap.getFronts());
            if (!fronts.isEmpty()) {
                store.applyLearnedFronts(id, fronts);
            }
            String pj = nz(snap.getProfileJSON());
            if (!pj.isEmpty()) {
                ProfileStore.applyOperator(this, pj, Live.name);
            }
        }
        updateNote();
        Bus.post();
    }

    /**
     * Starts the thread that reads packets apps write into the TUN and passes
     * them to the engine. Packets to "drop" destinations are discarded here.
     * The thread is a daemon so it never keeps the process alive. It ends
     * when the descriptor is closed (read throws) or the service stops;
     * interrupt() alone does not wake a blocked read.
     */
    private void pumpReads() {
        final ParcelFileDescriptor pfd;
        synchronized (io) {
            pfd = tun;
        }
        if (pfd == null) {
            return;
        }
        reader = new Thread(() -> {
            // One reusable read buffer; 2048 is larger than any MTU the tunnel uses.
            byte[] buf = new byte[2048];
            try (FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
                int n;
                while (!stopping.get() && (n = in.read(buf)) > 0) {
                    Engine eng = engine;
                    RoutePlan rp = plan;
                    if (eng == null) {
                        continue;
                    }
                    // Copy out of the shared buffer so the engine gets an array of
                    // exactly n bytes; buf is reused for the next read.
                    byte[] pkt = new byte[n];
                    System.arraycopy(buf, 0, pkt, 0, n);
                    if (rp != null && rp.destDropped(pkt)) {
                        continue;
                    }
                    try {
                        eng.injectPacket(pkt);
                    } catch (Exception ignored) {
                    }
                }
            } catch (Exception ignored) {
            }
        }, "loft-tun");
        reader.setDaemon(true);
        reader.start();
    }

    /**
     * Schedules the next once-a-second poll of the engine state. Any poll
     * already pending is cancelled first, so there is only ever one in the
     * queue. Safe to call from any thread (it only posts to the main handler).
     */
    private void tick() {
        main.removeCallbacks(ticker);
        main.postDelayed(ticker, 1000);
    }

    /**
     * Polls the engine once: rebuilds the TUN if addresses changed, refreshes
     * Live, and restarts the whole session when the engine reports an error
     * and auto-reconnect is on. Runs on the main thread. The next poll is
     * scheduled at the end, so a slow poll delays the next one rather than
     * stacking up.
     */
    private void tickOnce() {
        if (stopping.get() || engine == null) {
            return;
        }
        Snapshot snap = engine.snapshot();
        if (snap == null) {
            tick();
            return;
        }
        String st = nz(snap.getState());
        try {
            if ("on".equals(st) || "connecting".equals(st)) {
                applyTun(snap);
            }
        } catch (Exception e) {
            Journal.add("tun " + e.getMessage());
        }
        paint(snap);
        if ("error".equals(st) && !stopping.get()) {
            Journal.add("session " + snap.getErr());
            if (Prefs.reconnect(this)) {
                // Restart off the main thread; startSession schedules polling
                // again once the new engine is up, so no tick() here.
                go.execute(() -> {
                    stopEngine();
                    startSession();
                });
                return;
            }
        }
        tick();
    }

    /**
     * Follows the physical network under the VPN (Wi-Fi, LTE, Ethernet).
     * On a switch it tells Android which network the VPN rides on and calls
     * Engine.notifyPathChange. The engine then moves the session to the new
     * network without a new login: the GET front rebinds its connection pool
     * and keeps the same session id; other loft fronts are parked (the node
     * holds the session for a while after a drop) and resume. The overlay IP
     * stays the same. Live.holding shows "hold" in the UI until the next
     * snapshot reports "on". The first onAvailable is the network at start
     * and is not a switch. The callbacks run on a ConnectivityManager
     * thread, not the main thread.
     */
    private void watchPath() {
        ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm == null) {
            return;
        }
        unwatchPath();
        seenPath = false;
        pathCb = new ConnectivityManager.NetworkCallback() {
            /** A physical network came up: ride on it, and report a switch after the first one. */
            @Override
            public void onAvailable(Network network) {
                // Never ride on a VPN (our own TUN included): that would loop.
                if (isVPN(cm, network)) {
                    return;
                }
                underlying = network;
                Live.underlying = label(cm, network);
                // Tell Android which real network carries the VPN, so the system
                // reports the right type and metering for it.
                try {
                    setUnderlyingNetworks(new Network[]{network});
                } catch (Exception ignored) {
                }
                if (seenPath) {
                    Live.holding = true;
                    Journal.add("path " + Live.underlying);
                    Engine eng = engine;
                    if (eng != null) {
                        eng.notifyPathChange();
                    }
                    Bus.post();
                }
                seenPath = true;
            }

            /** The network under the VPN went away: show "hold" and let the engine park the session. */
            @Override
            public void onLost(Network network) {
                if (network.equals(underlying)) {
                    Live.holding = true;
                    Live.underlying = "";
                    Engine eng = engine;
                    if (eng != null) {
                        eng.notifyPathChange();
                    }
                    Bus.post();
                }
            }

            /** Keeps the network label (Wi-Fi, LTE, Ethernet) in Live current. */
            @Override
            public void onCapabilitiesChanged(Network network, NetworkCapabilities cap) {
                if (!network.equals(underlying) || cap == null) {
                    return;
                }
                Live.underlying = label(cm, network);
            }
        };
        try {
            // Default network after establish() is the VPN itself. Asking
            // for NOT_VPN keeps GET sockets on Wi-Fi / LTE / Ethernet.
            NetworkRequest req = new NetworkRequest.Builder()
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    .build();
            cm.registerNetworkCallback(req, pathCb);
        } catch (Exception e) {
            Journal.add("path watch " + e.getMessage());
        }
    }

    /** True when the network is a VPN (this one or another), which must not be used as the path. */
    private static boolean isVPN(ConnectivityManager cm, Network n) {
        try {
            NetworkCapabilities c = cm.getNetworkCapabilities(n);
            return c != null && c.hasTransport(NetworkCapabilities.TRANSPORT_VPN);
        } catch (Exception e) {
            return false;
        }
    }

    /** Removes the network callback registered by watchPath, if any. */
    private void unwatchPath() {
        ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm != null && pathCb != null) {
            try {
                cm.unregisterNetworkCallback(pathCb);
            } catch (Exception ignored) {
            }
        }
        pathCb = null;
    }

    /** Short name of the network type for the UI: "Wi-Fi", "LTE" (any cellular), "Ethernet" or "". */
    private static String label(ConnectivityManager cm, Network n) {
        try {
            NetworkCapabilities c = cm.getNetworkCapabilities(n);
            if (c == null) {
                return "";
            }
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) {
                return "Wi-Fi";
            }
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR)) {
                return "LTE";
            }
            if (c.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)) {
                return "Ethernet";
            }
        } catch (Exception ignored) {
        }
        return "";
    }

    /**
     * Engine callback: one packet from the tunnel, written to the TUN for apps
     * to receive. Called on an engine thread. The io lock keeps the write from
     * racing with applyTun / stopSession closing the stream; a packet that
     * arrives while there is no TUN is simply dropped.
     */
    @Override
    public void onPacket(byte[] pkt) {
        if (pkt == null) {
            return;
        }
        synchronized (io) {
            if (out == null) {
                return;
            }
            try {
                out.write(pkt);
            } catch (Exception ignored) {
            }
        }
    }

    /**
     * Engine callback for every socket it opens to the node: exclude it from
     * the VPN so the tunnel's own traffic does not go back into the tunnel.
     * The Go side passes the descriptor as a long; VpnService.protect takes an int.
     */
    @Override
    public boolean protect(long fd) {
        boolean ok = protect((int) fd);
        if (ok) {
            return true;
        }
        // Handshake runs before Builder.establish(). A false here would
        // fail every dial and surface as Engine "timeout".
        if (tun == null) {
            Journal.add("protect skipped fd=" + fd);
            return true;
        }
        Journal.add("protect failed fd=" + fd);
        return false;
    }

    /**
     * Rebuilds TUN routes when split, default route, LAN bypass, the lists,
     * the per-app filter or Local DNS change. Android delivers preference
     * listeners on the main thread; the work runs on {@code go} so the UI is
     * not blocked by list downloads or establish(). It is kept in a field
     * because SharedPreferences holds listeners only weakly: a lambda with no
     * other reference could be garbage-collected and silently stop firing.
     */
    private final SharedPreferences.OnSharedPreferenceChangeListener routeWatch = (sp, k) -> {
        if (!("split".equals(k) || "default_route".equals(k)
                || "lan_bypass".equals(k) || "split_store".equals(k)
                || "app_mode".equals(k) || "app_except".equals(k) || "app_only".equals(k)
                || "local_dns".equals(k) || "local_dns_addr".equals(k))) {
            return;
        }
        // Turning split on may need a list download first (pullRoutes saves
        // split_store, which brings us back here once more for the rebuild).
        go.execute(() -> {
            if ("split".equals(k) && Prefs.split(this)) {
                pullRoutes();
            }
            rebuildRoutes();
        });
    };

    /** Starts listening for route-related settings changes (see routeWatch). */
    private void watchRoutePrefs() {
        Prefs.p(this).registerOnSharedPreferenceChangeListener(routeWatch);
    }

    /** Stops listening for route-related settings changes. */
    private void unwatchRoutePrefs() {
        Prefs.p(this).unregisterOnSharedPreferenceChangeListener(routeWatch);
    }

    /**
     * Daily auto-pull of zone lists, same 20 h window as macOS. Only when
     * split routing is on; rebuilds the TUN when a list was downloaded.
     * Does network IO, so it must run on a worker thread.
     */
    private void pullRoutes() {
        if (!Prefs.split(this) || stopping.get()) {
            return;
        }
        SplitStore st = SplitStore.load(this);
        if (!st.pullDue()) {
            return;
        }
        st.save(this);
        Journal.add("routes pulled");
        rebuildRoutes();
    }

    /**
     * Sets the TUN's DNS servers: the user's own address when "Local DNS" is
     * set to an IP, otherwise the comma list the node gave in the Snapshot.
     * Only numeric addresses are used; names are skipped.
     */
    private void addDns(Builder b, Snapshot snap) {
        if ("ip".equals(Prefs.dnsMode(this))) {
            InetAddress a = Cidr.numeric(Prefs.dnsAddr(this));
            if (a != null) {
                b.addDnsServer(a);
                return;
            }
        }
        if (snap.getDNS() == null) {
            return;
        }
        for (String s : snap.getDNS().split(",")) {
            String d = s.trim();
            if (d.isEmpty()) {
                continue;
            }
            InetAddress a = Cidr.numeric(d);
            if (a != null) {
                b.addDnsServer(a);
            }
        }
    }

    /**
     * Applies the per-app filter: in "only" mode just the chosen apps use the
     * VPN, in the other mode the chosen apps are left out. "all" or an empty
     * list changes nothing. Apps that are no longer installed are logged.
     */
    private void addApps(Builder b) {
        String mode = Prefs.appMode(this);
        if ("all".equals(mode)) {
            return;
        }
        Set<String> pkgs = Prefs.appSet(this, mode);
        if (pkgs.isEmpty()) {
            return;
        }
        for (String pkg : pkgs) {
            try {
                if ("only".equals(mode)) {
                    b.addAllowedApplication(pkg);
                } else {
                    b.addDisallowedApplication(pkg);
                }
            } catch (Exception e) {
                Journal.add("app " + pkg + " " + e.getMessage());
            }
        }
    }

    /** Re-runs applyTun on a fresh Snapshot so changed settings reach the TUN. Called on a worker thread. */
    private void rebuildRoutes() {
        if (stopping.get() || engine == null) {
            return;
        }
        try {
            applyTun(engine.snapshot());
        } catch (Exception e) {
            Journal.add("tun " + e.getMessage());
        }
    }

    /**
     * Stops polling, network watching, the engine and the TUN, and resets Live.
     * Called on the main thread from onStartCommand(stop), onRevoke and
     * onDestroy, so Engine.stop runs there too.
     */
    private void stopSession() {
        main.removeCallbacks(ticker);
        unwatchPath();
        unwatchRoutePrefs();
        stopEngine();
        synchronized (io) {
            closeTunLocked();
            lastAddr = "";
        }
        running = false;
        Live.reset();
        Bus.post();
    }

    /**
     * Stops the Go engine and signals the TUN reader thread; the TUN itself
     * stays open. On a reconnect (tickOnce) the next applyTun sees the same
     * key and keeps this TUN. interrupt() does not wake a reader blocked in
     * read(), so that thread usually keeps running and simply picks up the
     * new engine from the field.
     */
    private void stopEngine() {
        Engine eng = engine;
        engine = null;
        if (eng != null) {
            try {
                eng.stop();
            } catch (Exception ignored) {
            }
        }
        Thread r = reader;
        reader = null;
        if (r != null) {
            r.interrupt();
        }
    }

    /** Closes the TUN output stream and descriptor. Caller must hold {@code io}. */
    private void closeTunLocked() {
        try {
            if (out != null) {
                out.close();
            }
        } catch (Exception ignored) {
        }
        out = null;
        try {
            if (tun != null) {
                tun.close();
            }
        } catch (Exception ignored) {
        }
        tun = null;
    }

    /**
     * Redraws the ongoing notification from the current Live state. Called
     * from paint (about once a second) and fail; each call rebuilds the whole
     * notification and posts it to the system.
     */
    private void updateNote() {
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (nm != null) {
            nm.notify(NOTE, notice(noteText()));
        }
    }

    /**
     * Notification text: "hold" while switching networks, profile name, kind,
     * overlay IP and, on error, the human explanation from Explain.
     */
    private String noteText() {
        StringBuilder b = new StringBuilder();
        if (Live.holding) {
            b.append(getString(R.string.hold)).append(" · ");
        }
        if (!Live.name.isEmpty()) {
            b.append(Live.name);
        }
        if (!Live.kind.isEmpty()) {
            if (b.length() > 0) {
                b.append(" · ");
            }
            b.append(Live.kind);
        }
        if (!Live.ip.isEmpty()) {
            b.append(' ').append(Live.ip);
        }
        if (!Live.err.isEmpty() && "error".equals(Live.state)) {
            if (b.length() > 0) {
                b.append(" · ");
            }
            b.append(Explain.display(this, Live.err));
        }
        return b.length() == 0 ? getString(R.string.connected) : b.toString();
    }

    /**
     * Builds the ongoing notification: tapping opens the home screen, the
     * button disconnects. Creates the low-importance channel first on API 26+
     * (creating an existing channel again is allowed and changes nothing).
     * FLAG_IMMUTABLE is required for PendingIntents from API 31 on.
     */
    private Notification notice(String text) {
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(CH, "loft", NotificationManager.IMPORTANCE_LOW);
            ch.setShowBadge(false);
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm != null) {
                nm.createNotificationChannel(ch);
            }
        }
        int flags = PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE;
        PendingIntent open = PendingIntent.getActivity(this, 0,
                new Intent(this, Home.activity()), flags);
        PendingIntent stop = PendingIntent.getService(this, 1,
                new Intent(this, LoftVpnService.class).setAction(ACTION_STOP), flags);
        Notification.Builder b = Build.VERSION.SDK_INT >= 26
                ? new Notification.Builder(this, CH)
                : new Notification.Builder(this);
        return b.setContentTitle("loft")
                .setContentText(text)
                .setSmallIcon(R.drawable.ic_loft)
                .setContentIntent(open)
                .setOngoing(true)
                .addAction(0, getString(R.string.disconnect), stop)
                .build();
    }

    /**
     * Errors that a retry cannot fix: the key is wrong, revoked or expired, the
     * member is not allowed, the clock is off, the instance is refused, or the
     * network is disabled on the node. Reconnecting would only hammer the node.
     */
    private static boolean fatalErr(String msg) {
        if (msg == null || msg.isEmpty()) {
            return false;
        }
        // The engine's error codes (auth_*, net_*) appear inside the message text.
        String s = msg.toLowerCase();
        return s.contains("auth_bad_key") || s.contains("auth_revoked")
                || s.contains("auth_expired") || s.contains("auth_not_authorized")
                || s.contains("auth_clock") || s.contains("auth_instance")
                || s.contains("net_disabled")
                || (s.contains("get handshake") && s.contains("404"));
    }

    /** Null-safe trim: null becomes "". */
    private static String nz(String s) {
        return s == null ? "" : s.trim();
    }

    /**
     * Starts the service. VPN permission must already be granted
     * (VpnService.prepare). API 26+ requires startForegroundService for a
     * service that will go foreground; onStartCommand then has a few seconds
     * to call startForeground.
     */
    static void requestStart(Context c) {
        Intent i = new Intent(c, LoftVpnService.class).setAction(ACTION_START);
        if (Build.VERSION.SDK_INT >= 26) {
            c.startForegroundService(i);
        } else {
            c.startService(i);
        }
    }

    /**
     * Asks the running service to end the session and stop itself. A plain
     * startService is enough here: the service is already in the foreground.
     */
    static void requestStop(Context c) {
        c.startService(new Intent(c, LoftVpnService.class).setAction(ACTION_STOP));
    }
}
