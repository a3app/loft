package app.loft.android;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.content.Intent;
import android.net.IpPrefix;
import android.net.VpnService;
import android.os.Build;
import android.os.ParcelFileDescriptor;

import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.net.InetAddress;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import app.loft.engine.Engine;
import app.loft.engine.Mobile;
import app.loft.engine.PacketSink;
import app.loft.engine.Snapshot;
import app.loft.engine.SocketProtector;

/**
 * Thin VpnService: pumps IP, installs routes/DNS from Snapshot.
 * Session is Go (clients/mobile). Java on purpose: gomobile already
 * emits Java; no Kotlin runtime in this process.
 */
public final class LoftVpnService extends VpnService implements PacketSink, SocketProtector {
    private static final String CH = "loft.vpn";
    private final ExecutorService go = Executors.newSingleThreadExecutor();
    private Engine engine;
    private ParcelFileDescriptor tun;
    private FileOutputStream out;

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        startForeground(1, notice());
        String body = SessionStore.yaml(this);
        go.execute(() -> startSession(body));
        return START_STICKY;
    }

    @Override
    public void onDestroy() {
        go.execute(() -> {
            if (engine != null) {
                engine.stop();
            }
            closeTun();
        });
        go.shutdown();
        super.onDestroy();
    }

    private void startSession(String body) {
        try {
            String yaml = body == null ? "" : body.trim();
            if (yaml.isEmpty()) {
                stopSelf();
                return;
            }
            if (!yaml.contains("\n") && yaml.toLowerCase().contains("://")) {
                yaml = Mobile.parseShare(yaml);
            }
            Engine eng = new Engine();
            eng.setProtector(this);
            eng.setSink(this);
            eng.start(yaml);
            engine = eng;
            Snapshot snap = eng.snapshot();
            tun = buildTun(snap);
            if (tun == null) {
                eng.stop();
                stopSelf();
                return;
            }
            out = new FileOutputStream(tun.getFileDescriptor());
            pumpReads(tun);
        } catch (Exception e) {
            stopSelf();
        }
    }

    private ParcelFileDescriptor buildTun(Snapshot snap) throws Exception {
        if (snap == null || snap.getIP() == null || snap.getIP().isEmpty()) {
            return null;
        }
        Builder b = new Builder()
                .setSession("loft")
                .setMtu(snap.getMTU() > 0 ? snap.getMTU() : 1280)
                .addAddress(snap.getIP(), 24)
                .addRoute("0.0.0.0", 0);
        String dns = snap.getDNS();
        if (dns != null && !dns.isEmpty()) {
            for (String s : dns.split(",")) {
                String ip = s.trim();
                if (!ip.isEmpty()) {
                    b.addDnsServer(ip);
                }
            }
        }
        String fronts = snap.getFrontIPs();
        if (fronts != null && !fronts.isEmpty() && Build.VERSION.SDK_INT >= 33) {
            for (String s : fronts.split(",")) {
                String ip = s.trim();
                if (ip.isEmpty() || !ip.contains(".")) {
                    continue;
                }
                b.excludeRoute(new IpPrefix(InetAddress.getByName(ip), 32));
            }
        }
        b.setBlocking(true);
        return b.establish();
    }

    private void pumpReads(ParcelFileDescriptor pfd) {
        go.execute(() -> {
            byte[] buf = new byte[2048];
            try (FileInputStream in = new FileInputStream(pfd.getFileDescriptor())) {
                int n;
                while ((n = in.read(buf)) > 0) {
                    byte[] pkt = new byte[n];
                    System.arraycopy(buf, 0, pkt, 0, n);
                    if (engine != null) {
                        engine.injectPacket(pkt);
                    }
                }
            } catch (Exception ignored) {
            }
        });
    }

    @Override
    public void onPacket(byte[] pkt) {
        if (pkt == null || out == null) {
            return;
        }
        try {
            out.write(pkt);
        } catch (Exception ignored) {
        }
    }

    @Override
    public boolean protect(long fd) {
        return protect((int) fd);
    }

    private Notification notice() {
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(CH, "loft", NotificationManager.IMPORTANCE_LOW);
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm != null) {
                nm.createNotificationChannel(ch);
            }
        }
        Notification.Builder b = Build.VERSION.SDK_INT >= 26
                ? new Notification.Builder(this, CH)
                : new Notification.Builder(this);
        return b.setContentTitle("loft")
                .setContentText("Connected")
                .setSmallIcon(android.R.drawable.ic_lock_lock)
                .setOngoing(true)
                .build();
    }

    private void closeTun() {
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
        engine = null;
    }
}
