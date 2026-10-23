package app.loft.android;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
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
 */
public final class LoftVpnService extends VpnService implements PacketSink, SocketProtector {
    static final String ACTION_STOP = "app.loft.android.STOP";
    static final String ACTION_START = "app.loft.android.START";
    private static final String CH = "loft.vpn";
    private static final int NOTE = 1;

    private final ExecutorService go = Executors.newCachedThreadPool();
    private final Handler main = new Handler(Looper.getMainLooper());
    private final Object io = new Object();
    private final AtomicBoolean stopping = new AtomicBoolean(false);

    private Engine engine;
    private ParcelFileDescriptor tun;
    private FileOutputStream out;
    private RoutePlan plan;
    private ConnectivityManager.NetworkCallback pathCb;
    private Network underlying;
    private boolean seenPath;
    private String lastAddr = "";
    private Thread reader;
    private int retries;
    private final Runnable ticker = this::tickOnce;

    static boolean running;

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

    @Override
    public void onRevoke() {
        stopping.set(true);
        stopSession();
        super.onRevoke();
    }

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

    private void startSession() {
        if (stopping.get()) {
            return;
        }
        Profile p = LoftApp.of(this).profiles().active();
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
        watchPath();
        try {
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
        } catch (Exception e) {
            fail(e.getMessage());
        }
    }

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

    private boolean applyTun(Snapshot snap) throws Exception {
        if (snap == null) {
            return false;
        }
        String ip = nz(snap.getIP());
        String ip6 = nz(snap.getIP6());
        if (ip.isEmpty() && ip6.isEmpty()) {
            return false;
        }
        String key = ip + "|" + ip6 + "|" + snap.getDNS() + "|" + snap.getFrontIPs() + "|" + snap.getMTU();
        if (key.equals(lastAddr) && tun != null) {
            paint(snap);
            return true;
        }
        RoutePlan rp = RoutePlan.of(this, snap);
        Builder b = new Builder()
                .setSession(Live.name.isEmpty() ? "loft" : Live.name)
                .setMtu(snap.getMTU() > 0 ? (int) snap.getMTU() : 1280)
                .setBlocking(true);
        if (Build.VERSION.SDK_INT >= 29) {
            b.setMetered(false);
        }
        if (rp.overlay4 != null) {
            b.addAddress(rp.overlay4.address(), rp.overlay4.bits);
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
        if (snap.getDNS() != null) {
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
        for (Cidr c : rp.include) {
            try {
                b.addRoute(c.address(), c.bits);
            } catch (Exception ignored) {
            }
        }
        if (Build.VERSION.SDK_INT >= 33) {
            for (Cidr c : rp.exclude) {
                try {
                    b.excludeRoute(new IpPrefix(InetAddress.getByAddress(c.addr), c.bits));
                } catch (Exception ignored) {
                }
            }
        }
        if (underlying != null && Build.VERSION.SDK_INT >= 22) {
            b.setUnderlyingNetworks(new Network[]{underlying});
        }
        ParcelFileDescriptor pfd = b.establish();
        if (pfd == null) {
            return false;
        }
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
        pumpReads();
        paint(snap);
        return true;
    }

    private void paint(Snapshot snap) {
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
        if ("on".equals(Live.state)) {
            Live.holding = false;
        }
        updateNote();
        Bus.post();
    }

    private void pumpReads() {
        final ParcelFileDescriptor pfd;
        synchronized (io) {
            pfd = tun;
        }
        if (pfd == null) {
            return;
        }
        reader = new Thread(() -> {
            byte[] buf = new byte[2048];
            try (FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
                int n;
                while (!stopping.get() && (n = in.read(buf)) > 0) {
                    Engine eng = engine;
                    RoutePlan rp = plan;
                    if (eng == null) {
                        continue;
                    }
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

    private void tick() {
        main.removeCallbacks(ticker);
        main.postDelayed(ticker, 1000);
    }

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
                go.execute(() -> {
                    stopEngine();
                    startSession();
                });
                return;
            }
        }
        tick();
    }

    private void watchPath() {
        ConnectivityManager cm = (ConnectivityManager) getSystemService(Context.CONNECTIVITY_SERVICE);
        if (cm == null) {
            return;
        }
        unwatchPath();
        seenPath = false;
        pathCb = new ConnectivityManager.NetworkCallback() {
            @Override
            public void onAvailable(Network network) {
                if (isVPN(cm, network)) {
                    return;
                }
                underlying = network;
                Live.underlying = label(cm, network);
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

    private static boolean isVPN(ConnectivityManager cm, Network n) {
        try {
            NetworkCapabilities c = cm.getNetworkCapabilities(n);
            return c != null && c.hasTransport(NetworkCapabilities.TRANSPORT_VPN);
        } catch (Exception e) {
            return false;
        }
    }

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

    private void stopSession() {
        main.removeCallbacks(ticker);
        unwatchPath();
        stopEngine();
        synchronized (io) {
            closeTunLocked();
        }
        running = false;
        Live.reset();
        Bus.post();
    }

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
        lastAddr = "";
    }

    private void updateNote() {
        NotificationManager nm = getSystemService(NotificationManager.class);
        if (nm != null) {
            nm.notify(NOTE, notice(noteText()));
        }
    }

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
        return b.length() == 0 ? getString(R.string.connected) : b.toString();
    }

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

    private static boolean fatalErr(String msg) {
        if (msg == null || msg.isEmpty()) {
            return false;
        }
        String s = msg.toLowerCase();
        return s.contains("auth_bad_key") || s.contains("auth_revoked")
                || s.contains("auth_expired") || s.contains("auth_not_authorized")
                || s.contains("auth_clock") || s.contains("auth_instance")
                || s.contains("net_disabled");
    }

    private static String nz(String s) {
        return s == null ? "" : s.trim();
    }

    static void requestStart(Context c) {
        Intent i = new Intent(c, LoftVpnService.class).setAction(ACTION_START);
        if (Build.VERSION.SDK_INT >= 26) {
            c.startForegroundService(i);
        } else {
            c.startService(i);
        }
    }

    static void requestStop(Context c) {
        c.startService(new Intent(c, LoftVpnService.class).setAction(ACTION_STOP));
    }
}
