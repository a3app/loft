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
 * profiles once for the whole process, starts the release channel
 * (the GitHub updater in the github flavor, nothing in the store flavor),
 * and flushes a pending crash report if the last process died.
 */
public final class LoftApp extends Application {
    /** The process-wide connection list, loaded in onCreate(). */
    private Profiles profiles;

    @Override
    /** Process start: crash handler, language, profiles, update channel, then queued crash send. */
    public void onCreate() {
        super.onCreate();
        Crash.install(this);
        Lang.apply(this);
        profiles = new Profiles(this);
        Channel.boot(this);
        Crash.drain(this);
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
