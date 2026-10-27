/*
 * Android client, shared code: crash capture. An uncaught Java exception is
 * written to filesDir/crash/ on the dying thread (no network). Native SIGSEGV /
 * SIGABRT and ANR are picked up from ApplicationExitInfo on the next start.
 * The github flavor then POSTs a GitHub issue; the store flavor compiles the
 * sender out. Keys are stripped the same way as the session journal.
 */
package app.loft.android;

import android.app.ActivityManager;
import android.app.ApplicationExitInfo;
import android.content.Context;
import android.os.Build;
import android.os.Process;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.PrintWriter;
import java.io.StringWriter;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Arrays;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.atomic.AtomicBoolean;

/** Process-local crash files, flushed to GitHub on the next start. */
final class Crash {
    static final int MAX_BODY = 24 * 1024;
    static final int MAX_PENDING = 3;
    static final int JOURNAL_TAIL = 40;
    private static final long DEDUP_MS = 24L * 60 * 60 * 1000;
    private static final long HARVEST_MS = 7L * 24 * 60 * 60 * 1000;
    private static final AtomicBoolean installed = new AtomicBoolean();

    private Crash() {}

    /**
     * Wraps the default uncaught handler so a Java crash is saved before the
     * process dies. GitHub-channel builds only; store builds use Play Vitals.
     */
    static void install(Context c) {
        if (!BuildConfig.GITHUB || !installed.compareAndSet(false, true)) {
            return;
        }
        final Context app = c.getApplicationContext();
        final Thread.UncaughtExceptionHandler prev = Thread.getDefaultUncaughtExceptionHandler();
        Thread.setDefaultUncaughtExceptionHandler((t, e) -> {
            try {
                if (Prefs.crashReports(app)) {
                    write(app, t, e);
                }
            } catch (Throwable ignored) {
            }
            if (prev != null) {
                prev.uncaughtException(t, e);
            } else {
                Process.killProcess(Process.myPid());
                System.exit(10);
            }
        });
    }

    /**
     * Next start: harvest native/ANR traces, then send queued files. Runs off
     * the main thread. No-op when the user turned reports off or this is a
     * store build.
     */
    static void drain(Context c) {
        if (!BuildConfig.GITHUB || !Prefs.crashReports(c)) {
            return;
        }
        final Context app = c.getApplicationContext();
        new Thread(() -> {
            harvest(app);
            sendPending(app);
        }, "loft-crash").start();
    }

    /** Redacts member keys, loft/vless/trojan/sstp userinfo, PATs and UUIDs. */
    static String redact(String s) {
        if (s == null || s.isEmpty()) {
            return "";
        }
        String r = Journal.redact(s);
        r = r.replaceAll("vless://[^\\s@]+@", "vless://…@");
        r = r.replaceAll("trojan://[^\\s@]+@", "trojan://…@");
        r = r.replaceAll("sstp://[^\\s@]+@", "sstp://…@");
        r = r.replaceAll("(?i)bearer [A-Za-z0-9._\\-]{8,}", "Bearer …");
        r = r.replaceAll("ghp_[A-Za-z0-9_]{20,}", "ghp_…");
        r = r.replaceAll("github_pat_[A-Za-z0-9_]{20,}", "github_pat_…");
        r = r.replaceAll("gho_[A-Za-z0-9_]{20,}", "gho_…");
        r = r.replaceAll(
                "[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}",
                "…uuid…");
        return r;
    }

    /** First 8 hex chars of SHA-256 over class/method/line frames, not the message. */
    static String fingerprint(Throwable e) {
        StringBuilder b = new StringBuilder();
        int n = 0;
        for (Throwable t = e; t != null && n < 5; t = t.getCause(), n++) {
            b.append(t.getClass().getName()).append('\n');
            StackTraceElement[] st = t.getStackTrace();
            if (st == null) {
                continue;
            }
            int k = Math.min(st.length, 20);
            for (int i = 0; i < k; i++) {
                StackTraceElement el = st[i];
                b.append(el.getClassName()).append('.')
                        .append(el.getMethodName()).append(':')
                        .append(el.getLineNumber()).append('\n');
            }
        }
        return sha8(b.toString());
    }

    /** {@code android crash: MainActivity.onCreate (a1b2c3d4)}. */
    static String title(Throwable e) {
        return "android crash: " + firstFrame(e) + " (" + fingerprint(e) + ")";
    }

    static String clip(String s, int max) {
        if (s == null) {
            return "";
        }
        if (s.length() <= max) {
            return s;
        }
        return s.substring(0, max) + "\n…\n";
    }

    /** Value of {@code key:} in the header block, or "". */
    static String field(String text, String key) {
        if (text == null || key == null) {
            return "";
        }
        String prefix = key + ":";
        int n = text.length();
        int i = 0;
        while (i < n) {
            int nl = text.indexOf('\n', i);
            if (nl < 0) {
                nl = n;
            }
            String line = text.substring(i, nl).trim();
            if (line.isEmpty()) {
                break;
            }
            if (line.startsWith(prefix)) {
                return line.substring(prefix.length()).trim();
            }
            i = nl + 1;
        }
        return "";
    }

