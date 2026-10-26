package app.loft.android;

import android.app.Activity;
import android.app.Application;
import android.app.PendingIntent;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInfo;
import android.content.pm.PackageInstaller;
import android.content.pm.PackageManager;
import android.content.pm.Signature;
import android.content.pm.SigningInfo;
import android.net.ConnectivityManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.provider.Settings;
import android.view.View;
import android.widget.Button;
import android.widget.TextView;

import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.lang.ref.WeakReference;
import java.security.MessageDigest;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * GitHub self-update. Compiled only into the github flavor.
 * Install-unknown-apps is asked once; later sessions use
 * {@code USER_ACTION_NOT_REQUIRED} plus update-ownership.
 */
public final class Channel {
    // busy: one check or install at a time. awaitingPerm / wantInstall remember
    // that the user was sent to the "install unknown apps" settings screen and
    // what to resume when they come back. pending is the last release found.
    // The UI references are weak so a closed screen is not kept in memory.
    private static final long CHECK_EVERY_MS = 6L * 60 * 60 * 1000;
    private static final AtomicBoolean busy = new AtomicBoolean();
    private static final AtomicBoolean hooked = new AtomicBoolean();
    private static final Handler main = new Handler(Looper.getMainLooper());
    private static volatile boolean awaitingPerm;
    private static volatile boolean wantInstall;
    private static Gh.Rel pending;
    private static WeakReference<Button> uiBtn;
    private static WeakReference<TextView> uiStatus;
    private static WeakReference<Activity> uiAct;

    private Channel() {}

