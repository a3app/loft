/*
 * Android client, "github" build flavor only: receives the result of an APK
 * install started by Channel.java (the GitHub self-updater). PackageInstaller
 * reports back here through a broadcast once the install session finishes or
 * needs the user's confirmation.
 */
package app.loft.android;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageInstaller;
import android.os.Build;
import android.widget.Toast;

/**
 * PackageInstaller callback. First session may still raise a confirm sheet
 * (update-ownership). After that USER_ACTION_NOT_REQUIRED is silent.
 */
public final class GhInstall extends BroadcastReceiver {
    /**
     * Handles one install status. When the system needs the user to confirm,
     * it hands back an activity intent; start it. Any other non-success status
     * shows a short error.
     */
    @Override
    public void onReceive(Context c, Intent in) {
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
                c.startActivity(next);
            }
            return;
        }
        if (st != PackageInstaller.STATUS_SUCCESS) {
            Toast.makeText(c, R.string.channel_fail, Toast.LENGTH_SHORT).show();
        }
    }
}
