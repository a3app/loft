/*
 * Android client, "store" build flavor only: the no-op update channel. Builds
 * installed from an app store are updated by the store itself, so this class
 * keeps the same API as the github flavor's Channel.java (the GitHub
 * self-updater) and does nothing.
 */
package app.loft.android;

import android.app.Activity;
import android.app.Application;
import android.content.Context;
import android.view.View;

/** Store channel: no GitHub updater. The github flavor ships a different class. */
public final class Channel {
    private Channel() {}

    // Same API as the github flavor's Channel so shared code compiles in both.
    // Store builds are updated by the store, so every hook does nothing.
    /** Called at app start; nothing to set up in store builds. */
    public static void boot(Application app) {}

    /** Called when a screen opens; store builds show no update button. */
    public static void attach(Activity a) {}

    /** Same as {@link #attach(Activity)} for a given view tree; does nothing. */
    public static void attach(Activity a, View root) {}

    /** Store builds do not send crash reports (Play Vitals). Shared Crash.java still compiles. */
    static boolean sendCrash(Context ctx, String title, String body) {
        return true;
    }
}
