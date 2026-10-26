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
    static volatile String err = "";
    static volatile String ip = "";
    static volatile String ip6 = "";
    static volatile String gateway = "";
    static volatile String kind = "";
    static volatile String dns = "";
    static volatile String instance = "";
    static volatile String underlying = "";
    static volatile String name = "";
    static volatile String extIP = "";
    static volatile long up;
    static volatile long down;
    static volatile int mtu = 1280;
    static volatile boolean holding;
    static volatile long lastAt;
    static volatile long since;
    static volatile long rateDown;
    static volatile long rateUp;
    static final int SPARK_N = SparkView.N;
    static final float[] sparkDn = new float[SPARK_N];
    static final float[] sparkUp = new float[SPARK_N];
    static volatile int sparkCount;

    static boolean on() {
        return "on".equals(state);
    }

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
