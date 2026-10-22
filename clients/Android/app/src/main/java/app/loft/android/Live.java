package app.loft.android;

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
    static volatile long rateDown;
    static volatile long rateUp;

    static boolean on() {
        return "on".equals(state);
    }

    static boolean connecting() {
        return "connecting".equals(state);
    }

    static void rates(long upNow, long downNow) {
        long now = System.currentTimeMillis();
        if (lastAt > 0 && now > lastAt) {
            double s = (now - lastAt) / 1000.0;
            if (s >= 0.2) {
                rateUp = (long) Math.max(0, (upNow - up) / s);
                rateDown = (long) Math.max(0, (downNow - down) / s);
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
    }
}
