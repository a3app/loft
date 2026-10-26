package app.loft.android;

import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Locale;

/** Ring of session lines. Tokens are not stored. */
final class Journal {
    private static final int MAX = 400;
    private static final List<String> LINES = new ArrayList<>();
    private static final SimpleDateFormat FMT = new SimpleDateFormat("HH:mm:ss", Locale.US);

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

    static synchronized List<String> all() {
        return new ArrayList<>(LINES);
    }

    static synchronized void clear() {
        LINES.clear();
        Bus.post();
    }

    static synchronized String text() {
        StringBuilder b = new StringBuilder();
        for (String l : LINES) {
            b.append(l).append('\n');
        }
        return b.toString();
    }

    // Hides member keys (lt_...) and the secret part of loft:// links so the log
    // can be copied and shared safely.
    private static String redact(String s) {
        return s.replaceAll("lt_[0-9a-fA-F]{8,}", "lt_…")
                .replaceAll("loft://[^\\s@]+@", "loft://…@");
    }
}
