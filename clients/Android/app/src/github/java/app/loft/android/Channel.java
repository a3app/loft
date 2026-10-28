/*
 * Android client, "github" build flavor only: the self-updater for APKs published
 * on GitHub Releases. It checks for a newer release, downloads and verifies the
 * APK, and installs it through PackageInstaller. The "store" flavor has its own
 * Channel.java with the same API that does nothing, so shared code compiles in
 * both. Gh.java does the HTTP and release picking; GhInstall receives the
 * installer result.
 */
package app.loft.android;

import android.app.Activity;
import android.app.ActivityOptions;
import android.app.AlertDialog;
import android.app.Application;
import android.app.PendingIntent;
import android.content.ClipData;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.content.pm.Signature;
import android.content.pm.SigningInfo;
import android.net.ConnectivityManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.View;
import android.widget.Button;
import android.widget.TextView;

import androidx.core.content.FileProvider;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.lang.ref.WeakReference;
import java.security.MessageDigest;
import java.util.Locale;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * GitHub self-update. Compiled only into the github flavor.
 * Install-unknown-apps is asked once. Silent PackageInstaller sessions run
 * only when this app already owns updates (API 34+). MIUI/HyperOS does not
 * honor USER_ACTION_NOT_REQUIRED for sideload and blocks a confirm sheet
 * started from a BroadcastReceiver, so those devices open the system
 * installer on the APK itself.
 *
 * <p>Threads: network and file work runs on short-lived "loft-gh" threads;
 * everything that touches views or the static state below is posted back to
 * the main thread through {@link #main}.
 */
public final class Channel {
    // busy: one check or install at a time. awaitingPerm / wantInstall remember
    // that the user was sent to the "install unknown apps" settings screen and
    // what to resume when they come back. pending is the last release found.
    // The UI references are weak so a closed screen is not kept in memory.
    /** Minimum gap between automatic background checks (6 hours). */
    private static final long CHECK_EVERY_MS = 6L * 60 * 60 * 1000;
    /** True while a check or install is running; set with compareAndSet so two taps cannot start two. */
    private static final AtomicBoolean busy = new AtomicBoolean();
    /** Guards {@link #boot} so the lifecycle callbacks are registered once. */
    private static final AtomicBoolean hooked = new AtomicBoolean();
    /** Main-thread handler: background threads post UI updates through it. */
    private static final Handler main = new Handler(Looper.getMainLooper());
    /** The user was sent to the "install unknown apps" screen; resume on return. */
    private static volatile boolean awaitingPerm;
    /** On return from that screen: true = install {@link #pending}, false = run a check. */
    private static volatile boolean wantInstall;
    /** Newest release found by the last check, or null. Main thread only. */
    private static Gh.Rel pending;
    /** Last verified APK on disk (cache or staged copy), reused for the system installer fallback. */
    private static File lastApk;
    /** Update button of the open settings/About screen, if any. */
    private static WeakReference<Button> uiBtn;
    /** Status line under the update button, if any. */
    private static WeakReference<TextView> uiStatus;
    /** The activity that owns {@link #uiBtn}; also used to start the installer from a foreground screen. */
    private static WeakReference<Activity> uiAct;
    /**
     * Watchdog posted 20 s after a PackageInstaller commit. Some devices never
     * call back (no sheet, no result); if we are still busy by then, open the
     * APK in the system installer directly, or give up with an error so the
     * button does not stay disabled forever.
     */
    private static final Runnable watch = () -> {
        if (!busy.get()) {
            return;
        }
        Journal.add("update no installer callback");
        Activity a = uiAct != null ? uiAct.get() : null;
        File apk = lastApk;
        if (a != null && !a.isFinishing() && apk != null && apk.isFile()) {
            try {
                openSheet(a, apk);
                busy.set(false);
                paint(a.getString(R.string.channel_confirm), true);
                return;
            } catch (Exception e) {
                Journal.add("update sheet " + e.getMessage());
            }
        }
        if (a != null) {
            done(a.getString(R.string.channel_fail));
        } else {
            busy.set(false);
        }
    };

    /** Static state and hooks only; not instantiated. */
    private Channel() {}

    /**
     * Called from LoftApp.onCreate on the main thread. Watches activity
     * resumes (to continue after the permission screen) and, in release
     * builds that may install packages, runs the background check. Debug
     * builds never auto-check: they are signed with a different key and could
     * not install a release anyway.
     */
    public static void boot(Application app) {
        if (!hooked.compareAndSet(false, true)) {
            return;
        }
        app.registerActivityLifecycleCallbacks(new Application.ActivityLifecycleCallbacks() {
            @Override public void onActivityCreated(Activity a, Bundle s) {}
            @Override public void onActivityStarted(Activity a) {}
            @Override public void onActivityResumed(Activity a) {
                Channel.onResume(a);
            }
            @Override public void onActivityPaused(Activity a) {}
            @Override public void onActivityStopped(Activity a) {}
            @Override public void onActivitySaveInstanceState(Activity a, Bundle s) {}
            @Override public void onActivityDestroyed(Activity a) {}
        });
        if (!BuildConfig.DEBUG && canInstall(app)) {
            auto(app);
        }
    }

    /** Binds the update controls found anywhere in the activity's window. */
    public static void attach(Activity a) {
        attach(a, a.getWindow().getDecorView());
    }

    /**
     * Binds the update button and status line on a settings or About screen,
     * when the layout has them, and adds a "GitHub" mark to the version text.
     * Called on the main thread each time such a screen is shown; the newest
     * screen replaces the previous binding. If the user just came back with
     * the install permission granted, the pending action resumes here.
     */
    public static void attach(Activity a, View root) {
        TextView ver = root.findViewById(R.id.ver);
        if (ver != null) {
            CharSequence t = ver.getText();
            String mark = a.getString(R.string.channel_github);
            if (t != null && !t.toString().contains(mark)) {
                ver.setText(t + " · " + mark);
            }
        }
        Button btn = root.findViewById(R.id.btn_channel);
        TextView status = root.findViewById(R.id.channel_status);
        if (btn == null) {
            return;
        }
        uiBtn = new WeakReference<>(btn);
        uiStatus = new WeakReference<>(status);
        uiAct = new WeakReference<>(a);
        btn.setOnClickListener(v -> tap(a));
        paint(idleMsg(a), true);
        if (awaitingPerm && canInstall(a)) {
            onResume(a);
        }
    }

    /**
     * Runs whenever any activity resumes. After the user grants "install
     * unknown apps", continue what they asked for: install the pending
     * release, or check again. Otherwise just refresh the idle status text.
     * Main thread (ActivityLifecycleCallbacks).
     */
    static void onResume(Activity a) {
        // The invisible installer trampoline is not a real screen; ignore it.
        if (a instanceof GhInstall) {
            return;
        }
        Activity cur = uiAct != null ? uiAct.get() : null;
        if (awaitingPerm && canInstall(a)) {
            awaitingPerm = false;
            if (busy.compareAndSet(false, true)) {
                if (wantInstall && pending != null && pending.hasApk()) {
                    startInstall(a.getApplicationContext(), pending, false);
                } else {
                    startCheck(a.getApplicationContext(), false);
                }
                return;
            }
        }
        if (a == cur && !busy.get()) {
            paint(idleMsg(a), true);
        }
    }

    /**
     * Update button: ask for the install permission, install a known newer
     * release, or check. Ignored while another check or install is running.
     */
    private static void tap(Activity a) {
        if (!canInstall(a)) {
            askInstall(a);
            return;
        }
        if (!busy.compareAndSet(false, true)) {
            return;
        }
        if (pending != null && pending.hasApk() && Ver.cmp(pending.ver, current(a)) > 0) {
            if (BuildConfig.DEBUG) {
                done(a.getString(R.string.channel_debug));
                return;
            }
            // HyperOS drops startActivity from a download callback. A second
            // tap is a user gesture and can open the already-downloaded APK.
            File apk = lastApk;
            if (apk != null && apk.isFile() && apk.length() > 0) {
                try {
                    openSheet(a, apk);
                    busy.set(false);
                    paint(a.getString(R.string.channel_confirm), true);
                } catch (Exception e) {
                    Journal.add("update sheet " + e.getMessage());
                    startInstall(a.getApplicationContext(), pending, false);
                }
                return;
            }
            startInstall(a.getApplicationContext(), pending, false);
            return;
        }
        startCheck(a.getApplicationContext(), false);
    }

    /**
     * Sends the user to the system "install unknown apps" screen for this app
     * and remembers whether to install or just check when they come back.
     * Falls back to the general app list on devices without that screen.
     */
    private static void askInstall(Activity a) {
        awaitingPerm = true;
        wantInstall = pending != null && pending.hasApk() && Ver.cmp(pending.ver, current(a)) > 0;
        paint(a.getString(R.string.channel_perm), true);
        Intent i = new Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES);
        i.setData(Uri.parse("package:" + a.getPackageName()));
        try {
            a.startActivity(i);
        } catch (Exception e) {
            try {
                a.startActivity(new Intent(Settings.ACTION_MANAGE_APPLICATIONS_SETTINGS));
            } catch (Exception ignored) {
                awaitingPerm = false;
                paint(a.getString(R.string.channel_fail), true);
            }
        }
    }

    /**
     * Background check at app start, at most once every 6 hours and only on an
     * unmetered network, so it never spends mobile data. The time of the last
     * check is kept in Prefs ("gh_checked") so the limit survives restarts.
     */
    private static void auto(Context ctx) {
        long last = Prefs.p(ctx).getLong("gh_checked", 0);
        if (System.currentTimeMillis() - last < CHECK_EVERY_MS) {
            return;
        }
        if (isMetered(ctx)) {
            return;
        }
        if (!busy.compareAndSet(false, true)) {
            return;
        }
        startCheck(ctx.getApplicationContext(), true);
    }

    /**
     * Fetches the release list on a background thread and passes the best
     * match to {@link #onCheck}. A silent (background) check shows no error
     * on failure. The caller must already hold {@code busy}.
     */
    private static void startCheck(Context ctx, boolean silent) {
        paint(ctx.getString(R.string.channel_checking), false);
        // The TV build wants the -tv APK; the phone build the plain one.
        final boolean tv = "tv".equals(BuildConfig.FLAVOR_device);
        final String ua = "loft-android/" + current(ctx);
        new Thread(() -> {
            try {
                String json = Gh.get(Gh.API, ua, Gh.MAX_JSON);
                Gh.Rel rel = Gh.pick(json, tv);
                Prefs.p(ctx).edit().putLong("gh_checked", System.currentTimeMillis()).apply();
                main.post(() -> onCheck(ctx, rel, silent));
            } catch (Exception e) {
                Journal.add("update check " + e.getMessage());
                main.post(() -> {
                    pending = null;
                    if (silent) {
                        busy.set(false);
                    } else {
                        done(ctx.getString(R.string.channel_fail));
                    }
                });
            }
        }, "loft-gh").start();
    }

    /**
     * Result of a check. A background check installs a newer release right away
     * when it can (release build, permission granted, unmetered, not MIUI);
     * otherwise the button just offers it. Runs on the main thread.
     */
    private static void onCheck(Context ctx, Gh.Rel rel, boolean silent) {
        pending = rel;
        if (rel == null) {
            if (silent) {
                busy.set(false);
            } else {
                done(ctx.getString(R.string.channel_fail));
            }
            return;
        }
        if (Ver.cmp(rel.ver, current(ctx)) <= 0) {
            done(ctx.getString(R.string.channel_current));
            return;
        }
        if (!rel.hasApk()) {
            done(ctx.getString(R.string.channel_none));
            return;
        }
        if (silent) {
            if (BuildConfig.DEBUG || !canInstall(ctx) || isMetered(ctx) || oemSheet(ctx)) {
                busy.set(false);
                paint(ctx.getString(R.string.channel_available, rel.ver), true);
                return;
            }
            startInstall(ctx, rel, true);
            return;
        }
        paint(ctx.getString(R.string.channel_available, rel.ver), true);
        busy.set(false);
    }

    /**
     * Downloads the checksum file and the APK, verifies the APK's SHA-256
     * against the published sum, checks it is the same package signed with the
     * same certificate, and then hands it to the system installer. Debug builds
     * stop early: they are signed with a different key, so a release APK could
     * not replace them. Download and checks run on a "loft-gh" thread; the
     * hand-off to the installer is posted to the main thread. The caller must
     * already hold {@code busy}.
     */
    private static void startInstall(Context ctx, Gh.Rel rel, boolean silent) {
        if (BuildConfig.DEBUG) {
            done(ctx.getString(R.string.channel_debug));
            return;
        }
        if (!canInstall(ctx)) {
            busy.set(false);
            Activity a = uiAct != null ? uiAct.get() : null;
            if (a != null) {
                askInstall(a);
            } else {
                paint(ctx.getString(R.string.channel_perm), true);
            }
            return;
        }
        paint(ctx.getString(R.string.channel_downloading), false);
        final String ua = "loft-android/" + current(ctx);
        final File apk = new File(ctx.getCacheDir(), "loft-update.apk");
        lastApk = apk;
        new Thread(() -> {
            try {
                // 1. Get the expected hash for this APK from the checksum file.
                String sums = Gh.get(rel.sumsUrl, ua, 64 * 1024);
                String want = Sums.hexFor(sums, rel.apkName);
                if (want == null) {
                    throw new IllegalStateException("sum");
                }
                // 2. Download the APK into the cache dir, hashing as it streams.
                try (FileOutputStream out = new FileOutputStream(apk)) {
                    String got = Gh.sha(rel.apkUrl, ua, out, Gh.MAX_APK);
                    if (!want.equals(got)) {
                        main.post(() -> done(ctx.getString(R.string.channel_bad_sum)));
                        return;
                    }
                }
                // 3. Refuse an APK that is not this app signed with our key.
                if (!samePackage(ctx, apk)) {
                    main.post(() -> done(ctx.getString(R.string.channel_bad_sign)));
                    return;
                }
                // 4. Hand it to the system installer on the main thread.
                // Xiaomi: copy out of cache (MIUI cannot read cache FileProvider
                // URIs) and prompt from a dialog click — Handler.post startActivity
                // is dropped as a background start on HyperOS.
                final File shown = oemSheet(ctx) ? stageApk(ctx, apk) : apk;
                lastApk = shown;
                main.post(() -> {
                    paint(ctx.getString(R.string.channel_installing), false);
                    try {
                        // Xiaomi path: ask from a dialog when a screen is
                        // open, else try the installer directly.
                        if (oemSheet(ctx)) {
                            Activity a = uiAct != null ? uiAct.get() : null;
                            if (a != null && !a.isFinishing()) {
                                promptSheet(a, shown);
                                return;
                            }
                            openSheet(ctx, shown);
                            busy.set(false);
                            paint(ctx.getString(R.string.channel_confirm), true);
                            return;
                        }
                        // AOSP path: PackageInstaller session, plus a
                        // watchdog in case no callback ever arrives.
                        commit(ctx, shown);
                        main.removeCallbacks(watch);
                        main.postDelayed(watch, 20_000);
                    } catch (Exception e) {
                        // Session failed to start: fall back to opening the
                        // APK in the system installer.
                        Journal.add("update commit " + e.getMessage());
                        try {
                            Activity a = uiAct != null ? uiAct.get() : null;
                            openSheet(a != null ? a : ctx, shown);
                            busy.set(false);
                            paint(ctx.getString(R.string.channel_confirm), true);
                        } catch (Exception e2) {
                            Journal.add("update sheet " + e2.getMessage());
                            done(ctx.getString(R.string.channel_fail));
                        }
                    }
                });
            } catch (Exception e) {
                Journal.add("update " + e.getMessage());
                main.post(() -> {
                    if (silent) {
                        busy.set(false);
                    } else {
                        done(ctx.getString(R.string.channel_fail));
                    }
                });
            }
        }, "loft-gh").start();
    }

    /**
     * PackageInstaller result from {@link GhInstall}. Keep {@code busy} until
     * this arrives so the row does not look idle while the system sheet is up.
     * Any failure other than "user cancelled" falls back to opening the APK
     * in the system installer. Main thread.
     */
    static void onInstall(Context ctx, int st, String msg) {
        main.removeCallbacks(watch);
        Journal.add("update status " + st + (msg == null || msg.isEmpty() ? "" : " " + msg));
        if (st == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            paint(ctx.getString(R.string.channel_confirm), false);
            return;
        }
        if (st == PackageInstaller.STATUS_SUCCESS) {
            done(ctx.getString(R.string.channel_current));
            return;
        }
        if (st == PackageInstaller.STATUS_FAILURE_ABORTED) {
            done(ctx.getString(R.string.channel_cancel));
            return;
        }
        File apk = lastApk;
        Activity a = uiAct != null ? uiAct.get() : null;
        if (apk != null && apk.isFile()) {
            try {
                openSheet(a != null && !a.isFinishing() ? a : ctx, apk);
                busy.set(false);
                paint(ctx.getString(R.string.channel_confirm), true);
                return;
            } catch (Exception e) {
                Journal.add("update sheet " + e.getMessage());
            }
        }
        done(ctx.getString(R.string.channel_fail));
    }

    /**
     * Writes the APK into a PackageInstaller session and commits it. Silent
     * install is requested only when this app already owns updates (API 34+);
     * otherwise the system must show a confirm sheet. The result arrives in
     * {@link GhInstall}. Note: it is called from a main.post() block, so the
     * whole APK copy into the session and the fsync run on the main thread.
     */
    private static void commit(Context ctx, File apk) throws Exception {
        Context app = ctx.getApplicationContext();
        PackageInstaller pi = app.getPackageManager().getPackageInstaller();
        PackageInstaller.SessionParams p =
                new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
        p.setAppPackageName(app.getPackageName());
        // Android 14+: ask to become the "update owner", so later updates
        // from this app may skip the confirm sheet.
        if (Build.VERSION.SDK_INT >= 34) {
            p.setRequestUpdateOwnership(true);
        }
        if (Build.VERSION.SDK_INT >= 31 && ownsUpdates(app)) {
            p.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED);
        }
        p.setSize(apk.length());
        int id = pi.createSession(p);
        PackageInstaller.Session s = pi.openSession(id);
        try (InputStream in = new FileInputStream(apk);
             OutputStream out = s.openWrite("loft.apk", 0, apk.length())) {
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
            }
            s.fsync(out);
        }
        // The result comes back as an Activity start (GhInstall), not a
        // broadcast: see GhInstall for why. FLAG_MUTABLE (API 31+) lets the
        // system add the status extras; on API 34+ the background-start
        // option lets that activity start even though the app is not in
        // front at that moment.
        Intent i = new Intent(app, GhInstall.class);
        int flags = PendingIntent.FLAG_UPDATE_CURRENT;
        if (Build.VERSION.SDK_INT >= 31) {
            flags |= PendingIntent.FLAG_MUTABLE;
        }
        PendingIntent cb;
        if (Build.VERSION.SDK_INT >= 34) {
            ActivityOptions opts = ActivityOptions.makeBasic();
            opts.setPendingIntentBackgroundActivityStartMode(
                    ActivityOptions.MODE_BACKGROUND_ACTIVITY_START_ALLOWED);
            cb = PendingIntent.getActivity(app, id, i, flags, opts.toBundle());
        } else {
            cb = PendingIntent.getActivity(app, id, i, flags);
        }
        s.commit(cb.getIntentSender());
        s.close();
    }

    /**
     * MIUI/HyperOS dialog whose positive button is a user gesture, so the
     * system installer is allowed to start. A Handler.post startActivity after
     * the download is treated as a background start and dropped. If the dialog
     * cannot be shown, it tries the installer directly.
     */
    private static void promptSheet(Activity a, File apk) {
        Journal.add("update oem " + Build.MANUFACTURER + "/" + Build.BRAND + "/" + Build.MODEL);
        busy.set(false);
        paint(a.getString(R.string.channel_ready), true);
        try {
            String msg = pending != null
                    ? a.getString(R.string.channel_available, pending.ver)
                    : a.getString(R.string.channel_ready);
            new AlertDialog.Builder(a)
                    .setMessage(msg)
                    .setPositiveButton(R.string.channel_install, (d, w) -> {
                        try {
                            openSheet(a, apk);
                            paint(a.getString(R.string.channel_confirm), true);
                        } catch (Exception e) {
                            Journal.add("update sheet " + e.getMessage());
                            done(a.getString(R.string.channel_fail));
                        }
                    })
                    .setNegativeButton(android.R.string.cancel, (d, w) ->
                            paint(a.getString(R.string.channel_ready), true))
                    .show();
        } catch (Exception e) {
            Journal.add("update prompt " + e.getMessage());
            try {
                openSheet(a, apk);
                paint(a.getString(R.string.channel_confirm), true);
            } catch (Exception e2) {
                Journal.add("update sheet " + e2.getMessage());
                done(a.getString(R.string.channel_fail));
            }
        }
    }

    /**
     * Copy the APK to app-specific external storage. MIUI's installer often
     * cannot read a FileProvider URI that points at getCacheDir(). Returns
     * the copy, or the original file if copying fails or sizes differ.
     * Runs on the download thread.
     */
    private static File stageApk(Context ctx, File apk) {
        File dir = ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS);
        if (dir == null) {
            dir = ctx.getExternalCacheDir();
        }
        if (dir == null) {
            return apk;
        }
        if (!dir.exists() && !dir.mkdirs()) {
            return apk;
        }
        File dst = new File(dir, "loft-update.apk");
        if (dst.getAbsolutePath().equals(apk.getAbsolutePath())) {
            return apk;
        }
        try (InputStream in = new FileInputStream(apk);
             OutputStream out = new FileOutputStream(dst)) {
            byte[] buf = new byte[8192];
            int n;
            while ((n = in.read(buf)) > 0) {
                out.write(buf, 0, n);
            }
        } catch (Exception e) {
            Journal.add("update stage " + e.getMessage());
            return apk;
        }
        if (dst.length() != apk.length()) {
            Journal.add("update stage size");
            return apk;
        }
        return dst;
    }

    /**
     * Opens the downloaded APK in the system installer. Used on MIUI/HyperOS,
     * where PackageInstaller sessions for sideload often finish with no sheet
     * and no callback, and as a fallback when a session fails.
     * The URI comes from the FileProvider declared in the github manifest
     * ("{package}.update"). Read access is granted up front to the known
     * installer packages and to every app that can open an APK, so whichever
     * installer ends up handling it can read the file. Throws if no activity
     * can open it.
     */
    @SuppressWarnings("deprecation")
    private static void openSheet(Context ctx, File apk) {
        Uri uri = FileProvider.getUriForFile(ctx, ctx.getPackageName() + ".update", apk);
        int grant = Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION;
        Intent i = new Intent(Intent.ACTION_VIEW);
        i.setDataAndType(uri, "application/vnd.android.package-archive");
        i.putExtra(Intent.EXTRA_NOT_UNKNOWN_SOURCE, true);
        i.putExtra("android.intent.extra.INSTALLER_PACKAGE_NAME", ctx.getPackageName());
        i.setClipData(ClipData.newRawUri("", uri));
        i.addFlags(grant);
        // Starting from a non-activity context (or a closing screen) needs a new task.
        if (!(ctx instanceof Activity) || ((Activity) ctx).isFinishing()) {
            i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
        }
        String[] installers = {
                "com.miui.packageinstaller",
                "com.miui.global.packageinstaller",
                "com.google.android.packageinstaller",
                "com.android.packageinstaller",
        };
        for (String pkg : installers) {
            try {
                ctx.grantUriPermission(pkg, uri, grant);
            } catch (Exception ignored) {
            }
        }
        try {
            for (ResolveInfo r : ctx.getPackageManager().queryIntentActivities(i, 0)) {
                if (r.activityInfo != null) {
                    ctx.grantUriPermission(r.activityInfo.packageName, uri, grant);
                }
            }
        } catch (Exception ignored) {
        }
        ctx.startActivity(i);
    }

    /**
     * True on Xiaomi/Redmi/POCO, whose installer does not run sideload
     * PackageInstaller sessions the AOSP way. Also true when MIUI/HyperOS
     * properties are set, even if manufacturer was rewritten.
     */
    static boolean oemSheet(Context ctx) {
        if (miui()) {
            return true;
        }
        String m = Build.MANUFACTURER == null ? "" : Build.MANUFACTURER.toLowerCase(Locale.US);
        String b = Build.BRAND == null ? "" : Build.BRAND.toLowerCase(Locale.US);
        return m.contains("xiaomi") || m.contains("redmi") || b.contains("xiaomi")
                || b.contains("redmi") || b.contains("poco");
    }

    /**
     * True when this build is MIUI or HyperOS (property present). Reads
     * system properties through reflection, because
     * android.os.SystemProperties is a hidden API with no public access.
     */
    private static boolean miui() {
        try {
            Class<?> c = Class.forName("android.os.SystemProperties");
            java.lang.reflect.Method get = c.getMethod("get", String.class, String.class);
            String[] keys = {
                    "ro.miui.ui.version.name",
                    "ro.mi.os.version.name",
                    "ro.hyperos.version.name",
            };
            for (String k : keys) {
                String v = (String) get.invoke(null, k, "");
                if (v != null && !v.isEmpty()) {
                    return true;
                }
            }
        } catch (Exception ignored) {
        }
        return false;
    }

    /** True when this app is the update owner (API 34+), so silent sessions are allowed. */
    private static boolean ownsUpdates(Context ctx) {
        if (Build.VERSION.SDK_INT < 34) {
            return false;
        }
        try {
            String owner = ctx.getPackageManager()
                    .getInstallSourceInfo(ctx.getPackageName())
                    .getUpdateOwnerPackageName();
            return ctx.getPackageName().equals(owner);
        } catch (Exception e) {
            return false;
        }
    }

    /**
     * True when this app may install packages. Before Android 8 (API 26) there
     * is no per-app permission, so the answer is always yes.
     */
    static boolean canInstall(Context ctx) {
        if (Build.VERSION.SDK_INT < 26) {
            return true;
        }
        return ctx.getPackageManager().canRequestPackageInstalls();
    }

    /** True when the current network is metered (for example mobile data). */
    private static boolean isMetered(Context ctx) {
        ConnectivityManager cm = (ConnectivityManager) ctx.getSystemService(Context.CONNECTIVITY_SERVICE);
        return cm != null && cm.isActiveNetworkMetered();
    }

    /**
     * True when the downloaded APK has our package name and the same first
     * signing certificate as the installed app. Catches a wrong or tampered
     * file before the installer is asked.
     */
    private static boolean samePackage(Context ctx, File apk) {
        PackageManager pm = ctx.getPackageManager();
        PackageInfo mine;
        PackageInfo got;
        try {
            mine = pm.getPackageInfo(ctx.getPackageName(), sigFlags());
            got = archiveInfo(pm, apk);
        } catch (Exception e) {
            Journal.add("update sign " + e.getMessage());
            return false;
        }
        if (got == null || got.packageName == null || !got.packageName.equals(ctx.getPackageName())) {
            Journal.add("update pkg " + (got == null ? "null" : got.packageName));
            return false;
        }
        byte[] a = firstCert(mine);
        byte[] b = firstCert(got);
        if (a == null || b == null) {
            Journal.add("update cert missing");
            return false;
        }
        return MessageDigest.isEqual(a, b);
    }

    /**
     * Package info of an on-disk APK, with sourceDir set so signing reads work.
     * If the modern signing flag yields no info or no certificate, it
     * retries with the legacy GET_SIGNATURES flag.
     */
    @SuppressWarnings("deprecation")
    private static PackageInfo archiveInfo(PackageManager pm, File apk) {
        String path = apk.getAbsolutePath();
        PackageInfo got = pm.getPackageArchiveInfo(path, sigFlags());
        if (got == null) {
            got = pm.getPackageArchiveInfo(path, PackageManager.GET_SIGNATURES);
        }
        if (got != null && got.applicationInfo != null) {
            got.applicationInfo.sourceDir = path;
            got.applicationInfo.publicSourceDir = path;
        }
        if (got != null && firstCert(got) == null) {
            PackageInfo legacy = pm.getPackageArchiveInfo(path, PackageManager.GET_SIGNATURES);
            if (legacy != null) {
                if (legacy.applicationInfo != null) {
                    legacy.applicationInfo.sourceDir = path;
                    legacy.applicationInfo.publicSourceDir = path;
                }
                return legacy;
            }
        }
        return got;
    }

    /** PackageManager flag that asks for signing certificates, old or new API. */
    @SuppressWarnings("deprecation")
    private static int sigFlags() {
        if (Build.VERSION.SDK_INT >= 28) {
            return PackageManager.GET_SIGNING_CERTIFICATES;
        }
        return PackageManager.GET_SIGNATURES;
    }

    /**
     * First signing certificate of a package, or null when none is known.
     * On API 28+ it reads the signing history (or the current signers when
     * there are several), so a rotated key still matches its original.
     */
    @SuppressWarnings("deprecation")
    private static byte[] firstCert(PackageInfo pi) {
        if (Build.VERSION.SDK_INT >= 28) {
            SigningInfo si = pi.signingInfo;
            if (si != null) {
                Signature[] s = si.hasMultipleSigners()
                        ? si.getApkContentsSigners() : si.getSigningCertificateHistory();
                if (s != null && s.length > 0) {
                    return s[0].toByteArray();
                }
            }
        }
        Signature[] s = pi.signatures;
        return s != null && s.length > 0 ? s[0].toByteArray() : null;
    }

    /** Installed version name (e.g. "1.2.3"), falling back to the build's own. Asks PackageManager on every call. */
    private static String current(Context c) {
        try {
            return c.getPackageManager().getPackageInfo(c.getPackageName(), 0).versionName;
        } catch (Exception e) {
            return BuildConfig.VERSION_NAME;
        }
    }

    /** Status text when nothing is running: permission needed, update available, or "check for updates". */
    private static String idleMsg(Context c) {
        if (!canInstall(c)) {
            return c.getString(R.string.channel_perm_d);
        }
        if (pending != null && pending.hasApk() && Ver.cmp(pending.ver, current(c)) > 0) {
            return c.getString(R.string.channel_available, pending.ver);
        }
        return c.getString(R.string.channel_update_d);
    }

    /**
     * Updates the bound button label (allow / install / check) and the status
     * text, if the screen is still open. Main thread only: it touches views.
     */
    private static void paint(String msg, boolean enable) {
        Activity a = uiAct != null ? uiAct.get() : null;
        Button btn = uiBtn != null ? uiBtn.get() : null;
        TextView status = uiStatus != null ? uiStatus.get() : null;
        if (a == null || a.isFinishing() || btn == null) {
            return;
        }
        btn.setEnabled(enable);
        if (!canInstall(a)) {
            btn.setText(R.string.channel_allow);
        } else if (pending != null && pending.hasApk() && Ver.cmp(pending.ver, current(a)) > 0) {
            btn.setText(R.string.channel_install);
        } else {
            btn.setText(R.string.channel_check);
        }
        if (status != null) {
            status.setText(msg);
        }
    }

    /** Ends the current check or install: frees {@code busy} and shows {@code msg}. */
    private static void done(String msg) {
        main.removeCallbacks(watch);
        busy.set(false);
        paint(msg, true);
    }

    /**
     * Posts a crash report as a GitHub issue. The token is a Dist secret baked
     * into this flavor ({@code BuildConfig.CRASH_TOKEN}); it must be
     * issues:write only. Empty token returns false so the file stays queued.
     * Blocks on the network; called from Crash's background thread.
     */
    static boolean sendCrash(Context ctx, String title, String body) {
        String tok = BuildConfig.CRASH_TOKEN;
        if (tok == null || tok.isEmpty()) {
            return false;
        }
        try {
            return Gh.postIssue(tok, "loft-android/" + current(ctx), title, body);
        } catch (Exception e) {
            return false;
        }
    }
}
