package app.loft.android;

/** SHA256SUMS lines: {@code <hex>  name} or {@code <hex> *name}. */
final class Sums {
    private Sums() {}

    /**
     * Returns the lower-case SHA-256 hex for {@code name} from a SHA256SUMS file,
     * or null when the file has no such line. The in-app updater uses it to check
     * a downloaded APK against the published checksum.
     */
    static String hexFor(String body, String name) {
        if (body == null || name == null || name.isEmpty()) {
            return null;
        }
        String[] lines = body.split("\n");
        for (String raw : lines) {
            String line = raw.trim();
            if (line.isEmpty() || line.charAt(0) == '#') {
                continue;
            }
            int sp = indexOfWs(line);
            if (sp <= 0) {
                continue;
            }
            String hex = line.substring(0, sp).trim();
            String rest = line.substring(sp).trim();
            if (rest.startsWith("*")) {
                rest = rest.substring(1);
            }
            if (name.equals(rest) && hex.length() == 64) {
                return hex.toLowerCase();
            }
        }
        return null;
    }

    private static int indexOfWs(String s) {
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            if (c == ' ' || c == '\t') {
                return i;
            }
        }
        return -1;
    }
}
