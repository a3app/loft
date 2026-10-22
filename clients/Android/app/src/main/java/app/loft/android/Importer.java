package app.loft.android;

import java.util.ArrayList;
import java.util.List;

import app.loft.engine.Mobile;

/** Paste / QR / file → profiles. Engine ParseShareAll is the detector. */
final class Importer {
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
