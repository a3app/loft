/*
 * Android client, "github" build flavor only: small HTTP helpers for GitHub
 * Releases. Picks the newest stable release with the right APK (phone or TV)
 * and its SHA256SUMS file, downloads small text bodies, and streams the APK
 * while hashing it. Used by Channel.java, which drives the update flow.
 */
package app.loft.android;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.regex.Pattern;

/** GitHub Releases for the github channel. Store builds do not compile this. */
final class Gh {
    // Size caps protect against a hostile or broken server streaming forever.
    // TAG accepts only plain release tags like v1.2.3 or v1.2.3-rc1.
    /** GitHub API URL listing the latest 20 releases of the loft repo. */
    static final String API = "https://api.github.com/repos/a3app/loft/releases?per_page=20";
    static final int MAX_JSON = 2 * 1024 * 1024;
    static final int MAX_APK = 128 * 1024 * 1024;
    private static final Pattern TAG = Pattern.compile(
            "^v\\d{1,4}\\.\\d{1,4}\\.\\d{1,6}(-[0-9A-Za-z.]{1,32})?$");

    private Gh() {}

    /** One release: its version and the URLs of the matching APK and checksum file (null when missing). */
    static final class Rel {
        final String tag;
        final String ver;
        final String apkName;
        final String apkUrl;
        final String sumsName;
        final String sumsUrl;

        /** Holds the fields as given; asset names and URLs may be null. */
        Rel(String tag, String ver, String apkName, String apkUrl, String sumsName, String sumsUrl) {
            this.tag = tag;
            this.ver = ver;
            this.apkName = apkName;
            this.apkUrl = apkUrl;
            this.sumsName = sumsName;
            this.sumsUrl = sumsUrl;
        }

        /** True when the release has both an APK for this device and a checksum file to verify it. */
        boolean hasApk() {
            return apkUrl != null && sumsUrl != null;
        }
    }

    /**
     * Picks the newest published (not draft, not pre-release) release from the
     * GitHub API list, with the APK for this device type (phone or TV) and the
     * SHA256SUMS file. Both unversioned and versioned asset names are accepted.
     */
    static Rel pick(String json, boolean tv) throws Exception {
        JSONArray rels = new JSONArray(json);
        Rel best = null;
        for (int i = 0; i < rels.length(); i++) {
            JSONObject o = rels.getJSONObject(i);
            if (o.optBoolean("draft") || o.optBoolean("prerelease")) {
                continue;
            }
            String tag = o.optString("tag_name", "");
            if (!TAG.matcher(tag).matches()) {
                continue;
            }
            String ver = tag.substring(1);
            JSONArray assets = o.optJSONArray("assets");
            String apkName = null, apkUrl = null, sumsName = null, sumsUrl = null;
            if (assets != null) {
                String apk1 = tv ? "loft-android-tv.apk" : "loft-android.apk";
                String apk2 = tv ? "loft-" + ver + "-android-tv.apk" : "loft-" + ver + "-android.apk";
                String sums1 = "loft-android-SHA256SUMS.txt";
                String sums2 = "loft-android-" + ver + "-SHA256SUMS.txt";
                for (int j = 0; j < assets.length(); j++) {
                    JSONObject a = assets.getJSONObject(j);
                    String n = a.optString("name", "");
                    String url = a.optString("browser_download_url", "");
                    if (n.equals(apk1) || n.equals(apk2)) {
                        apkName = n;
                        apkUrl = url;
                    } else if (n.equals(sums1) || n.equals(sums2)) {
                        sumsName = n;
                        sumsUrl = url;
                    }
                }
            }
            Rel r = new Rel(tag, ver, apkName, apkUrl, sumsName, sumsUrl);
            if (best == null || Ver.cmp(ver, best.ver) > 0) {
                best = r;
            }
        }
        return best;
    }

    /** Downloads a small text body (at most {@code max} bytes). */
    static String get(String url, String ua, int max) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        sha(url, ua, out, max);
        return out.toString(StandardCharsets.UTF_8.name());
    }

    /**
     * Streams a URL into {@code out} and returns the SHA-256 hex of what was
     * written, so the APK is hashed while it downloads. Fails above {@code max}
     * bytes.
     */
    static String sha(String url, String ua, OutputStream out, int max) throws IOException {
        MessageDigest md;
        try {
            md = MessageDigest.getInstance("SHA-256");
        } catch (Exception e) {
            throw new IOException(e);
        }
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setInstanceFollowRedirects(true);
        c.setConnectTimeout(8000);
        c.setReadTimeout(60000);
        c.setRequestProperty("User-Agent", ua);
        c.setRequestProperty("Accept", "application/vnd.github+json");
        try (InputStream in = c.getInputStream()) {
            byte[] buf = new byte[8192];
            int n, total = 0;
            while ((n = in.read(buf)) > 0) {
                total += n;
                if (total > max) {
                    throw new IOException("too large");
                }
                md.update(buf, 0, n);
                out.write(buf, 0, n);
            }
        } finally {
            c.disconnect();
        }
        return hex(md.digest());
    }

    /** Lowercase hex form of a digest, the same format as SHA256SUMS lines. */
    static String hex(byte[] d) {
        char[] out = new char[d.length * 2];
        for (int i = 0; i < d.length; i++) {
            int v = d[i] & 0xff;
            out[i * 2] = "0123456789abcdef".charAt(v >>> 4);
            out[i * 2 + 1] = "0123456789abcdef".charAt(v & 0xf);
        }
        return new String(out);
    }
}