    /**
     * Called from LoftApp. Watches activity resumes (to continue after the
     * permission screen) and, in release builds that may install packages,
     * runs the background check.
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

    public static void attach(Activity a) {
        attach(a, a.getWindow().getDecorView());
    }

    /**
     * Binds the update button and status line on a settings or About screen,
     * when the layout has them, and adds a "GitHub" mark to the version text.
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

    // After the user grants "install unknown apps", continue what they asked
    // for: install the pending release, or check again.
    static void onResume(Activity a) {
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

    /** Update button: ask for the install permission, install a known newer release, or check. */
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
            startInstall(a.getApplicationContext(), pending, false);
            return;
        }
        startCheck(a.getApplicationContext(), false);
    }

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
     * unmetered network, so it never spends mobile data.
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

    private static void startCheck(Context ctx, boolean silent) {
        paint(ctx.getString(R.string.channel_checking), false);
        final boolean tv = "tv".equals(BuildConfig.FLAVOR_device);
        final String ua = "loft-android/" + current(ctx);
        new Thread(() -> {
            try {
                String json = Gh.get(Gh.API, ua, Gh.MAX_JSON);
                Gh.Rel rel = Gh.pick(json, tv);
                Prefs.p(ctx).edit().putLong("gh_checked", System.currentTimeMillis()).apply();
                main.post(() -> onCheck(ctx, rel, silent));
            } catch (Exception e) {
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
     * when it can (release build, permission granted, unmetered); otherwise the
     * button just offers it.
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
            if (BuildConfig.DEBUG || !canInstall(ctx) || isMetered(ctx)) {
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
     * not replace them.
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
        new Thread(() -> {
            try {
                String sums = Gh.get(rel.sumsUrl, ua, 64 * 1024);
                String want = Sums.hexFor(sums, rel.apkName);
                if (want == null) {
                    throw new IllegalStateException("sum");
                }
                try (FileOutputStream out = new FileOutputStream(apk)) {
                    String got = Gh.sha(rel.apkUrl, ua, out, Gh.MAX_APK);
                    if (!want.equals(got)) {
                        main.post(() -> done(ctx.getString(R.string.channel_bad_sum)));
                        return;
                    }
                }
                if (!samePackage(ctx, apk)) {
                    main.post(() -> done(ctx.getString(R.string.channel_bad_sign)));
                    return;
                }
                main.post(() -> {
                    paint(ctx.getString(R.string.channel_installing), false);
                    try {
                        commit(ctx, apk);
                        busy.set(false);
                    } catch (Exception e) {
                        done(ctx.getString(R.string.channel_fail));
                    }
                });
            } catch (Exception e) {
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
     * Writes the APK into a PackageInstaller session and commits it. On API 31+
     * the session asks for no user action, and on API 34+ for update ownership,
     * so after the first confirmed install later updates can go through
     * silently. The result arrives in GhInstall.
     */
    private static void commit(Context ctx, File apk) throws Exception {
        Context app = ctx.getApplicationContext();
        PackageInstaller pi = app.getPackageManager().getPackageInstaller();
        PackageInstaller.SessionParams p =
                new PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL);
        p.setAppPackageName(app.getPackageName());
        if (Build.VERSION.SDK_INT >= 31) {
            p.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED);
        }
        if (Build.VERSION.SDK_INT >= 34) {
            p.setRequestUpdateOwnership(true);
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
        Intent i = new Intent(app, GhInstall.class);
        i.addFlags(Intent.FLAG_RECEIVER_FOREGROUND);
        int flags = PendingIntent.FLAG_UPDATE_CURRENT;
        if (Build.VERSION.SDK_INT >= 31) {
            flags |= PendingIntent.FLAG_MUTABLE;
        }
        PendingIntent cb = PendingIntent.getBroadcast(app, id, i, flags);
        s.commit(cb.getIntentSender());
        s.close();
    }

    static boolean canInstall(Context ctx) {
        if (Build.VERSION.SDK_INT < 26) {
            return true;
        }
        return ctx.getPackageManager().canRequestPackageInstalls();
    }

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
        int flags = sigFlags();
        PackageInfo mine;
        PackageInfo got;
        try {
            mine = pm.getPackageInfo(ctx.getPackageName(), flags);
            String path = apk.getAbsolutePath();
            got = pm.getPackageArchiveInfo(path, flags);
            if (got != null && got.applicationInfo != null) {
                got.applicationInfo.sourceDir = path;
                got.applicationInfo.publicSourceDir = path;
            }
        } catch (Exception e) {
            return false;
        }
        if (got == null || got.packageName == null || !got.packageName.equals(ctx.getPackageName())) {
            return false;
        }
        byte[] a = firstCert(mine);
        byte[] b = firstCert(got);
        return a != null && b != null && MessageDigest.isEqual(a, b);
    }

    @SuppressWarnings("deprecation")
    private static int sigFlags() {
        if (Build.VERSION.SDK_INT >= 28) {
            return PackageManager.GET_SIGNING_CERTIFICATES;
        }
        return PackageManager.GET_SIGNATURES;
    }

    @SuppressWarnings("deprecation")
    private static byte[] firstCert(PackageInfo pi) {
        if (Build.VERSION.SDK_INT >= 28) {
            SigningInfo si = pi.signingInfo;
            if (si == null) {
                return null;
            }
            Signature[] s = si.hasMultipleSigners() ? si.getApkContentsSigners() : si.getSigningCertificateHistory();
            return s != null && s.length > 0 ? s[0].toByteArray() : null;
        }
        Signature[] s = pi.signatures;
        return s != null && s.length > 0 ? s[0].toByteArray() : null;
    }

    private static String current(Context c) {
        try {
            return c.getPackageManager().getPackageInfo(c.getPackageName(), 0).versionName;
        } catch (Exception e) {
            return BuildConfig.VERSION_NAME;
        }
    }

    private static String idleMsg(Context c) {
        if (!canInstall(c)) {
            return c.getString(R.string.channel_perm_d);
        }
        if (pending != null && pending.hasApk() && Ver.cmp(pending.ver, current(c)) > 0) {
            return c.getString(R.string.channel_available, pending.ver);
        }
        return c.getString(R.string.channel_update_d);
    }

    /** Updates the bound button label (allow / install / check) and the status text, if the screen is still open. */
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

    private static void done(String msg) {
        busy.set(false);
        paint(msg, true);
    }
}
