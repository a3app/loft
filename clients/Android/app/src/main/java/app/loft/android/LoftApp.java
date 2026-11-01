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
 * It is named in AndroidManifest.xml as the application class, so Android
 * creates it once per process. Screens and the VPN service run in the same
 * (single) process, so they share this one instance and its Profiles.
 */
public final class LoftApp extends Application {
    /** The process-wide connection list (the saved profiles), loaded in onCreate(). */
    private Profiles profiles;

    /**
     * Process start, on the main thread, before any activity or service.
     * Order matters: the crash handler goes first so a crash in the steps
     * after it is still saved; the language is set before any screen
     * inflates; Crash.drain runs last and does its work on a background
     * thread.
     */
    @Override
    public void onCreate() {
        super.onCreate();
        Crash.install(this);
        Lang.apply(this);
        profiles = new Profiles(this);
        Channel.boot(this);
        Crash.drain(this);
    }

    /**
     * The shared connection list; loads it lazily in case onCreate() has not
     * run yet. Not synchronized: in practice onCreate() always fills it first.
     */
    public Profiles profiles() {
        if (profiles == null) {
            profiles = new Profiles(this);
        }
        return profiles;
    }

    /** Gets the LoftApp from any context (activity, service, application) via its application context. */
    static LoftApp of(android.content.Context c) {
        return (LoftApp) c.getApplicationContext();
    }
}
