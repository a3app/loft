/*
 * Android client, shared code: the Application class, created once per app
 * process before any screen or the VPN service. It holds the one shared
 * Profiles instance (the saved connections) that screens and LoftVpnService
 * reach through LoftApp.of(context).
 */
package app.loft.android;

import android.app.Application;

/**
 * Application object. Applies the chosen UI language, loads the saved
 * profiles once for the whole process, and starts the release channel
 * (the GitHub updater in the github flavor, nothing in the store flavor).
 */
public final class LoftApp extends Application {
    /** The process-wide connection list, loaded in onCreate(). */
    private Profiles profiles;

    @Override
    /** Process start: language first (so later strings are right), then profiles, then the update channel. */
    public void onCreate() {
        super.onCreate();
        Lang.apply(this);
        profiles = new Profiles(this);
        Channel.boot(this);
    }

    /** The shared connection list; loads it lazily in case onCreate() has not run yet. */
    public Profiles profiles() {
        if (profiles == null) {
            profiles = new Profiles(this);
        }
        return profiles;
    }

    /** Gets the LoftApp from any context (activity, service, application). */
    static LoftApp of(android.content.Context c) {
        return (LoftApp) c.getApplicationContext();
    }
}
