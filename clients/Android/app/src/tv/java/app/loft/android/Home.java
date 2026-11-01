/*
 * Android client, TV build: tells shared code which activity is the app's
 * main screen. The phone build has its own Home.java returning its main
 * activity, so code in src/main (for example LoftVpnService's notification)
 * can open "the home screen" without knowing which UI flavor it runs in.
 */
package app.loft.android;

/** Launcher activity for the TV flavor. */
final class Home {
    /** The activity to open as the home screen: TvActivity on TV builds. */
    static Class<?> activity() {
        return TvActivity.class;
    }
}
