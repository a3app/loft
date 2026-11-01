/*
 * Android client, shared code: reads single values out of the YAML documents
 * the Go engine produces (for example from a shared link). Profile uses it
 * to fill its fields from imported YAML without pulling in a YAML library.
 */
package app.loft.android;

/**
 * One-level YAML field reader for the client documents that Mobile.parseShare
 * (the Go engine's parser, called through gomobile) emits. Reading strings
 * line by line keeps the APK small and is fast for these short documents.
 */
final class Peek {
    /**
     * Value of a key (first match, list items skipped). Lines are trimmed
     * before matching, so an indented key that is not on a "- " list line
     * matches too; in the engine's flat documents that is the top-level
     * value. This is not a YAML parser: it only reads the documents the
     * engine itself writes, which is enough to fill the UI.
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

    /** Value of {@code key} in the first item of the "outbounds:" list; "" when absent. */
    static String outbound(String yaml, String key) {
        return outboundSkipHost(yaml, key, "");
    }

    /**
     * {@code key} from the first outbound item whose url host is not
     * {@code skipHost} ("" skips nothing). If that item has no such key the
     * result is "" (the next item is not tried). An item ends at the next
     * "- " line, and the list ends at the first non-indented line.
     */
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
            // A new list item: decide about the previous one first, then
            // read the rest of this line as the new item's first field.
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

    /**
     * Result for a finished outbound item: its value, or null to move on to
     * the next item (no item read yet, or its host is the one to skip).
     */
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
        /** Outbound type as written by the engine (for example a front name). */
        String type = "";
        /** Server URL of this outbound. */
        String url = "";
        /** Path for the HTTP-based fronts. */
        String path = "";
        /** Display name of the outbound. */
        String name = "";
        /** Cover host the connection is sent through (CDN). */
        String cdn = "";
        /** The node's mask public key, learned after the first handshake. */
        String maskPub = "";
        /** TLS fingerprint to imitate. */
        String fp = "";
    }

    /**
     * Reads every item of the "outbounds:" list, in order. Only the keys
     * listed in Ob are kept; others are ignored. Each item's fields are
     * expected on "key: value" lines indented under its "- " line.
     */
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

    /** Boolean field of the first outbound ("true", "yes" or "1" mean true); {@code def} when absent. */
    static boolean outboundBool(String yaml, String key, boolean def) {
        String v = outbound(yaml, key).toLowerCase();
        if (v.isEmpty()) {
            return def;
        }
        return v.equals("true") || v.equals("yes") || v.equals("1");
    }

    /** Removes one pair of matching single or double quotes around a value. */
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