    static String firstFrame(Throwable e) {
        for (Throwable t = e; t != null; t = t.getCause()) {
            StackTraceElement[] st = t.getStackTrace();
            if (st == null) {
                continue;
            }
            String fallback = null;
            for (StackTraceElement el : st) {
                String c = el.getClassName();
                if (c == null || skipFrame(c)) {
                    continue;
                }
                String s = shortName(c) + "." + el.getMethodName();
                if (c.startsWith("app.loft.")) {
                    return s;
                }
                if (fallback == null) {
                    fallback = s;
                }
            }
            if (fallback != null) {
                return fallback;
            }
        }
        String n = e.getClass().getSimpleName();
        return n.isEmpty() ? "crash" : n;
    }

    private static boolean skipFrame(String c) {
        return c.startsWith("java.lang.Thread")
                || c.startsWith("java.lang.Daemons")
                || c.startsWith("android.os.Looper")
                || c.startsWith("android.os.Handler")
                || c.startsWith("com.android.internal.os")
                || c.startsWith("dalvik.system.");
    }

    private static String shortName(String c) {
        int d = c.lastIndexOf('.');
        return d < 0 ? c : c.substring(d + 1);
    }

    private static void write(Context c, Thread t, Throwable e) {
        String fp = fingerprint(e);
        StringBuilder b = new StringBuilder();
        b.append(header(c, title(e), fp, "uncaught", t != null ? t.getName() : ""));
        b.append(stack(e));
        b.append("\njournal:\n");
        b.append(journalTail());
        save(c, fp, redact(b.toString()));
    }

    private static String stack(Throwable e) {
        StringWriter sw = new StringWriter();
        e.printStackTrace(new PrintWriter(sw));
        return sw.toString();
    }

    static String journalTail() {
        List<String> all = Journal.all();
        int n = all.size();
        int from = Math.max(0, n - JOURNAL_TAIL);
        StringBuilder b = new StringBuilder();
        for (int i = from; i < n; i++) {
            b.append(all.get(i)).append('\n');
        }
        return b.toString();
    }

    private static String header(Context c, String title, String fp, String reason, String thread) {
        StringBuilder b = new StringBuilder();
        b.append("title: ").append(title).append('\n');
        b.append("fingerprint: ").append(fp).append('\n');
        b.append("ver: ").append(BuildConfig.VERSION_NAME).append('\n');
        b.append("flavor: ").append(BuildConfig.FLAVOR).append('\n');
        b.append("sdk: ").append(Build.VERSION.SDK_INT).append('\n');
        b.append("release: ").append(Build.VERSION.RELEASE).append('\n');
        b.append("device: ").append(Build.MANUFACTURER).append(' ').append(Build.MODEL).append('\n');
        String abi = "";
        if (Build.SUPPORTED_ABIS != null && Build.SUPPORTED_ABIS.length > 0) {
            abi = Build.SUPPORTED_ABIS[0];
        }
        b.append("abi: ").append(abi).append('\n');
        b.append("thread: ").append(thread).append('\n');
        b.append("reason: ").append(reason).append('\n');
        b.append('\n');
        return b.toString();
    }

    private static File dir(Context c) {
        return new File(c.getFilesDir(), "crash");
    }

    private static void save(Context c, String fp, String body) {
        File d = dir(c);
        if (!d.isDirectory() && !d.mkdirs()) {
            return;
        }
        prune(d);
        File f = new File(d, System.currentTimeMillis() + "-" + fp + ".txt");
        byte[] bytes = clip(body, MAX_BODY).getBytes(StandardCharsets.UTF_8);
        try (FileOutputStream out = new FileOutputStream(f)) {
            out.write(bytes);
            out.getFD().sync();
        } catch (Exception ignored) {
            f.delete();
        }
    }

    private static void prune(File d) {
        File[] files = d.listFiles();
        if (files == null || files.length < MAX_PENDING) {
            return;
        }
        Arrays.sort(files);
        int extra = files.length - MAX_PENDING + 1;
        for (int i = 0; i < extra && i < files.length; i++) {
            files[i].delete();
        }
    }

    /**
     * Native / ANR / abort from the last week. Java uncaught is already in a
     * pending file from {@link #install}, so REASON_CRASH is skipped.
     */
    private static void harvest(Context c) {
        if (Build.VERSION.SDK_INT < 30) {
            return;
        }
        ActivityManager am = (ActivityManager) c.getSystemService(Context.ACTIVITY_SERVICE);
        if (am == null) {
            return;
        }
        List<ApplicationExitInfo> list;
        try {
            list = am.getHistoricalProcessExitReasons(null, 0, 8);
        } catch (Exception e) {
            return;
        }
        if (list == null || list.isEmpty()) {
            return;
        }
        long last = Prefs.p(c).getLong("crash_exit_at", 0);
        long newest = last;
        long floor = System.currentTimeMillis() - HARVEST_MS;
        for (ApplicationExitInfo info : list) {
            long ts = info.getTimestamp();
            if (ts <= last || ts < floor) {
                continue;
            }
            if (!interesting(info)) {
                continue;
            }
            writeExit(c, info);
            if (ts > newest) {
                newest = ts;
            }
        }
        if (newest > last) {
            Prefs.p(c).edit().putLong("crash_exit_at", newest).apply();
        }
    }

