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
 *
 * <p>volatile makes each single field read see the latest write, but a
 * group of fields is not updated atomically: a screen may briefly see a new
 * ip next to an old state. That is fine for display, which is all this is
 * used for. Everything is static: there is only one VPN session per process.
 */
final class Live {
    /** Session status: "idle", "connecting", "on" or "error" (see on() / connecting()). */
    static volatile String state = "idle";
    /** Last error text from the engine; "" when none. */
    static volatile String err = "";
    /** Tunnel IPv4 address the server gave this device; "" before the first one. */
    static volatile String ip = "";
    /** Tunnel IPv6 address the server gave this device; "" when the node has no IPv6. */
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
    /**
     * Public address as seen from the internet through the tunnel. The home
     * screens look it up on demand (ExtIP) and store "…" while the lookup
     * runs and "—" when it failed.
     */
    static volatile String extIP = "";
    /** Total bytes sent in this session, as last reported by the engine. */
    static volatile long up;
    /** Total bytes received in this session, as last reported by the engine. */
    static volatile long down;
    /** Tunnel MTU (largest packet size), 1280 until the engine reports one. */
    static volatile int mtu = 1280;
    /**
     * True after the physical network changed or dropped, until the next
     * snapshot reports "on". The UI then shows "hold": the session is parked,
     * not lost, and will resume on the new network.
     */
    static volatile boolean holding;
    /** Wall-clock time (ms) of the last rates() sample. */
    static volatile long lastAt;
    /** Wall-clock time (ms) when the session came up, for the uptime display. */
    static volatile long since;
    /** Current download speed in bytes per second (see rates()). */
    static volatile long rateDown;
    /** Current upload speed in bytes per second (see rates()). */
    static volatile long rateUp;
    /** Number of samples in the sparkline history; taken from SparkView so the two always match. */
    static final int SPARK_N = SparkView.N;
    /**
     * Speed history for the sparkline graph (SparkView): download and upload
     * in bytes per second, oldest first. The arrays themselves are not
     * thread-safe; they are only meant for drawing, where a torn sample is
     * harmless.
     */
    static final float[] sparkDn = new float[SPARK_N];
    /** Upload half of the sparkline history; same layout as sparkDn. */
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
     * the last call. Samples closer than 0.2 s apart are not used for the rate
     * (dividing a few bytes by a tiny time step would show absurd spikes).
     * Each rate is also pushed into the sparkline history. Called by the VPN
     * service with every engine Snapshot, normally once a second.
     */
    static void rates(long upNow, long downNow) {
        long now = System.currentTimeMillis();
        // No rate on the very first sample, or if the clock went backwards.
        if (lastAt > 0 && now > lastAt) {
            double s = (now - lastAt) / 1000.0;
            if (s >= 0.2) {
                // max(0, ...) because the counters restart from zero with a new engine.
                rateUp = (long) Math.max(0, (upNow - up) / s);
                rateDown = (long) Math.max(0, (downNow - down) / s);
                pushSpark(rateDown, rateUp);
            }
        }
        up = upNow;
        down = downNow;
        lastAt = now;
    }

    /**
     * Clears everything back to the idle state, e.g. when the VPN stops.
     * The profile name (name) and mtu are kept: they are simply overwritten
     * by the next session.
     */
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

    /**
     * Appends one sample to the fixed-size sparkline history. Until the
     * history is full it fills the next free slot; after that it shifts every
     * sample one slot left (dropping the oldest) and writes the new one last.
     * The parameter is called upBps so it does not hide the {@code up} field.
     */
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
