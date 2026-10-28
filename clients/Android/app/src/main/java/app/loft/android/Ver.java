/*
 * Android client, shared code: compares version strings. The GitHub update
 * channel uses it to decide whether a published release is newer than the
 * installed app.
 */
package app.loft.android;

/**
 * Version strings of the form 1.2.3 or 1.2.3-pre (semantic versioning), in
 * the same order as the panel updater uses, so the app and the panel agree
 * on which release is newest.
 */
final class Ver {
    /** Static helpers only; not instantiated. */
    private Ver() {}

    /**
     * Compares two versions; returns -1, 0 or 1. A release sorts above its own
     * pre-release (1.2.3 &gt; 1.2.3-rc1); two pre-release tags compare as strings.
     * Missing or non-numeric parts count as 0. Only the first three numeric
     * parts are compared. Both arguments must be non-null.
     */
    static int cmp(String a, String b) {
        // Split "1.2.3-rc1" into the numeric core "1.2.3" and the tag "rc1".
        String an = a, ap = "";
        String bn = b, bp = "";
        int ai = a.indexOf('-');
        if (ai >= 0) {
            an = a.substring(0, ai);
            ap = a.substring(ai + 1);
        }
        int bi = b.indexOf('-');
        if (bi >= 0) {
            bn = b.substring(0, bi);
            bp = b.substring(bi + 1);
        }
        String[] as = an.split("\\.");
        String[] bs = bn.split("\\.");
        for (int i = 0; i < 3; i++) {
            int x = i < as.length ? parse(as[i]) : 0;
            int y = i < bs.length ? parse(bs[i]) : 0;
            if (x != y) {
                return x < y ? -1 : 1;
            }
        }
        // Same numbers: no tag beats a tag, otherwise compare the tags as text.
        if (ap.equals(bp)) {
            return 0;
        }
        if (ap.isEmpty()) {
            return 1;
        }
        if (bp.isEmpty()) {
            return -1;
        }
        return ap.compareTo(bp);
    }

    /** Parses one numeric part; anything that is not a number counts as 0. */
    private static int parse(String s) {
        try {
            return Integer.parseInt(s);
        } catch (NumberFormatException e) {
            return 0;
        }
    }
}
