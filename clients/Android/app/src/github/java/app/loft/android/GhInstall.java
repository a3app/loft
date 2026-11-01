/*
 * Android client, "github" build flavor only: trampoline Activity for the
 * PackageInstaller session started by Channel.java. A BroadcastReceiver cannot
 * start the system confirm sheet on MIUI/HyperOS (background-activity rules);
 * an Activity can. The manifest declares it translucent, not exported and
 * hidden from recents, so the user never sees it: it opens, forwards the
 * result and closes at once.
 */
package app.loft.android;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.os.Build;
import android.os.Bundle;

/**
 * PackageInstaller callback, started by the PendingIntent that
 * Channel.prepare() builds for the install session (committed later on the
 * main thread by Channel.Ready.commit()). The first session may
 * still raise a confirm sheet (the system asks once before this app becomes
 * the "update owner"). After that, sessions marked USER_ACTION_NOT_REQUIRED
 * install silently.
 */
public final class GhInstall extends Activity {
    /**
     * Reads the installer status. When the system needs a confirm sheet, starts
     * that intent from this activity (a user-visible task). Any other status
     * is handed to Channel so the Settings row can update.
     */
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        Intent in = getIntent();
        int st = in.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE);
        if (st == PackageInstaller.STATUS_PENDING_USER_ACTION) {
            // The system wants the user to confirm: it put the confirm
            // screen's intent in EXTRA_INTENT. The typed getter exists only
            // on Android 13 (API 33) and newer; older versions use the
            // deprecated untyped one.
            Intent next;
            if (Build.VERSION.SDK_INT >= 33) {
                next = in.getParcelableExtra(Intent.EXTRA_INTENT, Intent.class);
            } else {
                next = in.getParcelableExtra(Intent.EXTRA_INTENT);
            }
            if (next != null) {
                next.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                try {
                    startActivity(next);
                    Channel.onInstall(this, st, null);
                } catch (Exception e) {
                    Channel.onInstall(this, PackageInstaller.STATUS_FAILURE, e.getMessage());
                }
            } else {
                Channel.onInstall(this, PackageInstaller.STATUS_FAILURE, "no confirm");
            }
            finish();
            return;
        }
        // Final result (success, aborted, failure): just report it.
        Channel.onInstall(this, st, in.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE));
        finish();
    }
}