    private static boolean interesting(ApplicationExitInfo info) {
        if (Build.VERSION.SDK_INT < 30) {
            return false;
        }
        int r = info.getReason();
        if (r == ApplicationExitInfo.REASON_CRASH_NATIVE
                || r == ApplicationExitInfo.REASON_ANR) {
            return true;
        }
        if (r != ApplicationExitInfo.REASON_SIGNALED) {
            return false;
        }
        int sig = info.getStatus();
        return sig == 4 || sig == 6 || sig == 7 || sig == 8 || sig == 11;
    }

    private static void writeExit(Context c, ApplicationExitInfo info) {
        if (Build.VERSION.SDK_INT < 30) {
            return;
        }
        String reason = reasonName(info.getReason());
        String desc = info.getDescription() == null ? "" : info.getDescription();
        String trace = readTrace(info, 16 * 1024);
        String fp = sha8(reason + "\n" + desc + "\n" + clip(trace, 2048));
        String title = "android crash: " + reason;
        if (!desc.isEmpty()) {
            String one = desc.replace('\n', ' ');
            title = "android crash: " + clip(one, 80).trim();
        }
        title = title + " (" + fp + ")";
        StringBuilder b = new StringBuilder();
        b.append(header(c, title, fp, reason, ""));
        if (!desc.isEmpty()) {
            b.append(desc).append('\n');
        }
        if (!trace.isEmpty()) {
            b.append(trace).append('\n');
        }
        b.append("\njournal:\n");
        b.append(journalTail());
        save(c, fp, redact(b.toString()));
    }

    private static String reasonName(int r) {
        if (Build.VERSION.SDK_INT < 30) {
            return "exit";
        }
        if (r == ApplicationExitInfo.REASON_CRASH_NATIVE) {
            return "native";
        }
        if (r == ApplicationExitInfo.REASON_ANR) {
            return "anr";
        }
        if (r == ApplicationExitInfo.REASON_SIGNALED) {
            return "signal";
        }
        return "exit-" + r;
    }

    private static String readTrace(ApplicationExitInfo info, int max) {
        if (Build.VERSION.SDK_INT < 30) {
            return "";
        }
        try (InputStream in = info.getTraceInputStream()) {
            if (in == null) {
                return "";
            }
            ByteArrayOutputStream out = new ByteArrayOutputStream();
            byte[] buf = new byte[4096];
            int n, total = 0;
            while ((n = in.read(buf)) > 0) {
                int take = Math.min(n, max - total);
                if (take <= 0) {
                    break;
                }
                out.write(buf, 0, take);
                total += take;
            }
            return out.toString(StandardCharsets.UTF_8.name());
        } catch (Exception e) {
            return "";
        }
    }

    private static void sendPending(Context c) {
        File[] files = dir(c).listFiles();
        if (files == null || files.length == 0) {
            return;
        }
        Arrays.sort(files);
        Set<String> seen = new HashSet<>();
        for (File f : files) {
            String text;
            try {
                text = readFile(f);
            } catch (Exception e) {
                f.delete();
                continue;
            }
            text = redact(text);
            String fp = field(text, "fingerprint");
            if (fp.isEmpty()) {
                fp = sha8(text);
            }
            if (!seen.add(fp) || recentlySent(c, fp)) {
                f.delete();
                continue;
            }
            String title = field(text, "title");
            if (title.isEmpty()) {
                title = "android crash: " + fp;
            }
            boolean ok = Channel.sendCrash(c, clip(title, 200), clip(text, MAX_BODY));
            if (!ok) {
                break;
            }
            remember(c, fp);
            f.delete();
        }
    }

    private static boolean recentlySent(Context c, String fp) {
        String last = Prefs.p(c).getString("crash_last_fp", "");
        if (!fp.equals(last)) {
            return false;
        }
        return System.currentTimeMillis() - Prefs.p(c).getLong("crash_last_at", 0) < DEDUP_MS;
    }

    private static void remember(Context c, String fp) {
        Prefs.p(c).edit()
                .putString("crash_last_fp", fp)
                .putLong("crash_last_at", System.currentTimeMillis())
                .apply();
    }

    private static String readFile(File f) throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        try (FileInputStream in = new FileInputStream(f)) {
            byte[] buf = new byte[4096];
            int n, total = 0;
            while ((n = in.read(buf)) > 0) {
                total += n;
                if (total > MAX_BODY + 4096) {
                    break;
                }
                out.write(buf, 0, n);
            }
        }
        return out.toString(StandardCharsets.UTF_8.name());
    }

    static String sha8(String s) {
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            String hex = hex(md.digest(s.getBytes(StandardCharsets.UTF_8)));
            return hex.length() >= 8 ? hex.substring(0, 8) : hex;
        } catch (Exception e) {
            return "00000000";
        }
    }

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
