/*
 * Android client, shared by the phone and TV builds: the Android VpnService
 * that runs the whole VPN session in-process. It drives the Go engine, owns
 * the TUN interface, keeps engine sockets off the VPN, follows network
 * switches, and publishes state into Live for the screens and notification.
 * All session work runs on one background thread (loft-ctl), never on the
 * main thread, so the UI stays smooth however slow the network or the system.
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
import android.os.HandlerThread;
import android.os.ParcelFileDescriptor;
import android.os.SystemClock;
import android.system.ErrnoException;
import android.system.Os;
import android.system.OsConstants;
import android.system.StructPollfd;

import java.io.File;
import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.net.InetAddress;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;

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
 * reads go to Engine.injectPacketN, and the engine hands packets back
 * through onPacket. A "front" is one of the HTTPS transports the protocol
 * rides on (GET, WebSocket, HTTP/2, QUIC, ...). The engine opens its own
 * sockets to the node's fronts; protect() keeps those sockets off the VPN so
 * the tunnel's traffic does not loop back into itself. Dual-stack means IPv4
 * and IPv6 overlay addresses, both taken from the engine's Snapshot.
 *
 * <p><b>Threads.</b> The main thread draws the UI, so it must never wait on
 * the network, the disk or a slow system call; on Xiaomi HyperOS even a few
 * such waits per second show up as a frozen screen. So:
 * <ul>
 * <li><b>loft-ctl</b> (one per process, {@link #CTL}) owns the session: it
 *     dials, polls the engine once a second, rebuilds the TUN, follows network
 *     switches and tears down. Everything that changes session state runs
 *     here, one task at a time, so none of it needs locks against itself.
 *     It is shared by every service instance on purpose: the Go engine keeps
 *     some process-wide state (the network sockets are bound to, the overlay
 *     address), and an old instance's stop must never overlap a new
 *     instance's start.</li>
 * <li>The <b>main thread</b> only receives Android lifecycle calls
 *     (onStartCommand, onRevoke, onDestroy) and settings changes, and turns
 *     them into tasks for loft-ctl.</li>
 * <li><b>loft-tun</b> reads packets apps write into the TUN and hands them to
 *     the engine; the engine calls onPacket and protect from its own threads.</li>
 * <li>The {@code go} pool does list downloads (network IO that can take long
 *     and must not hold up the poll).</li>
 * </ul>
 * Tasks are posted with this instance as the token, so one instance can
 * cancel its own pending work without touching another's.
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
    /** How often the engine state is polled for the UI, counters and TUN changes. */
    private static final long TICK_MS = 1000;
    /**
     * A burst of settings changes (typing a DNS address, ticking apps, editing
     * lists) is merged into one TUN rebuild this long after the last change.
     */
    private static final long REBUILD_DEBOUNCE_MS = 300;
    /**
     * TUN read buffer size. 64 KiB is bigger than any MTU, and, more
     * importantly, big enough for ART to place the array in its large-object
     * space, where arrays never move. gomobile passes a byte[] to Go through
     * JNI GetByteArrayElements: for an array that cannot move ART returns a
     * direct pointer, for a small movable one it mallocs and copies the whole
     * array on every call. So a large buffer reaches Go with zero copies.
     */
    private static final int READ_BUF = 64 * 1024;

    /** The process-wide session thread (see the class comment); started on first use. */
    private static final Handler CTL;

    static {
        HandlerThread t = new HandlerThread("loft-ctl");
        t.start();
        CTL = new Handler(t.getLooper());
    }

    /**
     * Counts sessions started in this process. A teardown remembers the value
     * from when it was asked for and resets Live only if no newer session has
     * started since, so a slow stop of an old session cannot blank the screen
     * of the new one.
     */
    private static final AtomicLong EPOCH = new AtomicLong();

    /**
     * True while a session is running, so screens (home, TV, quick-settings
     * tile) can show the right button state even before Live.state turns
     * "connecting". Static because screens have no reference to the service.
     */
    static volatile boolean running;

    /** Worker threads for list downloads, which can take long. */
    private final ExecutorService go = Executors.newCachedThreadPool();
    /** Guards the TUN write side: {@code out} against close and swap in applyTun / teardown. */
    private final Object io = new Object();
    /** Set once the user or system asked to stop; blocks retries and new sessions. */
    private final AtomicBoolean stopping = new AtomicBoolean(false);
    /**
     * Bumped on the main thread by every route-related settings change
     * (routeSettingChanged). It is part of the TUN key, so the next applyTun rebuilds;
     * this replaces re-reading and re-parsing all lists every second just to
     * find out whether they changed.
     */
    private final AtomicInteger routeGen = new AtomicInteger();
    /** True while a debounced rebuild is queued; the poll leaves the TUN alone until it runs. */
    private final AtomicBoolean rebuildQueued = new AtomicBoolean();

    /** The running Go engine, or null between sessions. Written on loft-ctl, read by the TUN reader. */
    private volatile Engine engine;
    /**
     * The engine inside Engine.start, which blocks loft-ctl until the
     * handshake ends. A stop request from the main thread stops this one to
     * cut the wait short.
     */
    private volatile Engine dialing;
    /** The TUN descriptor from Builder.establish(); null while there is none. Swapped under io. */
    private volatile ParcelFileDescriptor tun;
    /** Write side of the TUN: onPacket writes packets for apps here. Guarded by io. */
    private FileOutputStream out;
    /** Route plan the current TUN was built from; the reader uses it to drop blocked packets. */
    private volatile RoutePlan plan;
    /** Callback registered by watchPath to follow the physical network; null when not watching. */
    private ConnectivityManager.NetworkCallback pathCb;
    /** The physical network (Wi-Fi, LTE, Ethernet) the VPN currently rides on. */
    private volatile Network underlying;
    /** False until the first onAvailable, which is the start network and not a switch. loft-ctl only. */
    private boolean seenPath;
    /**
     * Key of everything the current TUN was built from: addresses, DNS, MTU,
     * node routes, front IPs and {@link #routeGen}. Built from a handful of
     * Snapshot strings, so comparing it every second is cheap; RoutePlan (which
     * reads the lists) runs only when it changes. loft-ctl only.
     */
    private String lastKey = "";
    /** Thread that reads packets from the current TUN (see TunReader). loft-ctl only. */
    private TunReader reader;
    /** Reconnect attempts since the last successful start. loft-ctl only. */
    private int retries;
    /**
     * What paint already saved from the node this session (mask key, fronts
     * list, operator lists tag), so it saves each value once instead of every
     * second. loft-ctl only.
     */
    private String lastMask = "";
    private String lastFronts = "";
    private String lastTag = "";
    /** Text of the notification on screen, to skip posting an identical one. */
    private volatile String lastNote = "";

    /** Cached system objects for the notification, made once in onCreate. */
    private NotificationManager nm;
    private PendingIntent openPi;
    private PendingIntent stopPi;

    /** The poll as one Runnable object, so it can be cancelled and re-posted. */
    private final Runnable ticker = this::tickOnce;
    /** The debounced rebuild as one Runnable object (see routeSettingChanged). */
    private final Runnable rebuild = () -> {
        rebuildQueued.set(false);
        rebuildRoutes();
    };

    /**
     * Creates the notification channel and the two PendingIntents once.
     * Each of these is a call into a system process; doing them for every
     * notification update, as before, cost four such calls a second.
     */
    @Override
    public void onCreate() {
        super.onCreate();
        nm = getSystemService(NotificationManager.class);
        NotificationChannel ch = new NotificationChannel(CH, "loft", NotificationManager.IMPORTANCE_LOW);
        ch.setShowBadge(false);
        if (nm != null) {
            nm.createNotificationChannel(ch);
        }
        int flags = PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE;
        openPi = PendingIntent.getActivity(this, 0, new Intent(this, Home.activity()), flags);
        stopPi = PendingIntent.getService(this, 1,
                new Intent(this, LoftVpnService.class).setAction(ACTION_STOP), flags);
    }

    /**
     * Handles start and stop intents on the main thread and returns at once.
     * Start goes foreground immediately (Android kills a service started with
     * startForegroundService otherwise; API 34+ also requires the
     * "systemExempted" type for a VPN), then queues the dial on loft-ctl.
     * START_STICKY asks Android to restart the service if it kills the
     * process for memory.
     */
    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            shutdown(false);
            stopForeground(STOP_FOREGROUND_REMOVE);
            stopSelf();
            return START_NOT_STICKY;
        }
        stopping.set(false);
        String text = getString(R.string.connecting);
        lastNote = text;
        Notification n = notice(text);
        if (Build.VERSION.SDK_INT >= 34) {
            startForeground(NOTE, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED);
        } else {
            startForeground(NOTE, n);
        }
        post(this::startSession, 0);
        return START_STICKY;
    }

    /**
     * The user turned the VPN off in system settings or another VPN took over.
     * The base class then stops the service.
     */
    @Override
    public void onRevoke() {
        shutdown(false);
        super.onRevoke();
    }

    /**
     * The service is going away. A fatal error (bad key, revoked, ...) is
     * kept in Live after the teardown so the main screen can still explain
     * why the connection stopped.
     */
    @Override
    public void onDestroy() {
        shutdown(true);
        go.shutdownNow();
        super.onDestroy();
    }

    /**
     * Ends the session without blocking the calling (main) thread: marks the
     * service as stopping, drops this instance's queued work, cuts short a
     * dial in progress, and queues the real teardown on loft-ctl. Safe to call
     * more than once.
     *
     * @param keepErr keep a final error in Live for the UI (true when the
     *                service dies on its own, false when the user stopped it)
     */
    private void shutdown(boolean keepErr) {
        stopping.set(true);
        CTL.removeCallbacksAndMessages(this);
        rebuildQueued.set(false);
        unwatchRoutePrefs();
        // loft-ctl may be blocked inside Engine.start for many seconds. Stop
        // that engine from another thread so the dial fails fast; the
        // teardown below then runs right after it.
        Engine d = dialing;
        if (d != null) {
            Thread t = new Thread(() -> {
                try {
                    d.stop();
                } catch (Exception ignored) {
                }
            }, "loft-abort");
            t.setDaemon(true);
            t.start();
        }
        long epoch = EPOCH.get();
        post(() -> teardown(keepErr, epoch), 0);
    }

    /**
     * Posts a task for this instance on loft-ctl after {@code delayMs}. The
     * instance is the token, so shutdown can cancel exactly its own tasks
     * (the thread is shared with other instances).
     */
    private void post(Runnable r, long delayMs) {
        CTL.postAtTime(r, this, SystemClock.uptimeMillis() + delayMs);
    }

    /**
     * Connects the active profile, on loft-ctl: builds the engine, hands it
     * the socket protector, the packet sink and the instance file, starts it
     * with the profile's YAML (this blocks until the handshake ends), and then
     * builds the TUN from the first Snapshot (the engine's current state:
     * addresses, routes, DNS, counters). The instance file keeps the
     * instance_id, a persistent id of this install; the node uses it to
     * recognise the same client after a reconnect. A start while a session
     * is already up replaces it.
     */
    private void startSession() {
        if (stopping.get()) {
            return;
        }
        EPOCH.incrementAndGet();
        stopEngine();
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
        lastMask = "";
        lastFronts = "";
        lastTag = "";
        Bus.post();
        updateNote();
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
            dialing = eng;
            try {
                eng.start(yaml);
            } finally {
                dialing = null;
            }
            // The user may have pressed stop while the handshake was running;
            // the dial still succeeded, so close it again right away.
            if (stopping.get()) {
                eng.stop();
                return;
            }
            engine = eng;
            Snapshot snap = eng.snapshot();
            if (!applyTun(snap)) {
                stopEngine();
                fail("no overlay address");
                return;
            }
            paint(snap);
            retries = 0;
            Live.since = System.currentTimeMillis();
            Journal.add("up " + Live.ip + (Live.ip6.isEmpty() ? "" : " " + Live.ip6));
            tick();
            go.execute(this::pullRoutes);
        } catch (Exception e) {
            if (!stopping.get()) {
                fail(e.getMessage());
            }
        }
    }

    /**
     * Records an error and, when auto-reconnect is on and the error is not
     * fatal, retries with exponential backoff (400 ms doubling up to 8 s, at most
     * 8 tries). Otherwise stops the service. Runs on loft-ctl.
     */
    private void fail(String msg) {
        Live.state = "error";
        Live.err = msg == null ? "" : msg;
        Journal.add("error " + Live.err);
        Bus.post();
        updateNote();
        if (!stopping.get() && Prefs.reconnect(this) && retries < 8 && !fatalErr(Live.err)) {
            retries++;
            post(this::startSession, Math.min(8000, 200 * (1 << retries)));
            return;
        }
        stopSelf();
    }

    /**
     * Creates or rebuilds the TUN from a Snapshot, on loft-ctl. Returns false
     * when the node gave no overlay address (the address inside the members'
     * virtual network) or Android refused the interface.
     *
     * <p>First a cheap key is built from the Snapshot and {@link #routeGen};
     * when it matches the current TUN nothing else happens. Only on a change
     * does RoutePlan read the settings and lists and work out the routes:
     * front IPs and split bypass prefixes are excluded so they stay on the real
     * network. The new descriptor is swapped in and the old one closed, which
     * also ends the old reader thread.
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
        // 1. Nothing the TUN depends on changed: keep it.
        String key = ip + "/" + snap.getPrefix() + "|" + ip6 + "/" + snap.getPrefix6()
                + "|" + snap.getDNS() + "|" + snap.getFrontIPs() + "|" + snap.getMTU()
                + "|" + snap.getRoutes() + "|" + routeGen.get();
        if (key.equals(lastKey) && tun != null) {
            return true;
        }
        RoutePlan rp = RoutePlan.of(this, snap);
        // 2. Describe the new interface: session name, MTU, overlay addresses, DNS.
        // setBlocking(false): TunReader waits in poll() instead of read(), so
        // it can be woken and stopped (see TunReader).
        Builder b = new Builder()
                .setSession(Live.name.isEmpty() ? "loft" : Live.name)
                .setMtu(snap.getMTU() > 0 ? (int) snap.getMTU() : 1280)
                .setBlocking(false);
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
        // 3. Add tunnel routes, then (API 33+) carve out excluded prefixes.
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
        // 4. Per-app filter and the physical network, then create the TUN.
        addApps(b);
        Network u = underlying;
        if (u != null) {
            b.setUnderlyingNetworks(new Network[]{u});
        }
        // establish() is a call into the system VPN service and can take a
        // while, which is why it runs here and never on the main thread. It
        // returns null when VPN permission was revoked.
        ParcelFileDescriptor pfd = b.establish();
        if (pfd == null) {
            return false;
        }
        // 5. Swap in the new descriptor; closeTunLocked wakes and ends the old reader.
        synchronized (io) {
            closeTunLocked();
            tun = pfd;
            out = new FileOutputStream(pfd.getFileDescriptor());
            plan = rp;
        }
        lastKey = key;
        // 6. Start reading from the new TUN.
        pumpReads();
        return true;
    }

    /**
     * Copies a Snapshot into Live for the UI and the notification, on
     * loft-ctl. Once the session is on, it also saves what the node taught
     * during the handshake: the mask public key (mask is a mode where the node
     * shows a certificate shaped like its cover website's; the next start
     * passes the key to the engine as mask_pub), the list of fronts the node
     * offers, and the operator's route lists. Each is saved only when it
     * differs from what this session already saved: saving means parsing and
     * writing the profile files, which must not happen every second.
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
        // 2. When up: save what the node taught, once per new value.
        if ("on".equals(Live.state)) {
            Live.holding = false;
            Profiles store = LoftApp.of(this).profiles();
            String id = store.activeId();
            String pbk = nz(snap.getMaskPub());
            if (!pbk.isEmpty() && !pbk.equals(lastMask)) {
                lastMask = pbk;
                store.applyLearnedMask(id, pbk);
            }
            String fronts = nz(snap.getFronts());
            if (!fronts.isEmpty() && !fronts.equals(lastFronts)) {
                lastFronts = fronts;
                store.applyLearnedFronts(id, fronts);
            }
            // The operator lists can be megabytes. The short tag says whether
            // they changed; only then is the JSON itself copied out of Go.
            String tag = nz(snap.getProfileTag());
            if (!tag.isEmpty() && !tag.equals(lastTag)) {
                lastTag = tag;
                String pj = nz(snap.getProfileJSON());
                if (!pj.isEmpty()) {
                    ProfileStore.applyOperator(this, pj, Live.name);
                }
            }
        }
        updateNote();
        Bus.post();
    }

    /**
     * Starts a reader on the current TUN descriptor (see TunReader). Runs on
     * loft-ctl right after a new descriptor is swapped in.
     */
    private void pumpReads() {
        ParcelFileDescriptor pfd = tun;
        if (pfd == null) {
            return;
        }
        try {
            TunReader r = new TunReader(pfd);
            reader = r;
            r.start();
        } catch (Exception e) {
            Journal.add("tun reader " + e.getMessage());
        }
    }

    /**
     * Reads packets apps write into the TUN and passes them to the engine.
     * Packets to "drop" destinations are discarded here.
     *
     * <p>Why poll() and a pipe: a thread blocked in read() on a TUN is not
     * woken when another thread closes the descriptor (Linux keeps the file
     * alive until the read returns). A blocking reader would therefore hang
     * forever after every TUN rebuild, leaking a thread and keeping the old
     * interface alive. Instead the descriptor is non-blocking and the thread
     * sleeps in poll() on two things at once: the TUN, and the read end of a
     * private pipe. {@link #finish} closes the pipe's write end, poll()
     * reports a hang-up on the read end, and the thread ends at once.
     * Each end has exactly one owner that closes it exactly once (the reader
     * closes [0] on exit, finish closes [1]), so a descriptor number is never
     * closed twice, which could otherwise hit an unrelated file that reused it.
     *
     * <p>The loop allocates nothing per packet: the block check reads the
     * destination straight from the buffer, and injectPacketN passes the
     * buffer and length to Go, which copies just those bytes (see READ_BUF
     * for why the hand-off itself copies nothing).
     */
    private final class TunReader extends Thread {
        private final FileDescriptor fd;
        /** Wake-up pipe: [0] is polled (and closed) by the reader, [1] is closed by finish(). */
        private final FileDescriptor[] wake;
        /** Makes finish() close the write end only once. */
        private final AtomicBoolean finished = new AtomicBoolean();

        TunReader(ParcelFileDescriptor pfd) throws ErrnoException {
            super("loft-tun");
            setDaemon(true);
            fd = pfd.getFileDescriptor();
            wake = Os.pipe();
        }

        /** Asks the reader to end; returns immediately. Safe to call more than once. */
        void finish() {
            if (finished.compareAndSet(false, true)) {
                closeQuietly(wake[1]);
            }
        }

        @Override
        public void run() {
            byte[] buf = new byte[READ_BUF];
            StructPollfd tunP = new StructPollfd();
            tunP.fd = fd;
            tunP.events = (short) OsConstants.POLLIN;
            StructPollfd wakeP = new StructPollfd();
            wakeP.fd = wake[0];
            wakeP.events = (short) OsConstants.POLLIN;
            StructPollfd[] set = {tunP, wakeP};
            try {
                while (!stopping.get()) {
                    try {
                        Os.poll(set, -1);
                    } catch (ErrnoException e) {
                        if (e.errno == OsConstants.EINTR) {
                            continue;
                        }
                        return;
                    }
                    if (wakeP.revents != 0) {
                        return;
                    }
                    if ((tunP.revents & (OsConstants.POLLERR | OsConstants.POLLHUP | OsConstants.POLLNVAL)) != 0) {
                        return;
                    }
                    // Drain everything that is queued, then go back to poll().
                    while (true) {
                        int n;
                        try {
                            n = Os.read(fd, buf, 0, buf.length);
                        } catch (ErrnoException e) {
                            if (e.errno == OsConstants.EAGAIN || e.errno == OsConstants.EINTR) {
                                break;
                            }
                            return;
                        }
                        if (n <= 0) {
                            break;
                        }
                        Engine eng = engine;
                        RoutePlan rp = plan;
                        if (eng == null || (rp != null && rp.destDropped(buf, n))) {
                            continue;
                        }
                        try {
                            eng.injectPacketN(buf, n);
                        } catch (Exception ignored) {
                            // Queue full or engine stopping: drop the packet like
                            // a congested link would; TCP inside resends it.
                        }
                    }
                }
            } catch (Exception ignored) {
                // The descriptor was closed under us (EBADF): the TUN is gone.
            } finally {
                closeQuietly(wake[0]);
            }
        }
    }

    /** Closes a raw descriptor, ignoring errors. */
    private static void closeQuietly(FileDescriptor d) {
        try {
            Os.close(d);
        } catch (Exception ignored) {
        }
    }

    /** Schedules the next poll of the engine state; only one is ever queued. */
    private void tick() {
        CTL.removeCallbacks(ticker, this);
        post(ticker, TICK_MS);
    }

    /**
     * Polls the engine once, on loft-ctl: rebuilds the TUN if what it depends
     * on changed, refreshes Live, and restarts the whole session when the
     * engine reports an error and auto-reconnect is on. The next poll is
     * scheduled at the end, so a slow poll delays the next one rather than
     * stacking up.
     */
    private void tickOnce() {
        Engine eng = engine;
        if (stopping.get() || eng == null) {
            return;
        }
        Snapshot snap = eng.snapshot();
        if (snap == null) {
            tick();
            return;
        }
        String st = nz(snap.getState());
        try {
            // A queued debounced rebuild will apply the new settings; doing
            // it here as well would build the same TUN twice.
            if (("on".equals(st) || "connecting".equals(st)) && !rebuildQueued.get()) {
                applyTun(snap);
            }
        } catch (Exception e) {
            Journal.add("tun " + e.getMessage());
        }
        paint(snap);
        if ("error".equals(st) && !stopping.get()) {
            Journal.add("session " + snap.getErr());
            if (Prefs.reconnect(this)) {
                // startSession replaces the failed engine and schedules
                // polling again once the new one is up.
                startSession();
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
     * and is not a switch. The callbacks are delivered on loft-ctl, in order
     * with the rest of the session work.
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
                Live.underlying = label(cap);
            }
        };
        try {
            // Default network after establish() is the VPN itself. Asking
            // for NOT_VPN keeps GET sockets on Wi-Fi / LTE / Ethernet.
            NetworkRequest req = new NetworkRequest.Builder()
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                    .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                    .build();
            cm.registerNetworkCallback(req, pathCb, CTL);
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
            return label(cm.getNetworkCapabilities(n));
        } catch (Exception ignored) {
            return "";
        }
    }

    /** Same, from capabilities already in hand (no extra system call). */
    private static String label(NetworkCapabilities c) {
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
        return "";
    }

    /**
     * Engine callback: one packet from the tunnel, written to the TUN for apps
     * to receive. Called on an engine thread. The io lock keeps the write from
     * racing with applyTun / teardown closing the stream; a packet that
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
     * Settings that change the TUN: split, default route, LAN bypass, the
     * per-app filter and Local DNS. Android calls this on the main thread.
     * It is kept in a field because SharedPreferences holds listeners only
     * weakly: a lambda with no other reference could be garbage-collected and
     * silently stop firing.
     */
    private final SharedPreferences.OnSharedPreferenceChangeListener routeWatch = (sp, k) -> {
        if ("split".equals(k) || "default_route".equals(k) || "lan_bypass".equals(k)
                || "app_mode".equals(k) || "app_except".equals(k) || "app_only".equals(k)
                || "local_dns".equals(k) || "local_dns_addr".equals(k)) {
            routeSettingChanged(k);
        }
    };

    /** The split lists live in Doc, not in the preferences; this hears their changes. */
    private final Doc.Listener docWatch = name -> {
        if (Doc.SPLIT.equals(name)) {
            routeSettingChanged(name);
        }
    };

    /**
     * Something the routes depend on changed. Only bumps {@link #routeGen}
     * and (re)queues one rebuild on loft-ctl a moment later, so typing a DNS
     * address or ticking ten apps rebuilds once, not once per change. Safe on
     * any thread.
     */
    private void routeSettingChanged(String k) {
        routeGen.incrementAndGet();
        // Turning split on may need a list download first; pullRoutes saves
        // the lists, which brings us back here for the rebuild.
        if ("split".equals(k) && Prefs.split(this)) {
            go.execute(this::pullRoutes);
        }
        rebuildQueued.set(true);
        CTL.removeCallbacks(rebuild, this);
        post(rebuild, REBUILD_DEBOUNCE_MS);
    }

    /** Starts listening for route-related settings and list changes. */
    private void watchRoutePrefs() {
        Prefs.p(this).registerOnSharedPreferenceChangeListener(routeWatch);
        Doc.watch(docWatch);
    }

    /** Stops listening for route-related settings and list changes. */
    private void unwatchRoutePrefs() {
        Prefs.p(this).unregisterOnSharedPreferenceChangeListener(routeWatch);
        Doc.drop(docWatch);
    }

    /**
     * Daily auto-pull of zone lists, same 20 h window as macOS. Only when
     * split routing is on. Runs on the {@code go} pool because it downloads;
     * saving the lists fires docWatch, which rebuilds the TUN.
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

    /** Re-runs applyTun on a fresh Snapshot so changed settings reach the TUN. Runs on loft-ctl. */
    private void rebuildRoutes() {
        Engine eng = engine;
        if (stopping.get() || eng == null) {
            return;
        }
        try {
            applyTun(eng.snapshot());
        } catch (Exception e) {
            Journal.add("tun " + e.getMessage());
        }
    }

    /**
     * The real teardown, on loft-ctl: stop watching, stop the engine, close
     * the TUN. Live is reset only when no newer session started since the
     * stop was asked for ({@code epoch}), so the screen never shows a new
     * session as idle.
     */
    private void teardown(boolean keepErr, long epoch) {
        String err = Live.err;
        boolean keep = keepErr && err != null && !err.isEmpty()
                && ("error".equals(Live.state) || fatalErr(err));
        unwatchPath();
        stopEngine();
        synchronized (io) {
            closeTunLocked();
        }
        lastKey = "";
        if (EPOCH.get() != epoch) {
            return;
        }
        running = false;
        Live.reset();
        if (keep) {
            Live.state = "error";
            Live.err = err;
        }
        Bus.post();
    }

    /**
     * Stops the Go engine. The TUN stays open, so a reconnect that brings the
     * same addresses back keeps it (applyTun sees the same key). The reader
     * keeps running and picks up the next engine from the field; packets that
     * arrive in between are dropped.
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
    }

    /** Ends the reader, then closes the TUN output stream and descriptor. Caller must hold {@code io}. */
    private void closeTunLocked() {
        TunReader r = reader;
        reader = null;
        if (r != null) {
            r.finish();
        }
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
        plan = null;
    }

    /**
     * Redraws the ongoing notification from Live, but only when its text
     * changed. The text has no counters, so while a session runs this is
     * nearly always a no-op; posting an identical notification every second
     * made the system UI redo its work for nothing, and HyperOS is known to
     * stutter under that.
     */
    private void updateNote() {
        String text = noteText();
        if (text.equals(lastNote) || nm == null) {
            return;
        }
        lastNote = text;
        nm.notify(NOTE, notice(text));
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
     * Builds the ongoing notification from the cached PendingIntents: tapping
     * opens the home screen, the button disconnects. Building it is local
     * work; only notify() talks to the system. setOnlyAlertOnce keeps updates
     * silent, and FOREGROUND_SERVICE_IMMEDIATE (API 31+) shows it at once
     * instead of after the 10 s grace period Android gives new foreground
     * services.
     */
    private Notification notice(String text) {
        Notification.Builder b = new Notification.Builder(this, CH)
                .setContentTitle("loft")
                .setContentText(text)
                .setSmallIcon(R.drawable.ic_loft)
                .setContentIntent(openPi)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
                .setShowWhen(false)
                .setCategory(Notification.CATEGORY_SERVICE)
                .addAction(0, getString(R.string.disconnect), stopPi);
        if (Build.VERSION.SDK_INT >= 31) {
            b.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE);
        }
        return b.build();
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
        c.startForegroundService(new Intent(c, LoftVpnService.class).setAction(ACTION_START));
    }

    /**
     * Asks the running service to end the session and stop itself. A plain
     * startService is enough here: the service is already in the foreground.
     */
    static void requestStop(Context c) {
        c.startService(new Intent(c, LoftVpnService.class).setAction(ACTION_STOP));
    }
}
