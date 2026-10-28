/*
 * Android client, shared code: the session log ("journal") shown on the log
 * screen. LoftVpnService adds most lines (other parts add errors, e.g. TV
 * pairing and route-list downloads); LogActivity shows, copies and shares
 * them. Kept in memory only and capped in size.
 */
package app.loft.android;

import java.text.SimpleDateFormat;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Locale;

/**
 * Ring of session lines. Tokens are not stored: every line passes through
 * redact() first. All methods that touch the lines are synchronized on the
 * class, so the VPN service's worker threads and the UI can use it at the
 * same time.
 *
 * <p>The log screen redraws on every Bus event, and most of those events are
 * not about the journal at all (traffic counters tick once a second). So the
 * journal keeps a {@link #version} counter that changes only when the lines
 * change, and {@link #snap()} tells the screen exactly which lines are new and
 * how many old ones fell off the front, so it can append instead of
 * re-setting all 400 lines each time.
 */
final class Journal {
    /** Maximum number of lines kept; older lines are dropped first. */
    private static final int MAX = 400;
    /**
     * The lines themselves, oldest first. An ArrayDeque, not an ArrayList:
     * dropping the oldest line from an ArrayList (remove(0)) shifts all 399
     * others one slot down, while a deque just moves its head. Only touched
     * while holding the class lock.
     */
    private static final ArrayDeque<String> LINES = new ArrayDeque<>(MAX + 1);
    /** Time prefix for each line. Only used under the class lock, so sharing one formatter is safe. */
    private static final SimpleDateFormat FMT = new SimpleDateFormat("HH:mm:ss", Locale.US);
    /**
     * How many lines were ever added since the app started (never goes down,
     * not even on clear()). Line number k (counting from 0) is the k-th line
     * ever added; the oldest line still kept is number {@code added - LINES.size()}.
     * Only touched under the class lock.
     */
    private static long added;
    /** Bumped by every clear(), so a screen can tell "cleared" from "trimmed". Under the class lock. */
    private static long clears;
    /**
     * Change counter: bumped by every add() and clear(). Volatile so a screen
     * can compare it with the value it last drew, without taking the lock,
     * and skip the redraw entirely when nothing changed.
     */
    static volatile long version;

    /**
     * Adds one time-stamped, redacted line, trims the oldest lines and
     * notifies screens. Empty input is ignored. Callable from any thread;
     * each call triggers one Bus event (one redraw of every open screen).
     */
    static void add(String s) {
        if (s == null || s.isEmpty()) {
            return;
        }
        // The two regex replacements are the slow part, and they only read
        // their own input, so they run before taking the lock: other threads
        // (and the log screen) are not kept waiting for them.
        String clean = redact(s);
        synchronized (Journal.class) {
            LINES.addLast(FMT.format(new Date()) + "  " + clean);
            added++;
            while (LINES.size() > MAX) {
                LINES.removeFirst();
            }
            version++;
        }
        // Bus.post() is cheap and thread-safe; no need to hold the lock for it.
        Bus.post();
    }

    /** A copy of all lines, oldest first. */
    static synchronized List<String> all() {
        return new ArrayList<>(LINES);
    }

    /** Removes every line and notifies screens. */
    static void clear() {
        synchronized (Journal.class) {
            LINES.clear();
            clears++;
            version++;
        }
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
     * A consistent picture of the journal for the log screen: all kept lines
     * plus the numbers needed to work out what changed since the last picture
     * (see {@link Snap}). Copying 400 references is cheap; the strings
     * themselves are shared, not copied.
     */
    static synchronized Snap snap() {
        return new Snap(version, clears, added - LINES.size(), new ArrayList<>(LINES));
    }

    /** One picture of the journal, taken by {@link #snap()}. */
    static final class Snap {
        /** Value of {@link Journal#version} when the picture was taken. */
        final long version;
        /** How many clear() calls had happened; a different value means "start over". */
        final long clears;
        /** Number (see {@code added}) of the first line in {@link #lines}. */
        final long first;
        /** The kept lines, oldest first, without the trailing newline. */
        final List<String> lines;

        Snap(long version, long clears, long first, List<String> lines) {
            this.version = version;
            this.clears = clears;
            this.first = first;
            this.lines = lines;
        }
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
