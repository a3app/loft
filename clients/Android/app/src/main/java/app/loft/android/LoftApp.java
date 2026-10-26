package app.loft.android;

import android.app.Application;

/**
 * Application object. Applies the chosen UI language, loads the saved
 * profiles once for the whole process, and starts the release channel
 * (the GitHub updater in the github flavor, nothing in the store flavor).
 */
public final class LoftApp extends Application {
    private Profiles profiles;

    @Override
    public void onCreate() {
        super.onCreate();
        Lang.apply(this);
        profiles = new Profiles(this);
        Channel.boot(this);
    }

    public Profiles profiles() {
        if (profiles == null) {
            profiles = new Profiles(this);
        }
        return profiles;
    }

    static LoftApp of(android.content.Context c) {
        return (LoftApp) c.getApplicationContext();
    }
}
