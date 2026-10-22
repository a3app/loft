package app.loft.android;

/** One-level YAML field reader for client documents Mobile.parseShare emits. */
final class Peek {
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
        if (yaml == null || key == null) {
            return "";
        }
        boolean in = false;
        String want = key + ":";
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
                t = t.substring(2).trim();
            }
            if (t.startsWith(want)) {
                return unquote(t.substring(want.length()).trim());
            }
        }
        return "";
    }

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
