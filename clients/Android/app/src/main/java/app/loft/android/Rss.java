/*
 * Android client, shared code: reads how much memory this app process uses
 * (resident set size). The Go engine runs inside the same process, so this
 * number includes its heap too. Nothing in the app calls it at the moment.
 */
package app.loft.android;

import java.io.BufferedReader;
import java.io.FileReader;

/** Process RSS from /proc (Go heap included). Empty off Linux. */
final class Rss {
    /**
     * Returns the VmRSS value from /proc/self/status as written there (for
     * example "41236 kB"), or "" when the file cannot be read.
     */
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
