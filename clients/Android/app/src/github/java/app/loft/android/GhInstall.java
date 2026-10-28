/*
 * Android client, "github" build flavor only: trampoline Activity for the
 * PackageInstaller session started by Channel.java. A BroadcastReceiver cannot
 * start the system confirm sheet on MIUI/HyperOS (background-activity rules);
 * an Activity can.
 */
package app.loft.android;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.os.Build;
import android.os.Bundle;

/**
 * PackageInstaller callback. First session may still raise a confirm sheet
 * (update-ownership). After that USER_ACTION_NOT_REQUIRED is silent.
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
        Channel.onInstall(this, st, in.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE));
        finish();
    }
}
