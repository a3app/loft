package app.loft.android;

import java.util.ArrayList;
import java.util.List;

import app.loft.engine.mobile.Mobile;

/** Paste / QR / file → profiles. Engine ParseShareAll is the detector. */
final class Importer {
    /**
     * Turns pasted text, a scanned QR or a file body into profiles. The engine
     * does the format detection: parseShareAll understands loft://, vless://,
     * trojan://, sstp:// links and YAML, and returns one YAML document per
     * connection, separated by "---". parseShare is the older single-link
     * parser, kept as a fallback. Documents with no key, password or URL are
     * skipped because they cannot connect.
     */
    static List<Profile> parse(String raw) {
        List<Profile> out = new ArrayList<>();
        if (raw == null) {
            return out;
        }
        String s = raw.trim();
        if (s.isEmpty()) {
            return out;
        }
        String all;
        try {
            all = Mobile.parseShareAll(s);
        } catch (Exception e) {
            try {
                all = Mobile.parseShare(s);
            } catch (Exception e2) {
                return out;
            }
        }
        if (all == null || all.trim().isEmpty()) {
            return out;
        }
        for (String doc : all.split("\n---\n")) {
            String y = doc.trim();
            if (y.isEmpty()) {
                continue;
            }
            Profile p = Profile.fromYaml(y);
            if (p.token.isEmpty() && p.pass.isEmpty() && p.url.isEmpty()) {
                continue;
            }
            out.add(p);
        }
        return out;
    }
}
