/*
 * Android client, shared code: the live state of the current VPN session
 * (status, addresses, traffic counters and speed history). LoftVpnService
 * writes it; the phone and TV home screens, the notification and the TV
 * status read it after a Bus event.
 */
package app.loft.android;

/**
 * Current session state, shared by the VPN service and every screen.
 * The service writes it from engine Snapshots; screens read it after a Bus
 * event. Fields are volatile because writers and readers are on different
 * threads. state is "idle", "connecting", "on" or "error". holding is true
 * while the session waits on a network switch. extIP is the public address
 * seen through the tunnel.
 */
final class Live {
    static volatile String state = "idle";
    /** Last error text from the engine; "" when none. */
    static volatile String err = "";
    /** Tunnel IPv4 / IPv6 address the server gave this device. */
    static volatile String ip = "";
    static volatile String ip6 = "";
    /** Tunnel gateway address on the server side. */
    static volatile String gateway = "";
    /** Transport in use: a loft front name, or sstp / vless / trojan. */
    static volatile String kind = "";
    /** DNS servers pushed by the server, comma-separated. */
    static volatile String dns = "";
    /** This client's instance id as reported by the engine. */
    static volatile String instance = "";
    /** Label of the physical network carrying the tunnel (Wi-Fi, mobile...); "" when none. */
    static volatile String underlying = "";
    /** Name of the connected profile. */
    static volatile String name = "";
    static volatile String extIP = "";
    /** Total bytes sent / received in this session. */
    static volatile long up;
    static volatile long down;
    /** Tunnel MTU (largest packet size), 1280 until the engine reports one. */
    static volatile int mtu = 1280;
    static volatile boolean holding;
    /** Wall-clock time (ms) of the last rates() sample. */
    static volatile long lastAt;
    /** Wall-clock time (ms) when the session came up, for the uptime display. */
    static volatile long since;
    /** Current speed in bytes per second. */
    static volatile long rateDown;
    static volatile long rateUp;
    /** Speed history for the sparkline graph: download and upload, oldest first. */
    static final int SPARK_N = SparkView.N;
    static final float[] sparkDn = new float[SPARK_N];
    static final float[] sparkUp = new float[SPARK_N];
    /** How many history slots are filled (at most SPARK_N). */
    static volatile int sparkCount;

    /** True while the tunnel is up. */
    static boolean on() {
        return "on".equals(state);
    }

    /** True while a connection attempt is in progress. */
    static boolean connecting() {
        return "connecting".equals(state);
    }

    /**
     * Updates byte counters and derives bytes-per-second from the change since
     * the last call. Samples closer than 0.2 s apart are not used for the rate.
     * Each rate is also pushed into the sparkline history.
     */
    static void rates(long upNow, long downNow) {
        long now = System.currentTimeMillis();
        if (lastAt > 0 && now > lastAt) {
            double s = (now - lastAt) / 1000.0;
            if (s >= 0.2) {
                rateUp = (long) Math.max(0, (upNow - up) / s);
                rateDown = (long) Math.max(0, (downNow - down) / s);
                pushSpark(rateDown, rateUp);
            }
        }
        up = upNow;
        down = downNow;
        lastAt = now;
    }

    /** Clears everything back to the idle state, e.g. when the VPN stops. */
    static void reset() {
        state = "idle";
        err = "";
        ip = "";
        ip6 = "";
        gateway = "";
        kind = "";
        dns = "";
        instance = "";
        underlying = "";
        extIP = "";
        up = 0;
        down = 0;
        holding = false;
        rateUp = 0;
        rateDown = 0;
        lastAt = 0;
        since = 0;
        sparkCount = 0;
        java.util.Arrays.fill(sparkDn, 0);
        java.util.Arrays.fill(sparkUp, 0);
    }

    // Appends one sample to the fixed-size sparkline history, shifting out the oldest.
    static void pushSpark(long down, long upBps) {
        if (sparkCount < SPARK_N) {
            sparkDn[sparkCount] = down;
            sparkUp[sparkCount] = upBps;
            sparkCount++;
            return;
        }
        System.arraycopy(sparkDn, 1, sparkDn, 0, SPARK_N - 1);
        System.arraycopy(sparkUp, 1, sparkUp, 0, SPARK_N - 1);
        sparkDn[SPARK_N - 1] = down;
        sparkUp[SPARK_N - 1] = upBps;
    }
}
