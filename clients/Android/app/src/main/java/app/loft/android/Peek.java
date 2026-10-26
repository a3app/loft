package app.loft.android;

/** One-level YAML field reader for client documents Mobile.parseShare emits. */
final class Peek {
    /**
     * Value of a top-level key (first match, list items skipped). This is not a
     * YAML parser: it only reads the flat documents the engine itself writes,
     * which is enough to fill the UI.
     */
    static String get(String yaml, String key) {
        if (yaml == null || key == null) {
            return "";
        }
        String want = key + ":";
        for (String line : yaml.split("\n")) {
            String t = line.trim();
            if (t.startsWith("#") || t.startsWith("- ")) {
                continue;
            }
            if (t.startsWith(want)) {
                return unquote(t.substring(want.length()).trim());
            }
        }
        return "";
    }

    static String outbound(String yaml, String key) {
        return outboundSkipHost(yaml, key, "");
    }

    /** First outbound field, skipping items whose url host equals skipHost. */
    static String outboundSkipHost(String yaml, String key, String skipHost) {
        if (yaml == null || key == null) {
            return "";
        }
        String skip = skipHost == null ? "" : skipHost.trim().toLowerCase();
        boolean in = false;
        String want = key + ":";
        String itemUrl = "";
        String itemVal = "";
        boolean have = false;
        for (String line : yaml.split("\n")) {
            if (line.startsWith("outbounds:")) {
                in = true;
                continue;
            }
            if (in && !line.isEmpty() && !line.startsWith(" ") && !line.startsWith("\t")) {
                break;
            }
            if (!in) {
                continue;
            }
            String t = line.trim();
            if (t.startsWith("- ")) {
                String hit = finishItem(itemUrl, itemVal, have, skip);
                if (hit != null) {
                    return hit;
                }
                have = true;
                itemUrl = "";
                itemVal = "";
                t = t.substring(2).trim();
            }
            if (!have) {
                continue;
            }
            if (t.startsWith("url:")) {
                itemUrl = unquote(t.substring(4).trim());
                if ("url".equals(key)) {
                    itemVal = itemUrl;
                }
            } else if (t.startsWith(want)) {
                itemVal = unquote(t.substring(want.length()).trim());
            }
        }
        String hit = finishItem(itemUrl, itemVal, have, skip);
        return hit == null ? "" : hit;
    }

    /** null = skip this item. */
    private static String finishItem(String url, String val, boolean have, String skip) {
        if (!have) {
            return null;
        }
        if (!skip.isEmpty() && skip.equals(Profile.hostOf(url).toLowerCase())) {
            return null;
        }
        return val;
    }

    /** The fields of one outbound list item that the app cares about. */
    static final class Ob {
        String type = "";
        String url = "";
        String path = "";
        String name = "";
        String cdn = "";
        String maskPub = "";
        String fp = "";
    }

    /** Reads every item of the "outbounds:" list, in order. */
    static java.util.List<Ob> outbounds(String yaml) {
        java.util.ArrayList<Ob> out = new java.util.ArrayList<>();
        if (yaml == null) {
            return out;
        }
        boolean in = false;
        Ob cur = null;
        for (String line : yaml.split("\n")) {
            if (line.startsWith("outbounds:")) {
                in = true;
                continue;
            }
            if (in && !line.isEmpty() && !line.startsWith(" ") && !line.startsWith("\t")) {
                break;
            }
            if (!in) {
                continue;
            }
            String t = line.trim();
            if (t.startsWith("- ")) {
                if (cur != null) {
                    out.add(cur);
                }
                cur = new Ob();
                t = t.substring(2).trim();
            }
            if (cur == null) {
                continue;
            }
            int c = t.indexOf(':');
            if (c <= 0) {
                continue;
            }
            String k = t.substring(0, c).trim();
            String v = unquote(t.substring(c + 1).trim());
            switch (k) {
                case "type":
                    cur.type = v;
                    break;
                case "url":
                    cur.url = v;
                    break;
                case "path":
                    cur.path = v;
                    break;
                case "name":
                    cur.name = v;
                    break;
                case "cdn":
                    cur.cdn = v;
                    break;
                case "mask_pub":
                    cur.maskPub = v;
                    break;
                case "fingerprint":
                    cur.fp = v;
                    break;
                default:
                    break;
            }
        }
        if (cur != null) {
            out.add(cur);
        }
        return out;
    }

    /** Boolean field of the first outbound; {@code def} when absent. */
    static boolean outboundBool(String yaml, String key, boolean def) {
        String v = outbound(yaml, key).toLowerCase();
        if (v.isEmpty()) {
            return def;
        }
        return v.equals("true") || v.equals("yes") || v.equals("1");
    }

    static String unquote(String s) {
        if (s.length() >= 2) {
            char a = s.charAt(0);
            char b = s.charAt(s.length() - 1);
            if ((a == '"' && b == '"') || (a == '\'' && b == '\'')) {
                return s.substring(1, s.length() - 1);
            }
        }
        return s;
    }
}
