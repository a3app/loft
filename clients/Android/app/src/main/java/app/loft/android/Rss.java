package app.loft.android;

import java.io.BufferedReader;
import java.io.FileReader;

/** Process RSS from /proc (Go heap included). Empty off Linux. */
final class Rss {
    static String vm() {
        try (BufferedReader r = new BufferedReader(new FileReader("/proc/self/status"))) {
            String line;
            while ((line = r.readLine()) != null) {
                if (line.startsWith("VmRSS:")) {
                    return line.substring(6).trim();
                }
            }
        } catch (Exception ignored) {
        }
        return "";
    }
}
