package app.loft.android;

/** Semver 1.2.3[-pre], same order as the panel updater. */
final class Ver {
    private Ver() {}

    static int cmp(String a, String b) {
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

    private static int parse(String s) {
        try {
            return Integer.parseInt(s);
        } catch (NumberFormatException e) {
            return 0;
        }
    }
}
