package app.loft.android;

import android.app.Activity;
import android.app.Application;
import android.view.View;

/** Store channel: no GitHub updater. The github flavor ships a different class. */
public final class Channel {
    private Channel() {}

    public static void boot(Application app) {}

    public static void attach(Activity a) {}

    public static void attach(Activity a, View root) {}
}
