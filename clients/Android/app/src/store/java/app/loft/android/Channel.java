package app.loft.android;

import android.app.Activity;
import android.app.Application;
import android.view.View;

/** Store channel: no GitHub updater. The github flavor ships a different class. */
public final class Channel {
    private Channel() {}

    // Same API as the github flavor's Channel so shared code compiles in both.
    // Store builds are updated by the store, so every hook does nothing.
    public static void boot(Application app) {}

    public static void attach(Activity a) {}

    public static void attach(Activity a, View root) {}
}
