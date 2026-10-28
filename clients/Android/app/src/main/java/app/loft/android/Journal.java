/*
 * Android client, shared code: the session log ("journal") shown on the log
 * screen. LoftVpnService adds most lines (other parts add errors, e.g. TV
 * pairing and route-list downloads); LogActivity shows, copies and shares
 * them. Kept in memory only and capped in size.
 */
package app.loft.android;

import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Locale;

/**
 * Ring of session lines. Tokens are not stored: every line passes through
 * redact() first. All methods are static and synchronized on the class, so
 * the VPN service's worker threads and the UI can use it at the same time.
 */
final class Journal {
    /** Maximum number of lines kept; older lines are dropped first. */
    private static final int MAX = 400;
    /** The lines themselves, oldest first. Only touched while holding the class lock. */
    private static final List<String> LINES = new ArrayList<>();
    /** Time prefix for each line. Only used under the class lock, so sharing one formatter is safe. */
    private static final SimpleDateFormat FMT = new SimpleDateFormat("HH:mm:ss", Locale.US);

    /**
     * Adds one time-stamped, redacted line, trims the oldest lines and
     * notifies screens. Empty input is ignored. Callable from any thread;
     * each call triggers one Bus event (one redraw of every open screen).
     */
    static synchronized void add(String s) {
        if (s == null || s.isEmpty()) {
            return;
        }
        String line = FMT.format(new Date()) + "  " + redact(s);
        LINES.add(line);
        while (LINES.size() > MAX) {
            LINES.remove(0);
        }
        Bus.post();
    }

    /** A copy of all lines, oldest first. */
    static synchronized List<String> all() {
        return new ArrayList<>(LINES);
    }

    /** Removes every line and notifies screens. */
    static synchronized void clear() {
        LINES.clear();
        Bus.post();
    }

    /** All lines joined with newlines, for display, copy and share. */
    static synchronized String text() {
        StringBuilder b = new StringBuilder();
        for (String l : LINES) {
            b.append(l).append('\n');
        }
        return b.toString();
    }

    /**
     * Hides member keys (lt_ followed by 8+ hex digits) and the secret part
     * of loft:// links (everything before the '@') so the log can be copied
     * and shared safely. Crash reports (Crash) reuse this. Not synchronized:
     * it touches no shared state.
     */
    static String redact(String s) {
        return s.replaceAll("lt_[0-9a-fA-F]{8,}", "lt_…")
                .replaceAll("loft://[^\\s@]+@", "loft://…@");
    }
}
